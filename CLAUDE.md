# OpenShift Skills Plugin

An OpenShift Console Dynamic Plugin for scheduled execution of LLM-driven agent skills. Written in Go (backend) and TypeScript/PatternFly 6 (frontend).

## Architecture

### Frontend (TypeScript, PatternFly 6)
- **`src/components/ChatPage.tsx`** - Interactive chat with agent loop (shell tool access in plugin pod). Renders markdown responses via `react-markdown` + `remark-gfm`. Per-session skill selection (expandable bar above messages, checkboxes in new chat modal). Active session highlighted in sidebar. Configurable temperature and max tokens per session.
- **`src/components/SkillsPage.tsx`** - Upload/manage SKILLS.md knowledge files
- **`src/components/SchedulePage.tsx`** - Schedule skills as cron jobs or run-once tasks with container image, SA, namespace, prompt, temperature, max token length. Toggle between recurring (cron) and run-once (delay notation: `now`, `+5m`, `+2h`).
- **`src/components/SettingsPage.tsx`** - Configure MaaS endpoints (registry or single-model), global system prompt, export/import SQLite database
- **`src/components/styles.css`** - Chat message styling including markdown rendering (code, tables, blockquotes, lists)
- **`src/utils/api.ts`** - API client with CSRF token handling (`X-CSRFToken` header from `csrf-token` cookie)
- **`console-extensions.json`** - Plugin routes under `/skills-plugin/{chat,skills,schedule,settings}` in admin perspective "Skills" nav section

### Backend (Go)
- **`cmd/backend/main.go`** - HTTP server (gorilla/mux), serves plugin static files + API routes, TLS support for OpenShift serving certs, initializes kube client on startup (non-fatal if unavailable)
- **`pkg/api/`** - REST handlers:
  - `chat.go` - `SendMessage` (POST) uses agent loop with local shell and passes conversation history for multi-turn context; `WebSocketChat` uses simple `maas.Complete()`. Both paths load only session-specific skills (falls back to all enabled skills if none selected).
  - `schedule.go` - Task scheduler supporting both cron (robfig/cron) and run-once (`time.AfterFunc`) execution:
    - **Container image set** → `executeContainerTask()`: creates executor pod → agent loop with `kube.ExecCommand` → stores results in chat session → deletes pod when done
    - **No container image** → `executeLLMTask()`: agent loop with local shell in plugin pod → stores results in chat session
    - **Run-once tasks**: `scheduleRunOnce(taskID, delay)` parses delay notation (`now`, `+30s`, `+5m`, `+2h`, `+1h30m`) and schedules via `time.AfterFunc`. Auto-disables after execution.
    - `ReloadScheduler()` - clears all cron entries and run-once timers, reloads from DB (used after database import)
    - Disabled tasks are skipped silently (no failed history entries)
  - `database.go` - `ExportDatabase` (GET, serves raw .db file) and `ImportDatabase` (POST multipart, replaces DB and reinitializes)
  - `skills.go` - CRUD for skills (upload SKILLS.md files)
  - `sessions.go` - CRUD for chat sessions with per-session skill selection (`session_skills` junction table). `PUT /sessions/{id}/skills` updates skill associations.
  - `maas_endpoints.go` - CRUD for MaaS endpoints, model listing, health checks. Supports two endpoint types:
    - **Model registry** (e.g. `http://maas.example.com/v1`) → lists models via `GET /v1/models`, returns per-model inference URLs
    - **Single-model URL** (e.g. `http://maas.example.com/prelude-maas/llama-32-3b/v1`) → auto-detected by `IsSingleModelURL()`, queries `GET {url}/models` to get model ID, shown as "Single model: name" in UI
    - API keys are never returned to the frontend (masked as `"****"`)
  - `config.go` - `GET/PUT /api/config?key=` for key-value config (e.g. `system_prompt`). `GetSystemPrompt()` helper used by session creation and scheduled task execution.
  - `helpers.go` - `jsonResponse()`, `httpError()`
- **`pkg/agent/agent.go`** - LLM-driven agent loop (OpenAI-compatible tool calling API):
  - `RunAgentLoop(completionsURL, token, model, systemPrompt, userMessage string, maxIterations int, shellExec ShellExecutor, opts *AgentOptions) (string, error)`
  - `ShellExecutor` type: `func(command string) string` - controls where commands run (nil = local `sh -c`)
  - `AgentOptions` struct: `Temperature float64`, `MaxTokens int`, `History []ChatMessage` (prior conversation messages inserted between system prompt and user message for multi-turn context)
  - Single `shell` tool definition, iterates up to `maxIterations` (default 15) calling LLM and executing tool calls
  - Strips `<think>` tags from responses (for reasoning models)
- **`pkg/agent/context.go`** - `newTimeoutContext()` helper
- **`pkg/kube/exec.go`** - Executor pod lifecycle for running agent commands in containers:
  - `CreateExecutorPod(namespace, serviceAccount, containerImage, taskName)` - creates pod with `sleep 3600`, waits for Running
  - `ExecCommand(ep, command, timeout)` - SPDY exec into the pod
  - `DeleteExecutorPod(ep)` - cleanup after agent loop completes
  - `Init()` - initializes in-cluster k8s client (`clientset`, `restConfig`)
- **`pkg/kube/jobs.go`** - `RunJob()` for simple container-only tasks (not used by agent loop), `sanitizeName()` for k8s-safe names
- **`pkg/maas/client.go`** - MaaS client:
  - `NewClient(baseURL, registryURL, apiKey, model)` - baseURL is model-specific inference URL, registryURL is MaaS API base
  - `Authenticate()` - exchanges bearer token → session token via `POST {RegistryURL}/v1/tokens`
  - `GetToken()` - exposes session token for agent loop
  - `ListModels()` - fetches `{RegistryURL}/v1/models` with session token, returns per-model URLs
  - `ListSingleModel(url)` - queries a single-model OpenAI-compatible endpoint's `/v1/models`, returns model info
  - `Complete()` - simple chat completion (used by WebSocket chat path)
  - `IsSingleModelURL(url)` - detects URLs with path segments before `/v1` (e.g. `.../llama-32-3b/v1`)
  - `ExtractModelName(url, fallback)` - extracts model name from URL, stripping `/v1` suffix first to avoid returning "v1" as model name. Used by all code paths (chat, schedule container, schedule LLM)
  - `ModelNameFromURL(url)` - simpler extraction for display purposes
- **`pkg/database/`** - SQLite (mattn/go-sqlite3) with WAL mode:
  - `database.go` - Init, migrate, schema for: `skills`, `sessions`, `messages`, `session_skills`, `scheduled_tasks`, `task_execution_history`, `maas_endpoints`, `config`
  - `GetDBPath()`, `Checkpoint()` (flush WAL), `Reinit(newDBPath)` (close, replace file, reopen with migrations)
  - `models.go` - Go structs: `Skill`, `Session` (includes `Temperature`, `MaxTokens`), `Message`, `ScheduledTask` (includes `Temperature`, `MaxTokens`, `RunOnce`, `RunOnceDelay`; `APIKey` uses `json:"-"`), `TaskExecutionHistory`, `MaaSEndpoint`, `Config`

## Deployment

### Container Build
- **`Containerfile`** - Multi-stage: Node 20 (frontend) → Go 1.25 (backend) → UBI9 minimal
  - Installs `sqlite-libs`, `tar`, `gzip`, `oc`, `kubectl` in final image
  - Frontend build requires `NODE_OPTIONS="--max-old-space-size=4096"` for webpack
  - Runs as UID 1001, port 9443

### Helm Chart (`chart/`)
- **`consoleplugin.yaml`** - ConsolePlugin CR (v1 API) with proxy: `endpoint.type: Service`, `authorization: UserToken`
- **`deployment.yaml`** - Sets `POD_NAMESPACE` via downward API, TLS from serving cert secret, PVC for SQLite data
- **`rbac.yaml`** - ClusterRole for batch jobs CRUD, serviceaccounts/namespaces list; namespace-scoped Role for pods create/delete, pods/log get, pods/exec create
- **`enable-plugin.yaml`** - Post-install/upgrade hook Job that patches Console CR to enable plugin (avoids ownership conflicts with other operators)
- **`values.yaml`** - Image: `quay.io/eformat/openshift-skills-plugin:latest`, PVC 2Gi, TLS enabled

### Deploy
```bash
helm upgrade --install skills-plugin chart/ -n skills-plugin --create-namespace
```

## Key Design Decisions

- **Agent loop in executor pods**: Scheduled skills with a container image create a temporary pod (`sleep 3600`), run the agent loop with commands exec'd into that pod via SPDY, then delete the pod. This keeps the plugin pod clean and allows per-task RBAC via ServiceAccount selection.
- **Scheduled task results in chat**: Both execution paths (container and LLM-only) create/reuse a chat session and store messages, so results appear in the Chat UI.
- **Console proxy for API calls**: All frontend API calls go through the OpenShift console proxy (`/api/proxy/plugin/openshift-skills-plugin/backend/...`), requiring CSRF tokens.
- **MaaS two-step auth**: Bearer token → `POST /v1/tokens` → session token. Session token used for all subsequent API calls.
- **Two endpoint types**: Supports both model registries (multi-model, lists via `/v1/models`) and single-model OpenAI-compatible URLs (auto-detected by path pattern). Single-model URLs have the model name before `/v1` in the path.
- **Model name extraction**: `ExtractModelName()` strips `/v1` suffix before taking the last path segment, preventing "v1" from being used as the model name. The actual model ID is confirmed via `GET /v1/models` API call when available.
- **Per-session skills**: Each chat session can have specific skills selected via the `session_skills` junction table. New chats pre-select all enabled skills. Skills can be changed on an active session via the expandable skills bar. If no skills are explicitly selected (e.g. old sessions), falls back to loading all enabled skills.
- **Multi-turn chat context**: `SendMessage` passes full conversation history from the DB to `RunAgentLoop` via `AgentOptions.History`, giving the LLM multi-turn context within a session while keeping sessions isolated from each other.
- **Run-once tasks**: Scheduled tasks can be set to "Run Once" mode with a delay (`now`, `+5m`, `+2h`). Uses `time.AfterFunc` instead of cron. After execution, the task is automatically disabled (`enabled = 0`). The cron schedule field stores a placeholder value for run-once tasks.
- **System prompt layering**: The full system prompt sent to the LLM is built in three layers:
  1. **Agent instructions** (hardcoded, read-only) — always prepended: shell tool usage, don't hallucinate, use temp files for scripts. Shown as a disabled read-only TextArea in Settings so users can see it.
  2. **Global system prompt** (configurable in Settings) — stored in the `config` table as `system_prompt`, with a sensible default defined in `DefaultSystemPrompt`. Applied to all new chat sessions (baked in at creation time) and all scheduled task executions (read at runtime via `GetSystemPrompt()`).
  3. **Skills content** — appended last, per-session or per-task.
- **Shell tool JSON repair**: `repairToolCallJSON()` in agent.go attempts to fix truncated/malformed JSON in LLM-generated tool call arguments (common with smaller models producing unescaped quotes in shell commands). The shell tool description also instructs models to write complex scripts to temp files via heredocs to avoid escaping issues.
- **Per-session LLM settings**: Each chat session stores its own `temperature` and `max_tokens` (defaults 0.2 and 2048), passed to `RunAgentLoop` via `AgentOptions`.
- **Token security**: API keys are never returned to the frontend. `ScheduledTask.APIKey` uses `json:"-"`, `ListEndpoints` returns `"****"`, Settings UI shows "Configured"/"Not set".
- **Database portability**: Export/import of the SQLite database file via Settings page for migrating config between clusters. Import triggers `Reinit()` + `ReloadScheduler()`.
- **No `<Page>` wrapper**: Console layout provides its own wrapper; adding `<Page>` causes a grey gap.
- **Chat nav route**: Uses `/skills-plugin/chat` (not `/skills-plugin`) to avoid prefix-match highlighting all nav items.

## Go Module
- Module: `github.com/eformat/openshift-skills-plugin`
- Requires: Go >= 1.25
- Key deps: `gorilla/mux`, `gorilla/websocket`, `mattn/go-sqlite3`, `robfig/cron/v3`, `k8s.io/client-go`

## Frontend Dependencies
- Key deps: `react-markdown`, `remark-gfm`, `@patternfly/react-core@6`, `@openshift-console/dynamic-plugin-sdk`

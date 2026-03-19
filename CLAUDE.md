# OpenShift Skills Plugin

An OpenShift Console Dynamic Plugin for scheduled execution of LLM-driven agent skills. Written in Go (backend) and TypeScript/PatternFly 6 (frontend).

## Architecture

### Frontend (TypeScript, PatternFly 6)
- **`src/components/ChatPage.tsx`** - Interactive chat with agent loop (shell tool access in plugin pod). Renders markdown responses via `react-markdown` + `remark-gfm`. Per-session skill selection (expandable bar above messages, checkboxes in new chat modal). Active session highlighted in sidebar. Configurable temperature and max tokens per session.
- **`src/components/SkillsPage.tsx`** - Upload/manage SKILLS.md knowledge files
- **`src/components/SchedulePage.tsx`** - Schedule skills as cron jobs or run-once tasks with container image, SA, namespace, prompt, temperature, max token length. Toggle between recurring (cron) and run-once (delay notation: `now`, `+5m`, `+2h`). Task cards show the user prompt in the description list. Per-task "Delete history" button (inline with the execution history toggle) clears execution history and resets run count/last run.
- **`src/components/SettingsPage.tsx`** - Configure MaaS endpoints (registry or single-model), global system prompt, export/import SQLite database
- **`src/components/styles.css`** - Chat message styling including markdown rendering (code, tables, blockquotes, lists)
- **`src/utils/api.ts`** - API client with CSRF token handling (`X-CSRFToken` header from `csrf-token` cookie)
- **`console-extensions.json`** - Plugin routes under `/skills-plugin/{chat,skills,schedule,settings}` in admin perspective "Skills" nav section

### Backend (Go)
- **`cmd/backend/main.go`** - HTTP server (gorilla/mux), serves plugin static files + API routes, TLS support for OpenShift serving certs, initializes kube client on startup (non-fatal if unavailable), initializes MLflow tracing (`mlflow.Init("")`) with deferred `Shutdown()`
- **`pkg/api/`** - REST handlers:
  - `chat.go` - `SendMessage` (POST) uses agent loop with local shell and passes conversation history for multi-turn context; `WebSocketChat` uses simple `maas.Complete()`. Both paths load only session-specific skills (falls back to all enabled skills if none selected).
  - `schedule.go` - Task scheduler supporting both cron (robfig/cron) and run-once (`time.AfterFunc`) execution:
    - **Container image set** → `executeContainerTask()`: creates executor pod → agent loop with `kube.ExecCommand` → stores results in chat session → deletes pod when done
    - **No container image** → `executeLLMTask()`: agent loop with local shell in plugin pod → stores results in chat session
    - **Run-once tasks**: `scheduleRunOnce(taskID, delay)` parses delay notation (`now`, `+30s`, `+5m`, `+2h`, `+1h30m`) and schedules via `time.AfterFunc`. Auto-disables after execution.
    - `ReloadScheduler()` - clears all cron entries and run-once timers, reloads from DB (used after database import)
    - `DeleteTaskHistory` (DELETE) - clears execution history for a task and resets `run_count`/`last_run`
    - **Concurrency guard**: `sync.Map` (`runningTasks`) prevents the same task from executing concurrently — if a cron fires while the previous run is still active, the new invocation is skipped
    - **Session reuse safety**: `getOrCreateSession()` verifies the referenced chat session still exists before reusing it; if deleted by the user, creates a new session
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
  - `RunAgentLoop(ctx, completionsURL, token, model, systemPrompt, userMessage string, maxIterations int, shellExec ShellExecutor, opts *AgentOptions) (*AgentResult, error)`
  - `ShellExecutor` type: `func(command string) string` - controls where commands run (nil = local `sh -c`)
  - `AgentResult` struct: `Response string`, `Iterations int`, `ToolCalls int`
  - `AgentOptions` struct: `Temperature float64`, `MaxTokens int`, `History []ChatMessage`, `Source string` (trace label), `ExperimentName string` (MLflow experiment)
  - Single `shell` tool definition, iterates up to `maxIterations` (default 15) calling LLM and executing tool calls
  - Instrumented with OTel spans: root AGENT span → CHAT_MODEL per LLM call → TOOL per shell execution
  - Strips `<think>` tags from responses (for reasoning models). When the final response is empty after stripping, falls back to the last substantive assistant message or tool output
- **`pkg/agent/context.go`** - `newTimeoutContext()` helper
- **`pkg/kube/exec.go`** - Executor pod lifecycle for running agent commands in containers:
  - `CreateExecutorPod(namespace, serviceAccount, containerImage, taskName)` - creates pod with `sleep 3600` and random suffix in pod name (avoids collisions), waits for Running
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
- **`pkg/mlflow/client.go`** - MLflow/OpenTelemetry tracing integration:
  - `Init(mlflowURL)` - validates MLflow connectivity (falls back to `MLFLOW_TRACKING_URI` env var), stores base URL/host for lazy tracer creation
  - `getTracerForExperiment(experimentName)` - lazily creates and caches a `TracerProvider` per experiment, each with its own OTLP HTTP exporter configured with `x-mlflow-experiment-id` header. Creates MLflow experiments via v2 API (`getOrCreateExperiment`)
  - `StartAgentSpan(ctx, experimentName, model, source, userMessage, temperature, maxTokens)` - root AGENT span routed to the per-experiment tracer
  - `StartLLMSpan(ctx, model, iteration)` - CHAT_MODEL child span (inherits tracer from parent context)
  - `StartToolSpan(ctx, toolName, arguments)` - TOOL child span
  - `EndSpanOK(span, output)` / `EndSpanError(span, err)` - span completion helpers
  - `Shutdown(ctx)` - flushes all cached tracer providers
  - MLflow span attributes: `mlflow.spanType` (AGENT/CHAT_MODEL/TOOL), `mlflow.spanInputs`, `mlflow.spanOutputs`
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
- **`deployment.yaml`** - Sets `POD_NAMESPACE` via downward API, TLS from serving cert secret, PVC for SQLite data, `MLFLOW_TRACKING_URI` env var (when mlflow enabled, points to internal mlflow service)
- **`rbac.yaml`** - ClusterRole for batch jobs CRUD, serviceaccounts/namespaces list; namespace-scoped Role for pods create/delete, pods/log get, pods/exec create
- **`enable-plugin.yaml`** - Post-install/upgrade hook Job that patches Console CR to enable plugin (avoids ownership conflicts with other operators)
- **`values.yaml`** - Image: `quay.io/eformat/openshift-skills-plugin:latest`, PVC 2Gi, TLS enabled, mlflow disabled by default
- **MLflow templates** (all gated by `.Values.mlflow.enabled`):
  - `mlflow-deployment.yaml` - MLflow server using args-from-values pattern (no hardcoded command), optional oauth-proxy sidecar (HTTPS :8443, serving cert TLS, upstream to localhost mlflow port). Requires `--disable-security-middleware` for OTLP endpoint.
  - `mlflow-service.yaml` - ClusterIP service exposing mlflow http port + oauth port (8443) when oauth enabled; serving cert annotation for auto-provisioned TLS secret
  - `mlflow-pvc.yaml` - PersistentVolumeClaim for mlflow data
  - `mlflow-serviceaccount.yaml` - ServiceAccount with `oauth-redirectreference` annotation pointing to mlflow route
  - `mlflow-route.yaml` - Route always created when mlflow enabled. Edge TLS without oauth, reencrypt TLS with oauth.

### Deploy
```bash
helm upgrade --install skills-plugin chart/ -n skills-plugin --create-namespace
```

## Key Design Decisions

- **Agent loop in executor pods**: Scheduled skills with a container image create a temporary pod (`sleep 3600`), run the agent loop with commands exec'd into that pod via SPDY, then delete the pod. This keeps the plugin pod clean and allows per-task RBAC via ServiceAccount selection.
- **Scheduled task results in chat**: Both execution paths (container and LLM-only) create/reuse a chat session and store messages, so results appear in the Chat UI. `getOrCreateSession()` validates the session still exists before reusing (handles user-deleted sessions gracefully).
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
- **MLflow OTel tracing**: All agent loop executions (chat and scheduled tasks) are traced via OpenTelemetry and exported to MLflow's OTLP endpoint (`/v1/traces`). Hierarchical spans: AGENT (root) → CHAT_MODEL (per LLM call) → TOOL (per shell execution). Each chat session maps to a separate MLflow experiment (by session name); scheduled tasks use `"Scheduled: " + task.Name`. Per-experiment `TracerProvider` instances are lazily created and cached, each with its own OTLP HTTP exporter configured with the `x-mlflow-experiment-id` header. The `ghcr.io/mlflow/mlflow` image requires an explicit `mlflow server` command (Python entrypoint, not a server by default) and `--disable-security-middleware` for the OTLP endpoint to accept traces without auth.
- **MLflow with OAuth proxy**: Optional MLflow deployment (`mlflow.enabled: false` by default) with OpenShift oauth-proxy sidecar for SSO authentication. Uses the standard OpenShift pattern: serving cert annotation on service auto-provisions a TLS secret, oauth-proxy serves HTTPS on :8443 with those certs, route uses `reencrypt` termination, and the ServiceAccount has the `oauth-redirectreference` annotation. Route is always created when mlflow is enabled (edge TLS without oauth, reencrypt with oauth). All resources named `{{ .Values.plugin.name }}-mlflow`.

## Go Module
- Module: `github.com/eformat/openshift-skills-plugin`
- Requires: Go >= 1.25
- Key deps: `gorilla/mux`, `gorilla/websocket`, `mattn/go-sqlite3`, `robfig/cron/v3`, `k8s.io/client-go`, `go.opentelemetry.io/otel` (SDK + OTLP HTTP exporter)

## Frontend Dependencies
- Key deps: `react-markdown`, `remark-gfm`, `@patternfly/react-core@6`, `@openshift-console/dynamic-plugin-sdk`

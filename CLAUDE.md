# OpenShift Skills Plugin

An OpenShift Console Dynamic Plugin for scheduled execution of LLM-driven agent skills.written in Go (backend) and TypeScript/PatternFly 6 (frontend).

## Architecture

### Frontend (TypeScript, PatternFly 6)
- **`src/components/ChatPage.tsx`** - Interactive chat with agent loop (shell tool access in plugin pod)
- **`src/components/SkillsPage.tsx`** - Upload/manage SKILLS.md knowledge files
- **`src/components/SchedulePage.tsx`** - Schedule skills as cron jobs with container image, SA, namespace
- **`src/components/SettingsPage.tsx`** - Configure MaaS endpoints
- **`src/utils/api.ts`** - API client with CSRF token handling (`X-CSRFToken` header from `csrf-token` cookie)
- **`console-extensions.json`** - Plugin routes under `/skills-plugin/{chat,skills,schedule,settings}` in admin perspective "Skills" nav section

### Backend (Go)
- **`cmd/backend/main.go`** - HTTP server (gorilla/mux), serves plugin static files + API routes, TLS support for OpenShift serving certs, initializes kube client on startup (non-fatal if unavailable)
- **`pkg/api/`** - REST handlers:
  - `chat.go` - `SendMessage` (POST) uses agent loop with local shell; `WebSocketChat` uses simple `maas.Complete()`
  - `schedule.go` - Cron scheduler (robfig/cron), two execution paths:
    - **Container image set** → `executeContainerTask()`: creates executor pod → agent loop with `kube.ExecCommand` → deletes pod when done
    - **No container image** → `executeLLMTask()`: agent loop with local shell in plugin pod
  - `skills.go` - CRUD for skills (upload SKILLS.md files)
  - `sessions.go` - CRUD for chat sessions
  - `maas_endpoints.go` - CRUD for MaaS endpoints, model listing, health checks
  - `helpers.go` - `jsonResponse()`, `httpError()`
- **`pkg/agent/agent.go`** - LLM-driven agent loop (OpenAI-compatible tool calling API):
  - `RunAgentLoop(completionsURL, token, model, systemPrompt, userMessage string, maxIterations int, shellExec ShellExecutor) (string, error)`
  - `ShellExecutor` type: `func(command string) string` - controls where commands run (nil = local `sh -c`)
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
  - `Complete()` - simple chat completion (used by WebSocket chat path)
  - Model name is extracted from URL last path segment (e.g. `llama-32-3b` from `.../llama-32-3b`)
- **`pkg/database/`** - SQLite (mattn/go-sqlite3) with WAL mode:
  - `database.go` - Init, migrate, schema for: `skills`, `sessions`, `messages`, `scheduled_tasks`, `task_execution_history`, `maas_endpoints`, `config`
  - `models.go` - Go structs: `Skill`, `Session`, `Message`, `ScheduledTask`, `TaskExecutionHistory`, `MaaSEndpoint`, `Config`

## Deployment

### Container Build
- **`Containerfile`** - Multi-stage: Node 20 (frontend) → Go 1.25 (backend) → UBI9 minimal
  - Installs `sqlite-libs`, `tar`, `gzip`, `oc`, `kubectl` in final image
  - Runs as UID 1001, port 9443

### Helm Chart (`chart/`)
- **`consoleplugin.yaml`** - ConsolePlugin CR (v1 API) with proxy: `endpoint.type: Service`, `authorization: UserToken`
- **`deployment.yaml`** - Sets `POD_NAMESPACE` via downward API, TLS from serving cert secret, PVC for SQLite data
- **`rbac.yaml`** - ClusterRole with: batch jobs CRUD, pods create/delete, pods/log get, pods/exec create, serviceaccounts/namespaces list
- **`enable-plugin.yaml`** - Post-install/upgrade hook Job that patches Console CR to enable plugin (avoids ownership conflicts with other operators)
- **`values.yaml`** - Image: `quay.io/eformat/openshift-skills-plugin:latest`, PVC 2Gi, TLS enabled

### Deploy
```bash
helm upgrade --install skills-plugin chart/ -n skills-plugin --create-namespace
```

## Key Design Decisions

- **Agent loop in executor pods**: Scheduled skills with a container image create a temporary pod (`sleep 3600`), run the agent loop with commands exec'd into that pod via SPDY, then delete the pod. This keeps the plugin pod clean and allows per-task RBAC via ServiceAccount selection.
- **Console proxy for API calls**: All frontend API calls go through the OpenShift console proxy (`/api/proxy/plugin/openshift-skills-plugin/backend/...`), requiring CSRF tokens.
- **MaaS two-step auth**: Bearer token → `POST /v1/tokens` → session token. Session token used for all subsequent API calls.
- **Model name from URL**: Inference endpoints expect the URL path segment as model name (e.g. `llama-32-3b`), not the registry ID (e.g. `RedHatAI/llama-3.2-3b-instruct`).
- **No `<Page>` wrapper**: Console layout provides its own wrapper; adding `<Page>` causes a grey gap.
- **Chat nav route**: Uses `/skills-plugin/chat` (not `/skills-plugin`) to avoid prefix-match highlighting all nav items.

## Go Module
- Module: `github.com/eformat/openshift-skills-plugin`
- Requires: Go >= 1.25
- Key deps: `gorilla/mux`, `gorilla/websocket`, `mattn/go-sqlite3`, `robfig/cron/v3`, `k8s.io/client-go`

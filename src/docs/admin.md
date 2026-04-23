# Administration

<span class="badge">Topics: RBAC, Multi-Tenancy, Deployment</span>

---

## RBAC and Multi-Tenancy

The plugin implements per-user data ownership and role-based access control using OpenShift RBAC primitives.

### User Roles

Two ClusterRoles are deployed with the Helm chart:

| ClusterRole | Verbs | Purpose |
|-------------|-------|---------|
| `skills-plugin-user` | `use` | Basic plugin access |
| `skills-plugin-admin` | `use`, `admin` | Full admin access |

Bind roles to users with:

```bash
oc adm policy add-cluster-role-to-user skills-plugin-user <username>
oc adm policy add-cluster-role-to-user skills-plugin-admin <username>
```

### Ownership Model

Every resource (sessions, tasks, skills, endpoints) has an `owner` field set at creation time. The ownership rules are:

| Resource | Non-Admin Visibility | Admin Visibility |
|----------|---------------------|-----------------|
| **Chat Sessions** | Own sessions only | All sessions |
| **Scheduled Tasks** | Own tasks only | All tasks |
| **Skills** | Own + Global | All skills |
| **MaaS Endpoints** | Own + Global | All endpoints |

### Global Resources

Skills and MaaS endpoints can be shared globally via the **Share globally** toggle. Global resources are readable by all users but only editable by the owner or admins.

---

## Namespace Permissions for Scheduled Tasks

### User Authorization

When a user creates or updates a scheduled task with a container image, the backend performs a **SubjectAccessReview** against the user's identity to verify they have `create`, `exec`, and `delete` permissions on pods in the target namespace. If any permission is missing, the request is rejected with a 403 error listing the specific missing permissions.

This prevents privilege escalation where a user could specify a privileged namespace and service account, relying on the plugin's service account to create the executor pod on their behalf.

### Plugin Service Account Permissions

The plugin service account has pod management permissions (create, exec, delete) only in its own namespace by default. To run container-based scheduled tasks in other namespaces, an admin must grant access:

```bash
oc -n <target-namespace> create rolebinding openshift-skills-plugin-pod-manager \
  --clusterrole=openshift-skills-plugin-pod-manager \
  --serviceaccount=skills-plugin:skills-plugin
```

When creating or editing a scheduled task with a container image, the UI automatically checks whether the plugin has the required permissions in the selected namespace. If not, a warning is displayed with the exact command to run.

<div class="alert alert-info"><strong>Two-level authorization:</strong> Both checks must pass for a container task to work. The user must have pod permissions in the namespace (enforced at task creation/update time), and the plugin service account must also have pod permissions there (checked at runtime). This ensures neither the user nor the plugin can escalate privileges independently.</div>

---

## Agent Shell Isolation

The plugin pod runs two containers:

| Container | Purpose | SA Token | Kube Access | Database Access |
|-----------|---------|----------|-------------|-----------------|
| **plugin** | Go backend, API server, scheduler | Projected token at non-standard path | Yes (manages executor pods, auth) | Yes (PVC mounted) |
| **agent-shell** | LLM agent shell commands (chat and LLM-only scheduled tasks) | None | None | None |

When the LLM agent executes shell commands (via the `shell` tool), they run inside the `agent-shell` sidecar container which has **no Kubernetes service account token mounted**, **no access to the Kubernetes API**, and **no access to the application database**. The SQLite data PVC is only mounted in the `plugin` container. This prevents the LLM from accessing cluster resources, reading secrets, reading API keys or session data, or performing privileged operations — even if it attempts to construct its own kubeconfig or call the API directly.

Container-based scheduled tasks (with a container image specified) run in separate executor pods with their own ServiceAccount, unaffected by this isolation.

---

## Admin-Only Features

| Feature | Location |
|---------|----------|
| Edit system prompt | Settings page |
| Export database | Settings page |
| Import database | Settings page |
| View all users' sessions | Chat page |
| View all users' tasks | Schedule page |
| Edit/delete any resource | All pages |

---

## Deployment

### Basic Install

```bash
helm upgrade --install skills-plugin chart/ -n skills-plugin --create-namespace
```

### With MLflow Observability

```bash
COOKIE=$(openssl rand -base64 32)
helm upgrade --install skills-plugin chart/ -n skills-plugin --create-namespace \
  --set mlflow.enabled=true \
  --set mlflow.oauth.cookieSecret=$COOKIE
```

### Key Helm Values

| Value | Default | Description |
|-------|---------|-------------|
| `plugin.image` | `quay.io/eformat/openshift-skills-plugin:latest` | Plugin container image |
| `plugin.pvc.size` | `2Gi` | PVC size for SQLite data |
| `mlflow.enabled` | `false` | Enable MLflow tracing |
| `mlflow.oauth.enabled` | `false` | Enable OAuth proxy for MLflow |

---

## MLflow Tracing

When MLflow is enabled, all agent loop executions (chat and scheduled tasks) are traced via OpenTelemetry:

- **AGENT** span (root) per agent invocation
- **CHAT_MODEL** span per LLM API call
- **TOOL** span per shell command execution

Each chat session maps to a separate MLflow experiment. Scheduled tasks use `"Scheduled: " + task name`.

---

## Next Steps

- [Settings](settings) -- configure endpoints and system prompt
- [FAQ](faq) -- common questions and troubleshooting

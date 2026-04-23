# Schedule

<span class="badge">Topics: Cron, Run-Once, Tasks, Automation</span>

---

## Overview

The Schedule page lets you automate skill execution as recurring cron jobs or one-off tasks. Task results are stored as chat sessions, so you can review them in the Chat page.

![images/schedule-page.png](images/schedule-page.png)

---

## Creating a Scheduled Task

Click **New Scheduled Task** to open the creation form.

![images/schedule-new-modal.png](images/schedule-new-modal.png)

| Field | Description |
|-------|-------------|
| **Name** | A descriptive name for the task |
| **Prompt** | Instructions to send to the agent |
| **Recurring (Cron)** | Toggle between recurring cron and run-once mode |
| **Cron Schedule** | Cron expression (e.g. `0 9 * * *` for daily at 9am) |
| **Skill** | Select a skill to use, or "None" for manual prompt only |
| **ServiceAccount** | Kubernetes SA for container tasks (default: `default`) |
| **Namespace** | Namespace for container tasks |
| **Container Image** | Optional container image for isolated execution |
| **MaaS Endpoint** | Model serving endpoint to use |
| **Model** | Specific model to use |
| **Temperature** | LLM temperature setting |
| **Max Tokens** | Maximum response length |

---

## Execution Modes

### Recurring (Cron)

When the **Recurring (Cron)** toggle is enabled, the task runs on a cron schedule. Standard cron expressions are supported:

| Expression | Meaning |
|-----------|---------|
| `*/5 * * * *` | Every 5 minutes |
| `0 9 * * *` | Daily at 9:00 AM |
| `0 0 * * 1` | Every Monday at midnight |
| `0 */6 * * *` | Every 6 hours |

### Run Once

When the toggle is off, the task runs once with a delay:

| Delay | Meaning |
|-------|---------|
| `now` | Execute immediately |
| `+30s` | After 30 seconds |
| `+5m` | After 5 minutes |
| `+2h` | After 2 hours |
| `+1h30m` | After 1 hour 30 minutes |

<div class="alert alert-info">Run-once tasks are automatically disabled after execution.</div>

---

## Container vs Plugin Execution

### With Container Image

When a container image is specified, the task:
1. Creates a temporary executor pod in the specified namespace
2. Runs the agent loop with commands exec'd into that pod via SPDY
3. Deletes the pod when done

This keeps the plugin pod clean and allows per-task RBAC via ServiceAccount selection.

<div class="alert alert-warning"><strong>User authorization:</strong> When you create or update a container task, the backend verifies that <em>you</em> have permissions to create, exec into, and delete pods in the target namespace. If you lack any of these permissions, the request is rejected with a 403 error listing the missing permissions. This prevents users from scheduling tasks in privileged namespaces they don't have access to.</div>

<div class="alert alert-warning">The plugin service account must <em>also</em> have pod management permissions in the target namespace. If it doesn't, a warning will appear in the form with the exact <code>oc</code> command to grant access. See <a href="admin">Administration</a> for details.</div>

### Without Container Image (LLM-only)

When no container image is specified, the agent loop runs commands in an isolated sidecar container within the plugin pod. This sidecar has **no Kubernetes credentials** — the LLM cannot access the cluster API, read secrets, or perform privileged operations. See [Agent Shell Isolation](admin) for details.

---

## Task Management

Each task card shows:
- **Name**, **schedule**, and **prompt**
- **Enabled/Disabled** toggle
- **Edit** and **Delete** buttons
- **Execution history** (expandable) with status, timestamps, and results
- **Delete history** button to clear past executions and reset run count
- **Owner** label (visible to admins)

<div class="alert alert-warning">A concurrency guard prevents the same task from running simultaneously. If a cron fires while the previous run is still active, the new run is skipped.</div>

---

## Viewing Results

Task execution results are stored as chat sessions. After a task runs, you can view the full agent conversation (including tool calls and responses) in the **Chat** page.

---

## Next Steps

- [Chat](chat) -- view scheduled task results
- [Settings](settings) -- configure MaaS endpoints

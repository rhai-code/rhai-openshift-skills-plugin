const PLUGIN_NAME = 'openshift-skills-plugin';
const PROXY_BASE = `/api/proxy/plugin/${PLUGIN_NAME}/backend/api`;

function getCSRFToken(): string {
  const match = document.cookie.match(/(?:^|;\s*)csrf-token=([^;]*)/);
  return match ? match[1] : '';
}

async function request<T>(path: string, options?: RequestInit): Promise<T> {
  const headers: Record<string, string> = {
    'Content-Type': 'application/json',
    'X-CSRFToken': getCSRFToken(),
    ...(options?.headers as Record<string, string>),
  };
  const resp = await fetch(`${PROXY_BASE}${path}`, {
    ...options,
    headers,
  });
  if (!resp.ok) {
    const err = await resp.json().catch(() => ({ error: resp.statusText }));
    throw new Error(err.error || resp.statusText);
  }
  return resp.json();
}

// Skills
export interface Skill {
  id: number;
  name: string;
  description: string;
  content: string;
  enabled: boolean;
  created_at: string;
  updated_at: string;
}

export const listSkills = () => request<Skill[]>('/skills');
export const getSkill = (id: number) => request<Skill>(`/skills/${id}`);
export const createSkill = (data: { name: string; description: string; content: string }) =>
  request<{ id: number }>('/skills', { method: 'POST', body: JSON.stringify(data) });
export const updateSkill = (id: number, data: Partial<Skill>) =>
  request('/skills/' + id, { method: 'PATCH', body: JSON.stringify(data) });
export const deleteSkill = (id: number) =>
  request('/skills/' + id, { method: 'DELETE' });

export async function uploadSkill(file: File, name?: string, description?: string) {
  const form = new FormData();
  form.append('file', file);
  if (name) form.append('name', name);
  if (description) form.append('description', description);
  const resp = await fetch(`${PROXY_BASE}/skills/upload`, {
    method: 'POST',
    headers: { 'X-CSRFToken': getCSRFToken() },
    body: form,
  });
  if (!resp.ok) throw new Error('Upload failed');
  return resp.json();
}

// Sessions
export interface Session {
  id: string;
  name: string;
  provider: string;
  model: string;
  base_url?: string;
  temperature: number;
  max_tokens: number;
  created_at: string;
}

export interface Message {
  id: number;
  session_id: string;
  role: string;
  content: string;
  timestamp: string;
}

export const listSessions = () => request<Session[]>('/sessions');
export const createSession = (data: { provider: string; model: string; base_url?: string; skill_ids?: number[]; temperature?: number; max_tokens?: number }) =>
  request<{ id: string; name: string }>('/sessions', { method: 'POST', body: JSON.stringify(data) });
export const getSession = (id: string) =>
  request<{ session: Session; messages: Message[]; skill_ids: number[] }>('/sessions/' + id);
export const updateSessionSkills = (id: string, skillIds: number[]) =>
  request('/sessions/' + id + '/skills', { method: 'PUT', body: JSON.stringify({ skill_ids: skillIds }) });
export const deleteSession = (id: string) =>
  request('/sessions/' + id, { method: 'DELETE' });

// Chat
export const sendMessage = (sessionId: string, message: string) =>
  request<{ response: string }>('/sessions/' + sessionId + '/messages', {
    method: 'POST',
    body: JSON.stringify({ message }),
  });

// Scheduled Tasks
export interface ScheduledTask {
  id: number;
  name: string;
  description: string;
  skill_id?: number;
  schedule: string;
  service_account: string;
  namespace: string;
  enabled: boolean;
  last_run?: string;
  run_count: number;
  session_id?: string;
  provider: string;
  model: string;
  base_url?: string;
  container_image?: string;
  temperature: number;
  max_tokens: number;
  run_once: boolean;
  run_once_delay?: string;
  created_at: string;
}

export interface TaskHistory {
  id: number;
  task_id: number;
  started_at: string;
  completed_at?: string;
  duration_ms?: number;
  status: string;
  error_message?: string;
  output?: string;
}

export const listScheduledTasks = () => request<ScheduledTask[]>('/scheduled-tasks');
export const createScheduledTask = (data: Partial<ScheduledTask>) =>
  request<{ id: number }>('/scheduled-tasks', { method: 'POST', body: JSON.stringify(data) });
export const updateScheduledTask = (id: number, data: Partial<ScheduledTask>) =>
  request('/scheduled-tasks/' + id, { method: 'PUT', body: JSON.stringify(data) });
export const deleteScheduledTask = (id: number) =>
  request('/scheduled-tasks/' + id, { method: 'DELETE' });
export const toggleScheduledTask = (id: number, enabled: boolean) =>
  request('/scheduled-tasks/' + id + '/toggle', { method: 'POST', body: JSON.stringify({ enabled }) });
export const getTaskHistory = (id: number) =>
  request<TaskHistory[]>('/scheduled-tasks/' + id + '/history');

// MaaS Endpoints
export interface MaaSEndpoint {
  id: number;
  name: string;
  url: string;
  api_key?: string;
  provider_type: string;
  enabled: boolean;
  single_model?: boolean;
  model_name?: string;
  created_at: string;
}

export const listEndpoints = () => request<MaaSEndpoint[]>('/maas-endpoints');
export const createEndpoint = (data: { name: string; url: string; api_key?: string; provider_type?: string }) =>
  request<{ id: number }>('/maas-endpoints', { method: 'POST', body: JSON.stringify(data) });
export const updateEndpoint = (id: number, data: Partial<MaaSEndpoint>) =>
  request('/maas-endpoints/' + id, { method: 'PUT', body: JSON.stringify(data) });
export const deleteEndpoint = (id: number) =>
  request('/maas-endpoints/' + id, { method: 'DELETE' });
export const healthCheckEndpoint = (id: number) =>
  request<{ healthy: boolean; error?: string }>('/maas-endpoints/' + id + '/health');

// Models
export interface ModelInfo {
  id: string;
  url: string;
  display_name: string;
  ready: boolean;
  owned_by: string;
}

export const listModels = (endpointId?: number) =>
  request<ModelInfo[]>('/models' + (endpointId ? '?endpoint_id=' + endpointId : ''));

// Health / config
export const getHealth = () =>
  request<{ status: string; namespace: string }>('/health');

// Database export/import
export async function exportDatabase(): Promise<void> {
  const resp = await fetch(`${PROXY_BASE}/database/export`, {
    headers: { 'X-CSRFToken': getCSRFToken() },
  });
  if (!resp.ok) throw new Error('Export failed');
  const blob = await resp.blob();
  const url = URL.createObjectURL(blob);
  const a = document.createElement('a');
  a.href = url;
  a.download = 'skills.db';
  a.click();
  URL.revokeObjectURL(url);
}

export async function importDatabase(file: File): Promise<{ message: string }> {
  const form = new FormData();
  form.append('database', file);
  const resp = await fetch(`${PROXY_BASE}/database/import`, {
    method: 'POST',
    headers: { 'X-CSRFToken': getCSRFToken() },
    body: form,
  });
  if (!resp.ok) {
    const err = await resp.json().catch(() => ({ error: resp.statusText }));
    throw new Error(err.error || resp.statusText);
  }
  return resp.json();
}

import { request } from './client';
import type { CanvasDoc, Op } from '@/features/canvas/kernel/types';
import type {
  AppendOpsResult, Asset, CanvasMeta, Credential, CredentialInput, GeneratePayload, ImportPayload,
  ImportResult, MetaResponse, Model, Plugin, PluginInstallInput, ProbeResult, Project, Prompt,
  PromptSource, PromptSourceInput, Provider, ProviderInput, Run as RunDTO, Session, SyncResult,
  User, Workspace,
} from './types';

/** 与 internal/api/router.go 路由一一对应；漂移由 make gen 检出。 */
export const api = {
  meta: () => request<MetaResponse>('/api/v1/meta'),
  healthz: () => request<{ status: string }>('/healthz'),

  login: (email: string, password: string) =>
    request<Session>('/api/v1/auth/login', { body: { email, password } }),
  register: (email: string, name: string, password: string) =>
    request<Session>('/api/v1/auth/register', { body: { email, name, password } }),
  logout: () => request<{ ok: boolean }>('/api/v1/auth/logout', { method: 'POST' }),
  me: () => request<{ user: User; workspaces: Workspace[] }>('/api/v1/me'),

  listWorkspaces: () => request<{ items: Workspace[] }>('/api/v1/workspaces'),
  createWorkspace: (name: string) => request<Workspace>('/api/v1/workspaces', { body: { name } }),
  listProjects: (wid: string) => request<{ items: Project[] }>(`/api/v1/workspaces/${wid}/projects`),
  createProject: (wid: string, name: string, description = '') =>
    request<Project>(`/api/v1/workspaces/${wid}/projects`, { body: { name, description } }),

  listCanvases: (pid: string) => request<{ items: CanvasMeta[] }>(`/api/v1/projects/${pid}/canvases`),
  createCanvas: (pid: string, name: string) =>
    request<{ canvas: CanvasMeta }>(`/api/v1/projects/${pid}/canvases`, { body: { name } }),
  getCanvas: (cid: string) => request<CanvasDoc>(`/api/v1/canvases/${cid}`),
  appendOps: (cid: string, baseVersion: number, ops: Op[], idempotencyKey?: string) =>
    request<AppendOpsResult>(`/api/v1/canvases/${cid}/ops`, { body: { baseVersion, ops }, idempotencyKey }),
  exportCanvas: (cid: string) => request<unknown>(`/api/v1/canvases/${cid}/export`),
  importCanvas: (pid: string, payload: ImportPayload, idempotencyKey?: string) =>
    request<ImportResult>(`/api/v1/canvases/${pid}/import`, { body: payload, idempotencyKey }),

  createRun: (cid: string, targetNodes: string[], idempotencyKey?: string) =>
    request<RunDTO>(`/api/v1/canvases/${cid}/runs`, { body: { targetNodes }, idempotencyKey }),
  getRun: (rid: string) => request<RunDTO>(`/api/v1/runs/${rid}`),
  listRuns: (cid: string, limit = 20) =>
    request<{ items: RunDTO[] }>(`/api/v1/canvases/${cid}/runs?limit=${limit}`),
  cancelRun: (rid: string) => request<{ ok: boolean }>(`/api/v1/runs/${rid}/cancel`, { method: 'POST' }),
  replayRun: (rid: string) => request<RunDTO>(`/api/v1/runs/${rid}/replay`, { method: 'POST' }),

  generate: (wid: string, payload: GeneratePayload, idempotencyKey?: string) =>
    request<RunDTO>(`/api/v1/workspaces/${wid}/generate`, { body: payload, idempotencyKey }),

  listAssets: (wid: string, kind = '', limit = 20, cursor = '') =>
    request<{ items: Asset[]; cursor: string }>(
      `/api/v1/workspaces/${wid}/assets?limit=${limit}&kind=${encodeURIComponent(kind)}&cursor=${encodeURIComponent(cursor)}`,
    ),
  getAsset: (aid: string, wid: string) =>
    request<Asset>(`/api/v1/assets/${aid}?workspaceId=${encodeURIComponent(wid)}`),
  deleteAsset: (aid: string, wid: string) =>
    request<{ ok: boolean }>(`/api/v1/assets/${aid}?workspaceId=${encodeURIComponent(wid)}`, { method: 'DELETE' }),
  assetRawUrl: (aid: string, wid: string) => `/api/v1/assets/${aid}/raw?workspaceId=${encodeURIComponent(wid)}`,
  assetThumbUrl: (aid: string, wid: string, w = 320) =>
    `/api/v1/assets/${aid}/thumb?workspaceId=${encodeURIComponent(wid)}&w=${w}`,
  uploadAsset: (wid: string, file: File | Blob, name = 'upload') => {
    const fd = new FormData();
    fd.append('file', file, name);
    return request<Asset>(`/api/v1/workspaces/${wid}/assets`, { formData: fd });
  },

  listProviders: (wid: string) => request<{ items: Provider[] }>(`/api/v1/workspaces/${wid}/providers`),
  createProvider: (wid: string, payload: ProviderInput) =>
    request<Provider>(`/api/v1/workspaces/${wid}/providers`, { body: payload }),
  createCredential: (wid: string, pid: string, payload: CredentialInput) =>
    request<Credential>(`/api/v1/workspaces/${wid}/providers/${pid}/credentials`, { body: payload }),
  testProvider: (wid: string, pid: string) =>
    request<ProbeResult>(`/api/v1/workspaces/${wid}/providers/${pid}/test`, { method: 'POST' }),
  listModels: (wid: string, capability = '') =>
    request<{ items: Model[] }>(`/api/v1/workspaces/${wid}/models?capability=${encodeURIComponent(capability)}`),

  listPromptSources: (wid: string) =>
    request<{ items: PromptSource[] }>(`/api/v1/workspaces/${wid}/prompt-sources`),
  createPromptSource: (wid: string, payload: PromptSourceInput) =>
    request<PromptSource>(`/api/v1/workspaces/${wid}/prompt-sources`, { body: payload }),
  syncPromptSource: (wid: string, sid: string) =>
    request<SyncResult>(`/api/v1/workspaces/${wid}/prompt-sources/${sid}/sync`, { method: 'POST' }),
  searchPrompts: (wid: string, q: string, tags: string[] = [], limit = 20) =>
    request<{ items: Prompt[]; cursor: string }>(
      `/api/v1/prompts?workspaceId=${encodeURIComponent(wid)}&q=${encodeURIComponent(q)}&tags=${encodeURIComponent(tags.join(','))}&limit=${limit}`,
    ),

  listPlugins: (wid: string) => request<{ items: Plugin[] }>(`/api/v1/workspaces/${wid}/plugins`),
  installPlugin: (wid: string, payload: PluginInstallInput) =>
    request<Plugin>(`/api/v1/workspaces/${wid}/plugins`, { body: payload }),
  enablePlugin: (wid: string, key: string, enabled: boolean) =>
    request<Plugin>(`/api/v1/workspaces/${wid}/plugins/${key}/enable`, { body: { enabled, trusted: true } }),
  pluginBundleUrl: (key: string, version: string) => `/api/v1/plugins/${key}/${version}/bundle`,

  createAgentSession: (wid: string, canvasId: string, backend = 'http') =>
    request<AgentSessionDTO>('/api/v1/agent/sessions', { body: { workspaceId: wid, canvasId, backend } }),
  createAgentTurn: (sid: string, input: string) =>
    request<AgentTurnDTO>(`/api/v1/agent/sessions/${sid}/turns`, { body: { input } }),
  approveAgentTurn: (sid: string, tid: string, approve: boolean) =>
    request<AgentTurnDTO>(`/api/v1/agent/sessions/${sid}/turns/${tid}/approve`, { body: { approve } }),
  agentHistory: (sid: string) => request<AgentSessionDTO>(`/api/v1/agent/sessions/${sid}/history`),
  getAgentSession: (sid: string) => request<AgentSessionDTO>(`/api/v1/agent/sessions/${sid}`),
};

export interface AgentSessionDTO {
  id: string;
  workspaceId: string;
  canvasId: string;
  backend: string;
  threadId?: string;
  title: string;
  permission?: string;
  turns: AgentTurnDTO[];
  createdAt?: string;
}

export interface AgentTurnDTO {
  id: string;
  seq: number;
  status: string;
  input?: string;
  items: AgentItemDTO[];
  usage?: Record<string, number>;
  pending?: AgentPendingDTO;
  error?: { code: string; message: string };
  createdAt?: string;
}

export interface AgentPendingDTO {
  callId: string;
  tool: string;
  arguments: unknown;
  opCount: number;
  nodeIds?: string[];
  estCostMicros: number;
}

export interface AgentItemDTO {
  id: string;
  turnId: string;
  seq: number;
  kind: 'agent_message' | 'reasoning' | 'tool_call' | 'tool_result' | 'file_change' | 'error';
  payload: unknown;
  source: 'live' | 'snapshot';
}

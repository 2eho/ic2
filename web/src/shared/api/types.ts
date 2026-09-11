/** 前端侧 DTO 类型。真源为 contracts/openapi.yaml，本文件由 make gen 校验同步。 */
export interface MetaResponse {
  build: { version: string; commit: string; date: string; goVersion: string; mode: string };
  features: Record<string, boolean>;
  limits: Record<string, unknown>;
  time: string;
}

export interface User {
  id: string;
  email: string;
  name: string;
  avatar?: string;
}

export interface Workspace {
  id: string;
  name: string;
  slug: string;
  role: 'owner' | 'admin' | 'editor' | 'viewer';
  plan: string;
}

export interface Project {
  id: string;
  workspaceId: string;
  name: string;
  description?: string;
  coverUrl?: string;
  canvasCount: number;
  createdAt: string;
  updatedAt: string;
}

export interface Session {
  token: string;
  user: User;
  workspaces: Workspace[];
  expiresAt: string;
}

export interface CanvasMeta {
  id: string;
  projectId: string;
  name: string;
  version: number;
  nodes: number;
  updatedAt: string;
}

export interface AppendOpsResult {
  version: number;
  applied: number;
  warnings?: string[];
  rebased?: boolean;
  inverse?: unknown[];
  document?: import('@/features/canvas/kernel/types').CanvasDoc;
}

export interface Asset {
  id: string;
  workspaceId: string;
  kind: 'image' | 'video' | 'audio' | 'text' | 'json' | 'file';
  hash: string;
  size: number;
  mime: string;
  name?: string;
  meta?: Record<string, unknown>;
  origin: string;
  url?: string;
  createdAt: string;
}

export interface Usage {
  textTokensIn: number;
  textTokensOut: number;
  images: number;
  videoMillis: number;
  audioMillis: number;
  costMicros: number;
}

export type StepStatus =
  | 'pending' | 'ready' | 'running' | 'retrying' | 'succeeded' | 'failed' | 'skipped' | 'canceled';

export interface RunError {
  code: string;
  message: string;
  class?: string;
  details?: Record<string, unknown>;
}

export interface RunAttempt {
  index: number;
  providerId: string;
  modelId: string;
  requestId: string;
  status: string;
  httpStatus?: number;
  latencyMs: number;
  tokensIn?: number;
  tokensOut?: number;
  costMicros?: number;
  error?: RunError;
}

export interface RunStep {
  id: string;
  nodeId: string;
  kind: string;
  status: StepStatus;
  attempts: RunAttempt[];
  outputs?: string[];
  text?: string;
  error?: RunError;
  startedAt: string;
  finishedAt?: string;
}

export type RunStatus = 'pending' | 'running' | 'succeeded' | 'failed' | 'canceled' | 'partial';

export interface Run {
  id: string;
  workspaceId: string;
  canvasId?: string;
  trigger: string;
  status: RunStatus;
  targetNodes?: string[];
  steps: RunStep[];
  usage: Usage;
  error?: RunError;
  startedAt: string;
  finishedAt?: string;
}

export interface Provider {
  id: string;
  workspaceId: string;
  kind: string;
  name: string;
  capabilities: string[];
  baseUrl: string;
  authKind: string;
  enabled: boolean;
}

export interface ProviderInput {
  id: string;
  name: string;
  kind: string;
  baseUrl: string;
  authKind: string;
  capabilities: string[];
}

export interface Credential {
  id: string;
  providerId: string;
  name: string;
  masked: string;
  priority: number;
  enabled: boolean;
  createdAt: string;
}

export interface CredentialInput {
  name: string;
  secret: string;
  priority?: number;
}

export interface ProbeResult {
  ok: boolean;
  latencyMs: number;
  models?: string[];
  error?: { code: string; message: string };
}

export interface Model {
  id: string;
  providerId: string;
  displayName?: string;
  capabilities: string[];
  paramsSchema?: Record<string, unknown>;
  healthy: boolean;
}

export interface PromptSource {
  id: string;
  name: string;
  url: string;
  format: string;
  refreshInterval: string;
  enabled: boolean;
  status: string;
  count: number;
  lastSyncedAt?: string;
}

export interface PromptSourceInput {
  name: string;
  url: string;
  format?: string;
  refreshInterval?: string;
  enabled?: boolean;
}

export interface SyncResult {
  sourceId: string;
  added: number;
  updated: number;
  removed: number;
  total: number;
  status: string;
  error?: { code: string; message: string };
}

export interface Prompt {
  id: string;
  sourceId: string;
  externalId: string;
  title: string;
  tags: string[];
  content: string;
  variables?: string[];
  coverUrl?: string;
  hash: string;
}

export interface PluginNode {
  type: string;
  title: string;
  defaultSize?: { width: number; height: number };
  minimapColor?: string;
  configSchema?: Record<string, unknown>;
  configVersion?: number;
  ports: { inputs: unknown[]; outputs: unknown[] };
}

export interface Plugin {
  key: string;
  name: string;
  version: string;
  apiVersion: string;
  author?: string;
  homepage?: string;
  permissions: string[];
  nodes: PluginNode[];
  enabled: boolean;
  builtin: boolean;
  signed: boolean;
}

export interface PluginInstallInput {
  manifestUrl?: string;
  manifest?: Record<string, unknown>;
  trusted: boolean;
}

export interface GeneratePayload {
  capability: string;
  providerId?: string;
  credentialId?: string;
  model?: string;
  prompt: string;
  params?: Record<string, unknown>;
  outputCount?: number;
}

export interface ImportPayload {
  sourceProjectId: string;
  contentHash: string;
  name: string;
  nodes: unknown[];
  edges: unknown[];
  viewport?: { x: number; y: number; k: number };
  settings?: Record<string, unknown>;
}

export interface ImportResult {
  canvasId: string;
  version: number;
  nodes: number;
  edges: number;
  warning?: string;
}

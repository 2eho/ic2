/** IC 插件 SDK 类型定义（10.9）。与运行时 src/index.js 一一对应。 */

export type HostMethod =
  | "node.get"
  | "node.patch"
  | "node.resize"
  | "node.emit"
  | "graph.upstream"
  | "graph.downstream"
  | "graph.query"
  | "asset.getUrl"
  | "asset.upload"
  | "ai.generate"
  | "storage.get"
  | "storage.set"
  | "storage.remove"
  | "host.toast";

export type Permission =
  | "node.read"
  | "node.write"
  | "asset.read"
  | "asset.write"
  | "ai.generate"
  | "storage"
  | "network"
  | "graph.query";

export interface PluginNodeSnapshot {
  id: string;
  type: string;
  title: string;
  rect: { x: number; y: number; w: number; h: number };
  config: Record<string, unknown>;
  schemaVersion: number;
}

export interface PluginPort {
  id: string;
  name: string;
  kind: "text" | "image" | "video" | "audio" | "file" | "json";
  multiple?: boolean;
  required?: boolean;
  order?: number;
}

export interface PluginNodeDef {
  type: string;
  title: string;
  defaultSize?: { width: number; height: number };
  minimapColor?: string;
  configSchema?: Record<string, unknown>;
  configVersion?: number;
  interactive?: boolean;
  ports: { inputs: PluginPort[]; outputs: PluginPort[] };
}

export interface PluginManifest {
  key: string;
  name: string;
  version: string;
  apiVersion: string;
  author?: string;
  homepage?: string;
  entry: string;
  style?: string;
  permissions: Permission[];
  network?: string[];
  nodes: PluginNodeDef[];
  integrity?: { sha256: string };
  signature?: string;
}

export interface PluginSpec {
  manifest: PluginManifest;
  render: (node: PluginNodeSnapshot, api: HostApi) => unknown;
}

export interface HostApi {
  getNode(): Promise<PluginNodeSnapshot>;
  patch(config: Record<string, unknown>): Promise<void>;
  resize(width: number, height: number): Promise<void>;
  emit(portId: string, resource: unknown): Promise<void>;
  query(params: Record<string, unknown>): Promise<unknown>;
  assetUrl(assetId: string): Promise<string>;
  upload(blob: Blob, name: string): Promise<{ id: string }>;
  generate(capability: string, params: Record<string, unknown>): Promise<unknown>;
  storage: {
    get(key: string): Promise<unknown>;
    set(key: string, value: unknown): Promise<void>;
    remove(key: string): Promise<void>;
  };
  toast(message: string): Promise<void>;
}

export declare const HOST_METHODS: string[];
export declare const PERMISSIONS: Permission[];
export declare const API_VERSION: string;

export declare function definePlugin(spec: PluginSpec): PluginSpec;
export declare function validateManifest(manifest: PluginManifest): true;
export declare function host(): HostApi;
export declare function onInit(
  fn: (node: PluginNodeSnapshot, theme: "light" | "dark") => void,
): void;
export declare function requestResize(width: number, height: number): void;

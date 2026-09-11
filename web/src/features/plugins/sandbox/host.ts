import {
  buildSandboxDocument,
  hasPermission,
  parsePluginMessage,
  requiredPermission,
  sandboxAttrs,
  type HostToPlugin,
  type HostMethod,
  type PluginNodeSnapshot,
} from './protocol';

export interface PluginHostOptions {
  pluginKey: string;
  version: string;
  permissions: string[];
  allowedHosts: string[];
  bundle: string;
  node: PluginNodeSnapshot;
  theme: 'light' | 'dark';
  /** 宿主能力实现。只暴露需要的部分。 */
  handlers: HostHandlers;
  onSizeChange?: (w: number, h: number) => void;
  onLog?: (level: 'info' | 'warn' | 'error', message: string) => void;
}

/**
 * 宿主能力代理。**只实现被声明权限允许的能力**；
 * 每次调用都会先校验权限，未声明即拒绝并记审计日志。
 */
export interface HostHandlers {
  nodeGet?: () => PluginNodeSnapshot | null;
  nodePatch?: (config: Record<string, unknown>) => void;
  nodeResize?: (w: number, h: number) => void;
  nodeEmit?: (portId: string, resource: unknown) => void;
  graphUpstream?: () => unknown;
  graphDownstream?: () => unknown;
  graphQuery?: (params: Record<string, unknown>) => unknown;
  assetGetUrl?: (assetId: string) => Promise<string>;
  assetUpload?: (blob: Blob, name: string) => Promise<{ id: string }>;
  aiGenerate?: (req: Record<string, unknown>) => Promise<unknown>;
  storageGet?: (key: string) => Promise<unknown>;
  storageSet?: (key: string, value: unknown) => Promise<void>;
  storageRemove?: (key: string) => Promise<void>;
  toast?: (message: string) => void;
  audit?: (entry: { plugin: string; method: string; allowed: boolean; reason?: string }) => void;
}

/**
 * 创建 iframe 沙箱宿主。
 *
 * 设计要点：
 * - iframe 无 allow-same-origin → 插件处于 null origin，无法触碰宿主；
 * - 每帧做方法白名单与权限校验；
 * - 通信失败（插件崩溃）不会影响主页面：仅停用该节点并提示。
 */
export function createPluginHost(opts: PluginHostOptions): { render: () => HTMLIFrameElement; dispose: () => void } {
  const iframe = document.createElement('iframe');
  iframe.setAttribute('sandbox', sandboxAttrs());
  iframe.setAttribute('title', opts.pluginKey);
  iframe.style.border = '0';
  iframe.style.width = '100%';
  iframe.style.height = '100%';
  iframe.setAttribute('referrerpolicy', 'no-referrer');
  iframe.srcdoc = buildSandboxDocument(opts.bundle, opts.allowedHosts);

  const onMessage = async (ev: MessageEvent) => {
    // 只接受来自本 iframe 的消息（contentWindow 身份比对，而不是 origin——
    // sandbox 下 origin 为 "null"，无法用于判别）。
    if (ev.source !== iframe.contentWindow) return;
    const msg = parsePluginMessage(ev.data);
    if (!msg) {
      opts.onLog?.('warn', '丢弃非法协议消息');
      return;
    }
    switch (msg.type) {
      case 'plugin:ready':
        post({ type: 'plugin:init', node: opts.node, theme: opts.theme, width: opts.node.rect.w, height: opts.node.rect.h });
        break;
      case 'plugin:resize':
        opts.onSizeChange?.(msg.width, msg.height);
        break;
      case 'plugin:log':
        opts.onLog?.(msg.level, msg.message);
        break;
      case 'host:event':
        break;
      case 'host:call': {
        const id = msg.id;
        const method = msg.method as HostMethod;
        const need = requiredPermission(method);
        const arg = typeof msg.params?.capability === 'string' ? msg.params.capability : undefined;
        if (!hasPermission(opts.permissions, need, arg)) {
          opts.handlers.audit?.({ plugin: opts.pluginKey, method, allowed: false, reason: `missing ${need}` });
          post({
            type: 'plugin:result', id, ok: false,
            error: { code: 'plugin_permission_denied', message: `method ${method} requires ${need}` },
          });
          return;
        }
        opts.handlers.audit?.({ plugin: opts.pluginKey, method, allowed: true });
        try {
          const value = await invoke(opts.handlers, method, msg.params ?? {});
          post({ type: 'plugin:result', id, ok: true, value });
        } catch (e) {
          post({ type: 'plugin:result', id, ok: false, error: { code: 'host_error', message: String(e) } });
        }
        break;
      }
      default:
        break;
    }
  };

  function post(msg: HostToPlugin) {
    iframe.contentWindow?.postMessage(msg, '*');
  }

  window.addEventListener('message', onMessage);

  return {
    render: () => iframe,
    dispose: () => {
      window.removeEventListener('message', onMessage);
      post({ type: 'plugin:disable', reason: 'host disposed' });
      iframe.remove();
    },
  };
}

async function invoke(h: HostHandlers, method: HostMethod, params: Record<string, unknown>): Promise<unknown> {
  switch (method) {
    case 'node.get':
      return h.nodeGet?.() ?? null;
    case 'node.patch':
      h.nodePatch?.((params.config as Record<string, unknown>) ?? {});
      return null;
    case 'node.resize':
      h.nodeResize?.(Number(params.width) || 0, Number(params.height) || 0);
      return null;
    case 'node.emit':
      h.nodeEmit?.(String(params.portId ?? ''), params.resource);
      return null;
    case 'graph.upstream':
      return h.graphUpstream?.() ?? [];
    case 'graph.downstream':
      return h.graphDownstream?.() ?? [];
    case 'graph.query':
      return h.graphQuery?.(params) ?? null;
    case 'asset.getUrl':
      return h.assetGetUrl?.(String(params.assetId ?? '')) ?? '';
    case 'asset.upload':
      return h.assetUpload?.(params.blob as Blob, String(params.name ?? 'upload')) ?? { id: '' };
    case 'ai.generate':
      return h.aiGenerate?.(params) ?? null;
    case 'storage.get':
      return h.storageGet?.(String(params.key ?? '')) ?? null;
    case 'storage.set':
      h.storageSet?.(String(params.key ?? ''), params.value);
      return null;
    case 'storage.remove':
      h.storageRemove?.(String(params.key ?? ''));
      return null;
    case 'host.toast':
      h.toast?.(String(params.message ?? ''));
      return null;
    default:
      throw new Error(`unknown method ${method}`);
  }
}

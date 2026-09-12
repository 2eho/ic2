/**
 * 极简 JSX runtime（10.9）。
 *
 * 为什么自研而不是引 React：插件 iframe 是 `sandbox="allow-scripts"`、
 * 无 allow-same-origin，因此**不能**从 CDN 加载，也不能与宿主共享 React 实例
 * （跨 origin 的 window 对象不互通）。把 React 打进每个插件的 bundle
 * 会让「一个 markdown 渲染插件」变成 40KB+。
 *
 * 所以插件 UI 用一层极薄的 createElement：它产出的是普通对象，
 * 由插件自己的 render 决定怎么挂到 iframe 的 document 上。
 * 这与宿主用 React 渲染画布并不冲突——它们本来就运行在两个世界里。
 */

export function jsx(type, props, ...children) {
  return createElement(type, props, ...children);
}

export const jsxs = jsx;

export function Fragment(props) {
  return props?.children ?? null;
}

/** 创建元素描述（纯数据，不含任何 DOM 操作）。 */
export function createElement(type, props, ...children) {
  const flat = children.flat(Infinity).filter((c) => c != null && c !== false);
  const out = { type, props: { ...(props ?? {}) } };
  if (flat.length === 1) out.children = flat[0];
  else if (flat.length > 1) out.children = flat;
  return out;
}

/**
 * 把元素描述渲染成真实 DOM。
 *
 * 安全要点：**只用 textContent 写文本**，属性名以 `on` 开头的当作事件绑定。
 * 不支持 `innerHTML` / `dangerouslySetInnerHTML` —— 插件本身就是不可信代码，
 * 给它一个「把字符串变成 HTML」的入口，等于给每个插件一个 XSS。
 * 需要富文本的插件（例如 markdown）必须自己走白名单清洗。
 */
export function renderToDOM(vnode, doc = document) {
  if (vnode == null || vnode === false) return doc.createTextNode("");
  if (typeof vnode === "string" || typeof vnode === "number") {
    return doc.createTextNode(String(vnode));
  }
  if (Array.isArray(vnode)) {
    const frag = doc.createDocumentFragment();
    for (const child of vnode) frag.appendChild(renderToDOM(child, doc));
    return frag;
  }
  const el = doc.createElement(vnode.type);
  for (const [key, value] of Object.entries(vnode.props ?? {})) {
    if (value == null || value === false) continue;
    if (key === "children") continue;
    if (key === "style" && typeof value === "object") {
      for (const [k, v] of Object.entries(value)) {
        // style 用 setProperty 而不是字符串拼接：拼接会让值里的 `;`
        // 变成新的声明（一个很常见的属性注入面）。
        el.style.setProperty(
          k.replace(/[A-Z]/g, (m) => "-" + m.toLowerCase()),
          String(v),
        );
      }
      continue;
    }
    if (key.startsWith("on") && typeof value === "function") {
      el.addEventListener(key.slice(2).toLowerCase(), value);
      continue;
    }
    if (key === "class" || key === "className") {
      el.setAttribute("class", String(value));
      continue;
    }
    // 只允许白名单属性 + data-/aria-：`href="javascript:"` 这类值
    // 只要属性名被限制成安全集合，就没有落点。
    if (SAFE_ATTRS.has(key) || key.startsWith("data-") || key.startsWith("aria-")) {
      el.setAttribute(key, String(value));
    }
  }
  const children = vnode.children ?? vnode.props?.children;
  if (children != null) el.appendChild(renderToDOM(children, doc));
  return el;
}

const SAFE_ATTRS = new Set([
  "id",
  "title",
  "alt",
  "src",
  "width",
  "height",
  "value",
  "placeholder",
  "disabled",
  "checked",
  "type",
  "role",
  "tabindex",
  "colspan",
  "rowspan",
]);

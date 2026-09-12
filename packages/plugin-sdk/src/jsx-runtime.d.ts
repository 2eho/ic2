/** JSX runtime 类型（tsconfig 需配 "jsx": "react-jsx", "jsxImportSource": "@ic/plugin-sdk"）。 */

export declare function jsx(
  type: string | ((props: Record<string, unknown>) => unknown),
  props: Record<string, unknown> | null,
  key?: string,
): unknown;
export declare const jsxs: typeof jsx;
export declare function Fragment(props: { children?: unknown }): unknown;
export declare function createElement(
  type: string,
  props?: Record<string, unknown> | null,
  ...children: unknown[]
): unknown;
export declare function renderToDOM(vnode: unknown, doc?: Document): Node;

declare global {
  namespace JSX {
    interface IntrinsicElements {
      [elem: string]: Record<string, unknown>;
    }
  }
}

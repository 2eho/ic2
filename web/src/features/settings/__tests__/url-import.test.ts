import { describe, expect, it, vi } from "vitest";
import {
  consumeCredentialParams,
  parseCredentialParams,
  stripCredentialParams,
} from "../url-import";

/**
 * URL 参数导入凭据。
 *
 * 核心断言是**清理**：把 `?apiKey=sk-xxx` 留在地址栏里，它会进浏览器历史、
 * 进 referer、被截图发出去——一次泄露就是永久的。
 * 因此「导入后地址栏必须干净」是这条功能的验收标准，而不是附赠行为。
 */
describe("解析凭据参数", () => {
  it("支持驼峰与下划线两种写法", () => {
    expect(parseCredentialParams("?apiKey=sk-1&baseUrl=https://a.com")).toEqual(
      {
        apiKey: "sk-1",
        baseUrl: "https://a.com",
      },
    );
    expect(
      parseCredentialParams("?api_key=sk-2&base_url=https://b.com"),
    ).toEqual({
      apiKey: "sk-2",
      baseUrl: "https://b.com",
    });
  });

  it("没有凭据参数时返回 null（正常访问不该被当成一键配置）", () => {
    expect(parseCredentialParams("")).toBeNull();
    expect(parseCredentialParams("?foo=bar")).toBeNull();
  });

  it("解析可选的 name 与 model", () => {
    const parsed = parseCredentialParams(
      "?apiKey=k&baseUrl=https://a.com&name=我的渠道&model=gpt-4o",
    );
    expect(parsed?.name).toBe("我的渠道");
    expect(parsed?.model).toBe("gpt-4o");
  });

  it("URL 编码的值能正确解码", () => {
    const parsed = parseCredentialParams(
      "?apiKey=sk%2Fwith%2Fslash&baseUrl=https%3A%2F%2Fa.com",
    );
    expect(parsed?.apiKey).toBe("sk/with/slash");
    expect(parsed?.baseUrl).toBe("https://a.com");
  });
});

describe("清除地址栏参数", () => {
  function fakeWin(href: string) {
    const replaceState = vi.fn();
    return {
      win: {
        location: { href, search: new URL(href).search },
        history: { replaceState },
      } as unknown as Window,
      replaceState,
    };
  }

  it("移除全部凭据参数，保留其他参数", () => {
    const { win, replaceState } = fakeWin(
      "https://ic.dev/settings?apiKey=sk-secret&tab=models&baseUrl=https://a.com",
    );
    expect(stripCredentialParams(win)).toBe(true);
    expect(replaceState).toHaveBeenCalledTimes(1);
    const target = replaceState.mock.calls[0]![2] as string;
    expect(target).not.toContain("sk-secret");
    expect(target).not.toContain("baseUrl");
    expect(target).toContain("tab=models");
  });

  it("没有凭据参数时不改地址（避免无意义的历史项）", () => {
    const { win, replaceState } = fakeWin("https://ic.dev/settings?tab=models");
    expect(stripCredentialParams(win)).toBe(false);
    expect(replaceState).not.toHaveBeenCalled();
  });

  it("覆盖 token / access_token 等别名", () => {
    const { win, replaceState } = fakeWin(
      "https://ic.dev/?token=abc&access_token=def",
    );
    expect(stripCredentialParams(win)).toBe(true);
    const target = replaceState.mock.calls[0]![2] as string;
    expect(target).not.toContain("abc");
    expect(target).not.toContain("def");
  });

  it("使用 replaceState 而不是 pushState（后退不能回到带密钥的 URL）", () => {
    const { win, replaceState } = fakeWin("https://ic.dev/?apiKey=sk");
    stripCredentialParams(win);
    expect(replaceState).toHaveBeenCalled();
  });
});

describe("读取并清除", () => {
  it("一次性取出参数并把地址栏抹干净", () => {
    const replaceState = vi.fn();
    const win = {
      location: {
        href: "https://ic.dev/settings?apiKey=sk-x&baseUrl=https://a.com",
        search: "?apiKey=sk-x&baseUrl=https://a.com",
      },
      history: { replaceState },
    } as unknown as Window;

    expect(consumeCredentialParams(win)?.apiKey).toBe("sk-x");
    expect(replaceState).toHaveBeenCalledTimes(1);
  });

  it("无参数时不做任何修改", () => {
    const replaceState = vi.fn();
    const win = {
      location: { href: "https://ic.dev/settings", search: "" },
      history: { replaceState },
    } as unknown as Window;
    expect(consumeCredentialParams(win)).toBeNull();
    expect(replaceState).not.toHaveBeenCalled();
  });
});

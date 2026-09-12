import { useEffect, useState } from "react";

/**
 * 大图预览（1.1）。
 *
 * 首页的提示词封面墙、提示词库与画布共用同一个查看器：原项目首页与画布各写了
 * 一份，结果是「ESC 能关掉其中一个」这类不一致。这里只有一份实现。
 *
 * 一些看起来是细节、但决定「能不能用」的行为：
 *   - 点遮罩与按 ESC 都关闭（只支持其中一个会让人觉得卡住了）；
 *   - 打开期间锁滚动（否则滚轮会带着背景一起动，像页面错位）；
 *   - 图片加载失败时给出「在新标签页打开」的兜底链接，而不是一个空白弹层
 *     （封面来自第三方仓库，挂掉是常态，不能让用户以为应用坏了）。
 */
export function ImageLightbox({
  src,
  alt,
  onClose,
}: {
  src: string;
  alt?: string;
  onClose: () => void;
}) {
  const [failed, setFailed] = useState(false);

  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (e.key === "Escape") onClose();
    };
    window.addEventListener("keydown", onKey);
    const prev = document.body.style.overflow;
    document.body.style.overflow = "hidden";
    return () => {
      window.removeEventListener("keydown", onKey);
      document.body.style.overflow = prev;
    };
  }, [onClose]);

  return (
    <div
      data-testid="image-lightbox"
      onClick={onClose}
      style={{
        position: "fixed",
        inset: 0,
        background: "rgba(0,0,0,.72)",
        display: "grid",
        placeItems: "center",
        zIndex: 2000,
        cursor: "zoom-out",
      }}
    >
      {failed ? (
        <div
          onClick={(e) => e.stopPropagation()}
          className="ic-card"
          style={{ padding: 20, cursor: "default", textAlign: "center" }}
        >
          <p style={{ marginTop: 0 }}>图片加载失败</p>
          <a href={src} target="_blank" rel="noreferrer noopener">
            在新标签页打开原图
          </a>
        </div>
      ) : (
        <img
          src={src}
          alt={alt ?? ""}
          onError={() => setFailed(true)}
          onClick={(e) => e.stopPropagation()}
          style={{
            maxWidth: "92vw",
            maxHeight: "88vh",
            borderRadius: 8,
            boxShadow: "0 12px 48px rgba(0,0,0,.5)",
            cursor: "default",
          }}
        />
      )}
    </div>
  );
}

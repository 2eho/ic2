/**
 * 按模型名关键词猜测能力（对齐 docs/design/10 §8.3）。
 *
 * 与后端的 `GuessCapabilities` 保持同一套关键词——两边判定不一致时，
 * 会出现「前端显示是生图模型、后端不给它生图凭据」这种混乱。
 * 因此这里的关键词表与 `internal/provider/service.go` 的
 * `GuessCapabilities` 一一对应，并由 i18n 一致性测试同类的思路维护。
 *
 * 猜测只是默认值：用户可以覆盖。猜错不该导致「不能选」，只该导致「默认勾选错」。
 */
export function guessCapabilities(modelId: string): string[] {
  const m = (modelId ?? "").toLowerCase();
  if (m.includes("upscale") || m.includes("superres")) return ["image.upscale"];
  if (m.includes("embed") || m.includes("moderation")) return ["text.tools"];
  if (m.includes("tts") || m.includes("audio") || m.includes("speech"))
    return ["audio.generate"];
  if (
    m.includes("video") ||
    m.includes("veo") ||
    m.includes("sora") ||
    m.includes("kling")
  ) {
    return ["video.generate"];
  }
  if (
    m.includes("dall") ||
    m.includes("image") ||
    m.includes("flux") ||
    m.includes("sd") ||
    m.includes("imagen") ||
    m.includes("gpt-image")
  ) {
    return ["image.generate", "image.edit"];
  }
  return ["text.generate", "model.list"];
}

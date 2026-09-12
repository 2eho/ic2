package asset

import (
	"bytes"
	"context"
	"image"
	"image/color"
	"image/png"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/context-flow/ic/internal/platform"
	"github.com/context-flow/ic/migrations"
)

func newTestService(t *testing.T) *Service {
	t.Helper()
	cfg := platform.Defaults()
	cfg.DBDriver = "sqlite"
	cfg.DBDSN = "file::memory:?cache=shared"
	cfg.AllowInsecureDevKey = true
	db, err := platform.OpenDB(cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := db.Migrate(context.Background(), migrations.FS, "."); err != nil {
		t.Fatal(err)
	}
	return New(db.DB, NewMemoryBlobStore(), platform.SystemClock(), platform.DefaultIDGen())
}

func toInt(v any) int {
	switch n := v.(type) {
	case int:
		return n
	case int64:
		return int(n)
	case float64:
		return int(n)
	}
	return -1
}

func pngBytes(t *testing.T, w, h int) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.Set(x, y, color.RGBA{R: uint8(x % 256), G: uint8(y % 256), B: 128, A: 255})
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// 内容寻址：同一张图重复上传只占一份存储。
func TestUploadDeduplicatesByContent(t *testing.T) {
	svc := newTestService(t)
	ctx := context.Background()
	data := pngBytes(t, 64, 64)

	a, err := svc.Upload(ctx, "ws_1", "a.png", "image/png", bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatal(err)
	}
	b, err := svc.Upload(ctx, "ws_1", "b.png", "image/png", bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatal(err)
	}
	if a.ID != b.ID {
		t.Fatalf("相同内容应复用同一资产记录: %s != %s", a.ID, b.ID)
	}
	if mem, ok := svc.blobs.(*MemoryBlobStore); ok && mem.Size() != 1 {
		t.Fatalf("Blob 应只存一份，实际 %d", mem.Size())
	}
	if toInt(a.Meta["width"]) != 64 || toInt(a.Meta["height"]) != 64 {
		t.Fatalf("元数据提取失败: %+v", a.Meta)
	}
}

// ATK-05：上传文件名为 ../../x.png，Blob 路径必须是 hash，无穿越。
func TestATK05UploadSanitizesFileName(t *testing.T) {
	svc := newTestService(t)
	ctx := context.Background()
	data := pngBytes(t, 8, 8)
	d, err := svc.Upload(ctx, "ws_1", "../../../etc/passwd.png", "image/png", bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatal(err)
	}
	if strings.ContainsAny(d.Name, "/\\") {
		t.Fatalf("文件名未清洗: %q", d.Name)
	}
	if !validHash(d.Hash) {
		t.Fatalf("hash 非法: %q", d.Hash)
	}
}

// 跨工作区不可读（IDOR）
func TestCrossWorkspaceAssetIsolation(t *testing.T) {
	svc := newTestService(t)
	ctx := context.Background()
	data := pngBytes(t, 8, 8)
	d, err := svc.Upload(ctx, "ws_1", "a.png", "image/png", bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Get(ctx, "ws_2", d.ID); err == nil {
		t.Fatal("跨工作区读取应失败")
	}
	if _, err := svc.Get(ctx, "ws_1", d.ID); err != nil {
		t.Fatalf("本工作区读取失败: %v", err)
	}
}

// ATK-16：GC 运行期间被重新引用的资产不得被删除。
func TestATK16GCKeepsReferencedAsset(t *testing.T) {
	svc := newTestService(t)
	ctx := context.Background()
	data := pngBytes(t, 8, 8)
	d, err := svc.Upload(ctx, "ws_1", "a.png", "image/png", bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.Delete(ctx, "ws_1", d.ID); err != nil {
		t.Fatal(err)
	}
	// 冷静期内不回收
	res, err := svc.GC(ctx, GCOptions{GracePeriod: 24 * 1e9 * 3600})
	if err != nil {
		t.Fatal(err)
	}
	if res.Deleted != 0 {
		t.Fatalf("冷静期内不应删除: %+v", res)
	}
	// 重新建立引用后，即使过冷静期也不回收。
	// 注意语义：有引用的资产在候选筛选阶段就被排除，因此既不算 Deleted 也不算 Skipped。
	if err := svc.AddRef(ctx, d.ID, "canvas", "cv_1"); err != nil {
		t.Fatal(err)
	}
	res, err = svc.GC(ctx, GCOptions{GracePeriod: 0})
	if err != nil {
		t.Fatal(err)
	}
	if res.Deleted != 0 {
		t.Fatalf("有引用的资产不得被回收: %+v", res)
	}
	if _, err := svc.Get(ctx, "ws_1", d.ID); err != nil {
		t.Fatal("资产记录应仍然存在")
	}
}

func TestGCDeletesOrphan(t *testing.T) {
	svc := newTestService(t)
	ctx := context.Background()
	data := pngBytes(t, 8, 8)
	d, _ := svc.Upload(ctx, "ws_1", "a.png", "image/png", bytes.NewReader(data), int64(len(data)))
	if err := svc.Delete(ctx, "ws_1", d.ID); err != nil {
		t.Fatal(err)
	}
	res, err := svc.GC(ctx, GCOptions{GracePeriod: 0})
	if err != nil {
		t.Fatal(err)
	}
	if res.Deleted != 1 {
		t.Fatalf("孤儿资产应被回收: %+v", res)
	}
	if _, err := svc.Get(ctx, "ws_1", d.ID); err == nil {
		t.Fatal("记录应已被删除")
	}
}

func TestGCRespectsGracePeriod(t *testing.T) {
	svc := newTestService(t)
	ctx := context.Background()
	data := pngBytes(t, 8, 8)
	d, _ := svc.Upload(ctx, "ws_1", "a.png", "image/png", bytes.NewReader(data), int64(len(data)))
	_ = svc.Delete(ctx, "ws_1", d.ID)
	res, _ := svc.GC(ctx, GCOptions{GracePeriod: 365 * 24 * 1e9 * 1})
	if res.Deleted != 0 {
		t.Fatal("未过冷静期不应删除")
	}
}

func TestThumbForImage(t *testing.T) {
	svc := newTestService(t)
	ctx := context.Background()
	data := pngBytes(t, 1024, 512)
	d, _ := svc.Upload(ctx, "ws_1", "big.png", "image/png", bytes.NewReader(data), int64(len(data)))
	out, mime, err := svc.Thumb(ctx, "ws_1", d.ID, 256)
	if err != nil {
		t.Fatal(err)
	}
	if mime != "image/png" || len(out) == 0 {
		t.Fatalf("缩略图生成失败 mime=%s len=%d", mime, len(out))
	}
	cfg, _, err := image.DecodeConfig(bytes.NewReader(out))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Width > ThumbMaxEdge || cfg.Height > ThumbMaxEdge {
		t.Fatalf("缩略图尺寸超限: %dx%d", cfg.Width, cfg.Height)
	}
}

func TestThumbRejectsNonImage(t *testing.T) {
	svc := newTestService(t)
	ctx := context.Background()
	d, _ := svc.Upload(ctx, "ws_1", "a.txt", "text/plain", strings.NewReader("hello"), 5)
	if _, _, err := svc.Thumb(ctx, "ws_1", d.ID, 64); err == nil {
		t.Fatal("非图片不应生成缩略图")
	}
}

func TestUploadRejectsOversize(t *testing.T) {
	svc := newTestService(t)
	ctx := context.Background()
	// 声明超过上限的大小
	_, err := svc.Upload(ctx, "ws_1", "a.png", "image/png", bytes.NewReader([]byte("x")), MaxUploadBytes+1)
	if err == nil {
		t.Fatal("超限上传应被拒绝")
	}
	if de := platform.AsDomainError(err); de.Code != platform.CodePayloadTooLarge {
		t.Fatalf("code=%s", de.Code)
	}
}

func TestListPaginationStable(t *testing.T) {
	svc := newTestService(t)
	ctx := context.Background()
	for i := 0; i < 5; i++ {
		data := pngBytes(t, 8+i, 8)
		if _, err := svc.Upload(ctx, "ws_1", "x.png", "image/png", bytes.NewReader(data), int64(len(data))); err != nil {
			t.Fatal(err)
		}
	}
	page1, cursor, err := svc.List(ctx, "ws_1", "", 2, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(page1) != 2 || cursor == "" {
		t.Fatalf("首页异常: len=%d cursor=%q", len(page1), cursor)
	}
	page2, _, err := svc.List(ctx, "ws_1", "", 2, cursor)
	if err != nil {
		t.Fatal(err)
	}
	for _, a := range page2 {
		for _, b := range page1 {
			if a.ID == b.ID {
				t.Fatal("分页出现重复项")
			}
		}
	}
}

func TestKindOfAndMime(t *testing.T) {
	cases := []struct{ mime, name, want string }{
		{"image/png", "a.png", "image"},
		{"video/mp4", "a.mp4", "video"},
		{"audio/mpeg", "a.mp3", "audio"},
		{"text/plain", "a.txt", "text"},
		{"application/json", "a.json", "json"},
		{"application/octet-stream", "a.webp", "image"},
		{"application/octet-stream", "a.unknown", "file"},
	}
	for _, c := range cases {
		if got := KindOf(c.mime, c.name); got != c.want {
			t.Fatalf("KindOf(%q,%q)=%q 期望 %q", c.mime, c.name, got, c.want)
		}
	}
}

func TestNormalizeMimeSniffing(t *testing.T) {
	png := pngBytes(t, 4, 4)
	if got := normalizeMIME("", png, "a.png"); got != "image/png" {
		t.Fatalf("嗅探失败: %s", got)
	}
	if got := normalizeMIME("image/png; charset=binary", png, "a.png"); got != "image/png" {
		t.Fatalf("参数未剥离: %s", got)
	}
	// 魔数优先于声明：PNG 字节即使被声明成 svg 也必须是 png（ATK-19）
	if got := normalizeMIME("image/svg+xml", png, "a.svg"); got != "image/png" {
		t.Fatalf("声明覆盖了内容嗅探: %s", got)
	}
	// 无法判定内容时也不接受可执行类型声明（ATK-19）
	if got := normalizeMIME("text/html", []byte("plain words"), "a.txt"); got == "text/html" {
		t.Fatalf("无法判定内容时采信了可执行声明: %s", got)
	}
}

func TestDerivationLineage(t *testing.T) {
	svc := newTestService(t)
	ctx := context.Background()
	parent, _ := svc.Upload(ctx, "ws_1", "p.png", "image/png", bytes.NewReader(pngBytes(t, 32, 32)), 0)
	child, _ := svc.Upload(ctx, "ws_1", "c.png", "image/png", bytes.NewReader(pngBytes(t, 16, 16)), 0)
	if err := svc.Derive(ctx, child.ID, parent.ID, "crop"); err != nil {
		t.Fatal(err)
	}
	if err := svc.Derive(ctx, child.ID, parent.ID, "crop"); err != nil {
		t.Fatal("重复派生记录应幂等")
	}
}

func TestRefCountLifecycle(t *testing.T) {
	svc := newTestService(t)
	ctx := context.Background()
	d, _ := svc.Upload(ctx, "ws_1", "a.png", "image/png", bytes.NewReader(pngBytes(t, 8, 8)), 0)
	if n, _ := svc.RefCount(ctx, d.ID); n != 0 {
		t.Fatalf("初始引用应为 0，实际 %d", n)
	}
	if err := svc.AddRef(ctx, d.ID, "canvas", "cv_1"); err != nil {
		t.Fatal(err)
	}
	if err := svc.AddRef(ctx, d.ID, "canvas", "cv_1"); err != nil {
		t.Fatal("重复引用应幂等")
	}
	if n, _ := svc.RefCount(ctx, d.ID); n != 1 {
		t.Fatalf("引用数应为 1，实际 %d", n)
	}
	if err := svc.RemoveRef(ctx, d.ID, "canvas", "cv_1"); err != nil {
		t.Fatal(err)
	}
	if n, _ := svc.RefCount(ctx, d.ID); n != 0 {
		t.Fatalf("引用应已清除，实际 %d", n)
	}
}

func TestDownscaleKeepsAspect(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 1000, 500))
	out := Downscale(img, 100)
	if out.Bounds().Dx() != 100 || out.Bounds().Dy() != 50 {
		t.Fatalf("缩放比例错误: %v", out.Bounds())
	}
	small := image.NewRGBA(image.Rect(0, 0, 10, 10))
	if Downscale(small, 100) != image.Image(small) {
		t.Fatal("小图不应被放大")
	}
}

// 元数据更新：改名 + 加标签，且不得触碰内容寻址字段。
func TestUpdateAssetMeta(t *testing.T) {
	svc := newTestService(t)
	const wsID = "ws_1"
	ctx := context.Background()
	dto, err := svc.Upload(ctx, wsID, "raw.png", "image/png", strings.NewReader("hello"), 5)
	if err != nil {
		t.Fatal(err)
	}
	name := "《我的图》 第二版.png"
	updated, err := svc.UpdateMeta(ctx, wsID, dto.ID, AssetMetaPatch{
		Name: &name,
		Meta: map[string]any{"tags": "风景,夜景", "note": "备注"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if updated.Name != name {
		t.Fatalf("中文名被改写: %q", updated.Name)
	}
	if updated.Meta["tags"] != "风景,夜景" {
		t.Fatalf("meta 未写入: %+v", updated.Meta)
	}
	// 内容寻址字段必须不变
	if updated.Hash != dto.Hash || updated.Size != dto.Size || updated.MIME != dto.MIME {
		t.Fatalf("内容字段被修改: %+v vs %+v", updated, dto)
	}
}

// 元数据删除用显式列表（避免「传空对象就清空」的隐式语义）。
func TestUpdateAssetMetaDeleteKey(t *testing.T) {
	svc := newTestService(t)
	const wsID = "ws_1"
	ctx := context.Background()
	dto, _ := svc.Upload(ctx, wsID, "a.png", "image/png", strings.NewReader("x"), 1)
	if _, err := svc.UpdateMeta(ctx, wsID, dto.ID, AssetMetaPatch{Meta: map[string]any{"a": 1, "b": 2}}); err != nil {
		t.Fatal(err)
	}
	updated, err := svc.UpdateMeta(ctx, wsID, dto.ID, AssetMetaPatch{MetaDelete: []string{"a"}})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := updated.Meta["a"]; ok {
		t.Fatal("a 应被删除")
	}
	if updated.Meta["b"] != float64(2) && updated.Meta["b"] != 2 {
		t.Fatalf("b 不应受影响: %+v", updated.Meta)
	}
}

// 跨工作区更新必须 404（不泄露存在性）。
func TestUpdateAssetMetaCrossWorkspace(t *testing.T) {
	svc := newTestService(t)
	const wsID = "ws_1"
	ctx := context.Background()
	dto, _ := svc.Upload(ctx, wsID, "a.png", "image/png", strings.NewReader("x"), 1)
	name := "hacked"
	_, err := svc.UpdateMeta(ctx, "ws_other", dto.ID, AssetMetaPatch{Name: &name})
	if err == nil {
		t.Fatal("跨工作区更新应被拒绝")
	}
	if de := platform.AsDomainError(err); de.Code != platform.CodeNotFound {
		t.Fatalf("期望 not_found（不泄露存在性），实际 %s", de.Code)
	}
}

// 超长名字必须被截断（否则会在下游（Content-Disposition 等）出问题）。
func TestUpdateAssetMetaTruncatesName(t *testing.T) {
	svc := newTestService(t)
	const wsID = "ws_1"
	ctx := context.Background()
	dto, _ := svc.Upload(ctx, wsID, "a.png", "image/png", strings.NewReader("x"), 1)
	long := strings.Repeat("字", 500)
	updated, err := svc.UpdateMeta(ctx, wsID, dto.ID, AssetMetaPatch{Name: &long})
	if err != nil {
		t.Fatal(err)
	}
	if len(updated.Name) > MaxNameLen*3 {
		// 截断按字节做，中文 3 字节/字，因此用 3 倍上界
		t.Fatalf("名字未被截断: %d", len(updated.Name))
	}
	// 关键：不能切出半个 UTF-8 字符
	if !utf8.ValidString(updated.Name) {
		t.Fatal("截断产生了非法 UTF-8")
	}
}

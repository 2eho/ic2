package api

import (
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

// StaticHandler 托管前端产物；未命中的非 /api 路径回落到 index.html（SPA 路由）。
// 生产形态下由 Go 服务同源托管，因此没有 CORS 需求（见 docs/design/08 §2.1）。
func StaticHandler(fsys fs.FS, dir string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := strings.TrimPrefix(r.URL.Path, "/")
		if p == "" {
			p = "index.html"
		}
		// 目录穿越防护：清理后不允许逃出根
		clean := filepath.Clean("/" + p)
		if strings.Contains(clean, "..") {
			http.NotFound(w, r)
			return
		}
		target := strings.TrimPrefix(clean, "/")

		// 静态资源可直接长缓存（文件名带 hash），index.html 不缓存
		if target != "index.html" && fileExists(fsys, dir, target) {
			w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
			http.ServeFileFS(w, r, fsys, join(dir, target))
			return
		}
		index := join(dir, "index.html")
		if !fileExists(fsys, dir, "index.html") {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Cache-Control", "no-cache")
		http.ServeFileFS(w, r, fsys, index)
	})
}

func join(dir, name string) string {
	if dir == "" || dir == "." {
		return name
	}
	return dir + "/" + name
}

func fileExists(fsys fs.FS, dir, name string) bool {
	f, err := fsys.Open(join(dir, name))
	if err != nil {
		return false
	}
	defer f.Close()
	st, err := f.Stat()
	return err == nil && !st.IsDir()
}

// ResolveStaticDir 优先使用显式配置，其次探测常见构建输出目录。
func ResolveStaticDir(configured string) (fs.FS, string, bool) {
	candidates := []string{configured, "web/dist", "dist", "public"}
	for _, c := range candidates {
		if c == "" {
			continue
		}
		if st, err := os.Stat(c); err == nil && st.IsDir() {
			if _, err := os.Stat(filepath.Join(c, "index.html")); err == nil {
				return os.DirFS(c), ".", true
			}
		}
	}
	return nil, "", false
}

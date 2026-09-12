# IC rewrite / ic2 部署（简）

源码：`/home/box/ic2`（cnb.cool/context-flow.cloud/ic，Go `bin/ic-server` + Vite `web/dist/`）

| 服务 | Bind | 公网 |
|------|------|------|
| Web+API | `127.0.0.1:25692` | `https://ic2.zehh.de5.net/` |
| update-api | `127.0.0.1:25693` | `https://ic2.zehh.de5.net/update-api/…` |

> 与旧 basketikun IC（`/home/box/infinite-canvas-ic` → `ic.zehh.de5.net` :25662）隔离，互不干扰。

## 服务层

- `bin/ic-server`：Go 同源托管 `web/dist/` + `/api/*` + `/healthz` `/readyz`
- `bin/inject-update-widget.py`：注入 overlay 更新控件（左上、可拖、`ic2-update-pill-pos`）
- `overlay/`：update-api sidecar（:25693）
- 数据：`/home/box/ic2/data`（sqlite + assets）
- 密钥：`/home/box/ic2/.env`（`IC_SECRET_KEY`）；副本路径 `data/secret`（勿提交）

## 构建

```bash
export PATH="/home/box/.local/bin:/home/box/go/go1.25.0/bin:$PATH"
cd /home/box/ic2
(cd web && npm install --no-audit --no-fund && npm run build)
go build -o bin/ic-server ./cmd/ic-server
./start.sh
```

或一键：`./update.sh` / `./update.sh pull`

## 环境要点

- `IC_LISTEN=127.0.0.1:25692`（勿用 :8080，已被 cli-proxy-api 占用）
- `IC_SESSION_COOKIE_SECURE=true`（Cloudflare HTTPS）
- **开放访问已启用**（`IC_OPEN_ACCESS=true`）：无私钥登录/注册，持 URL 即可用（私有隧道场景；任何人拿到链接都能用）
- `IC_SSRF_ALLOW_PRIVATE` 默认 `false`；接本机 CPA/chatgpt2api 时再按需开启并配 `IC_SSRF_ALLOW_HOSTS`
- 无 dockerd：源码部署（Go 1.25 + npm）

## 已接线

- cloudflared ingress（本地 + persist + **remote**）
- DNS CNAME → tunnel `aede6421-…`
- secondary-origins / Phase4 / lock-local-ports（25692 25693）

勿动：infinite-canvas-ic、context-flow-canvas、studio-1、ai-video-editor、chatgpt2api、CPA、comfy、VLESS/sing-box。

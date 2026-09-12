# IC rewrite / ic2 运维（简）

公网：`https://ic2.zehh.de5.net/`  
本机：`http://127.0.0.1:25692/`（Go 托管 SPA + API）+ `http://127.0.0.1:25693/`（update-api）

源码：`/home/box/ic2`（优先 `cnb.cool/context-flow.cloud/ic`）。  
与旧 IC（infinite-canvas-ic / ic.zehh.de5.net）、studio、edit、canvas、comfy 隔离；勿改 VLESS / Clash / 其他隧道。

## 启停

```bash
/home/box/ic2/start.sh            # 幂等
/home/box/ic2/start.sh --restart  # 重启 web（并拉起 update-api）
/home/box/ic2/restart.sh          # 同上
/home/box/ic2/update.sh           # npm + go 重建 + 原子替换
/home/box/ic2/update.sh pull      # git pull 后再重建
```

左上角可拖动「一键更新」胶囊；位置存 `localStorage` 键 `ic2-update-pill-pos`。

## 自愈 / keepalive

- `check-secondary-origins.py`：`ic2` / `ic2-update`
- `start-all.sh` Phase4：25692 / 25693
- `lock-local-ports.sh`：25692 25693 仅回环

## 构建注意

- 前端：`web/` 下 **npm**（`package-lock` 随仓库）
- 后端：Go 1.25（`/home/box/go/go1.25.0`）；系统可能仍是 1.24
- 无 dockerd；密钥在 `/home/box/ic2/.env`（路径 only，勿 cat）

## 隧道注意

Edge 推送的 **remote** tunnel config 会覆盖本地 `config.yml` 的 ingress。改域名路由需同步 PUT remote configurations，并保持本地+persist 副本一致。勿 bounce sing-box/VLESS。

## 健康检查

```bash
curl -sS http://127.0.0.1:25692/healthz
curl -sS http://127.0.0.1:25692/readyz
curl -sS -o /dev/null -w '%{http_code}\n' http://127.0.0.1:25692/
curl -sS -o /dev/null -w '%{http_code}\n' https://ic2.zehh.de5.net/
```

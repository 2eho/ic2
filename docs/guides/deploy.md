# 部署指南

## 单机（推荐）

```bash
# 1. 生成主密钥（32 字节）
export IC_SECRET_KEY=$(openssl rand -base64 32)

# 2. 启动
docker compose -f deploy/docker-compose.yml up -d

# 3. 验证
curl -s localhost:8080/healthz
curl -s localhost:8080/api/v1/meta | head -c 400
```

打开 `http://localhost:8080` 即可注册并使用。前端静态产物由 Go 服务同源托管，
不存在 CORS，也不需要 nginx。

## 配置项

| 变量 | 默认 | 说明 |
| --- | --- | --- |
| `IC_LISTEN` | `:8080` | 监听地址 |
| `IC_MODE` | `standalone` | `standalone`（SQLite+FS）或 `cluster` |
| `IC_ROLE` | `all` | `api` / `worker` / `all` |
| `IC_DB_DRIVER` | `sqlite` | `sqlite` 或 `postgres` |
| `IC_DB_DSN` | `file:./data/ic.db` | 连接串 |
| `IC_BLOB_DRIVER` | `fs` | `fs` 或 `s3` |
| `IC_BLOB_FS_ROOT` | `./data/assets` | 资产目录 |
| `IC_SECRET_KEY` | 无（必填） | 凭据加密主密钥，32 字节 base64/hex |
| `IC_ALLOW_REGISTRATION` | `true` | 是否开放注册 |
| `IC_SSRF_ALLOW_PRIVATE` | `false` | 是否允许访问私网（接自建中转站时开启） |
| `IC_SSRF_ALLOW_HOSTS` | 空 | 额外允许的主机名（逗号分隔） |
| `IC_STATIC_DIR` | 自动探测 | 前端产物目录 |
| `IC_LOG_LEVEL` / `IC_LOG_FORMAT` | `info` / `text` | 日志 |

**启动期就会校验配置**：模式/驱动/密钥长度/必要项，非法立即退出并打印原因，
不会出现「跑起来才发现密钥不对」。

## 安全清单

- [ ] `IC_SECRET_KEY` 已改为随机值，且未提交到仓库
- [ ] 生产环境 `IC_SESSION_COOKIE_SECURE=true`（HTTPS）
- [ ] 不需要注册时把 `IC_ALLOW_REGISTRATION=false`
- [ ] 数据目录挂载到持久卷，并纳入备份
- [ ] 反向代理开启 SSE 透传（关闭响应缓冲）

## 反向代理注意

SSE 通道需要关闭缓冲，否则事件会被攒批：

```nginx
location /api/v1/canvases/ {
    proxy_pass http://127.0.0.1:8080;
    proxy_buffering off;
    proxy_cache off;
    proxy_read_timeout 3600s;
    proxy_set_header Connection "";
    proxy_http_version 1.1;
}
```

## 备份与恢复

```bash
# 备份：SQLite 用 .backup（避免拷到半个事务），资产目录直接打包
docker compose -f deploy/docker-compose.yml exec ic \
  sh -c 'sqlite3 /data/ic.db ".backup /data/backup.db"' || true
tar czf ic-backup-$(date +%F).tgz deploy/data/

# 恢复：停服务 → 换回数据 → 启动 → 跑一次 op 日志重放校验
```

`/healthz` 是存活探针，`/readyz` 会真正 ping 数据库（就绪探针用后者）。

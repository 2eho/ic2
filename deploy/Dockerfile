# IC 多阶段构建：Go 后端 + React 前端 → distroless 运行镜像。
# 设计依据 docs/design/08-infra.md §2.3。

# ---------- 前端 ----------
FROM node:22-alpine AS web
WORKDIR /src/web
COPY web/package.json web/pnpm-lock.yaml* web/pnpm-workspace.yaml* ./
RUN corepack enable && pnpm install --frozen-lockfile || pnpm install
COPY web/ ./
RUN pnpm build

# ---------- 后端 ----------
FROM golang:1.24-alpine AS backend
WORKDIR /src
RUN apk add --no-cache git
COPY go.mod go.sum ./
RUN go mod download
COPY . .
ARG VERSION=dev
ARG COMMIT=none
ARG DATE=unknown
RUN CGO_ENABLED=0 go build \
      -ldflags "-s -w -X github.com/context-flow/ic/internal/platform.Version=${VERSION} \
                -X github.com/context-flow/ic/internal/platform.Commit=${COMMIT} \
                -X github.com/context-flow/ic/internal/platform.Date=${DATE}" \
      -o /out/ic-server ./cmd/ic-server

# ---------- 运行 ----------
FROM gcr.io/distroless/static-debian12:nonroot
WORKDIR /app
COPY --from=backend /out/ic-server /app/ic-server
COPY --from=web /src/web/dist /app/web/dist
ENV IC_LISTEN=:8080 \
    IC_MODE=standalone \
    IC_DB_DRIVER=sqlite \
    IC_DB_DSN=file:/data/ic.db?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000) \
    IC_BLOB_DRIVER=fs \
    IC_BLOB_FS_ROOT=/data/assets \
    IC_STATIC_DIR=/app/web/dist \
    IC_LOG_FORMAT=json
VOLUME ["/data"]
EXPOSE 8080
USER nonroot:nonroot
ENTRYPOINT ["/app/ic-server"]

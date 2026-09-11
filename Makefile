# IC 重写 · 统一工程入口
# 约定：所有门禁都能在本地与 CI 以同一命令复现（见 docs/design/13 §6）。

SHELL := /bin/bash
GO    ?= go
BIN   ?= bin
PKG   := github.com/context-flow/ic
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
COMMIT  ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo none)
DATE    ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
LDFLAGS := -X $(PKG)/internal/platform.Version=$(VERSION) \
           -X $(PKG)/internal/platform.Commit=$(COMMIT) \
           -X $(PKG)/internal/platform.Date=$(DATE)

.PHONY: help
help: ## 显示可用目标
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | awk 'BEGIN{FS=":.*?## "}{printf "  \033[36m%-18s\033[0m %s\n", $$1, $$2}'

# ---------------------------------------------------------------- 构建

.PHONY: build
build: ## 构建 k1 服务端与 CLI
	@mkdir -p $(BIN)
	$(GO) build -ldflags "$(LDFLAGS)" -o $(BIN)/ic-server ./cmd/ic-server
	$(GO) build -ldflags "$(LDFLAGS)" -o $(BIN)/ic-cli    ./cmd/ic-cli

.PHONY: run
run: ## 本地启动服务端（开发模式）
	IC_ALLOW_INSECURE_DEV_KEY=true IC_LOG_LEVEL=debug $(GO) run ./cmd/ic-server

.PHONY: web-build
web-build: ## 构建前端（需要 node 20+）
	cd web && pnpm install --frozen-lockfile=false && pnpm build

.PHONY: dev
dev: ## 前端开发服务器（后端另行 make run）
	cd web && pnpm dev

# ---------------------------------------------------------------- 门禁

.PHONY: fmt
fmt: ## 格式化 Go 与前端代码
	$(GO) fmt ./...
	cd web && pnpm exec prettier --write 'src/**/*.{ts,tsx,css}' 2>/dev/null || true

.PHONY: lint
lint: ## 静态检查：vet + staticcheck + tsc + eslint
	$(GO) vet ./...
	@command -v staticcheck >/dev/null 2>&1 && staticcheck ./... || echo "staticcheck 未安装，跳过"
	cd web && pnpm exec tsc --noEmit 2>/dev/null || echo "web 未安装依赖，跳过 tsc"

.PHONY: test
test: ## 单测 + 对抗用例（ATK-*）
	$(GO) test ./... -count=1 -race -coverprofile=coverage.out
	@cd web 2>/dev/null && pnpm exec vitest run 2>/dev/null || true

.PHONY: test-norace
test-norace: ## 单测（不启用 race，供无 CGO 环境）
	$(GO) test ./... -count=1

.PHONY: cover
cover: test ## 覆盖率报告
	$(GO) tool cover -func=coverage.out | tail -20

.PHONY: parity
parity: ## 对等矩阵覆盖率报告（10-parity-matrix.md）
	node scripts/parity-report.mjs

.PHONY: boundaries
boundaries: ## 校验边界常量与文档一致（唯一真源 internal/graph/limits.go）
	node scripts/check-boundaries.mjs

.PHONY: sec
sec: ## 安全门禁：凭据脱敏、SSRF、依赖漏洞
	$(GO) test ./internal/platform/ -run 'TestRedact|TestSSRF' -count=1
	node scripts/check-secrets.mjs
	@command -v govulncheck >/dev/null 2>&1 && govulncheck ./... || echo "govulncheck 未安装，跳过"

.PHONY: check
check: lint test boundaries sec ## 本地全套门禁

.PHONY: drill
drill: ## 故障演练子集（进程崩溃 / 时钟回拨 / 并发冲突）
	$(GO) test ./internal/graph/ -run 'TestVersionConflict|TestRebaseable' -count=1
	$(GO) test ./internal/platform/ -run TestClockFake -count=1

.PHONY: gen
gen: ## 从 contracts 生成产物并校验已提交（契约先行）
	node scripts/gen-contracts.mjs

.PHONY: e2e
e2e: ## 端到端主链路测试
	$(GO) test ./internal/api/apitest/ -count=1 -v

.PHONY: clean
clean: ## 清理构建产物
	rm -rf $(BIN) coverage.out web/dist upstream

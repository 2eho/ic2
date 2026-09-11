# IC 重写 · 统一工程入口
#
# 约定（docs/design/13 §6）：所有门禁都能在本地与 CI 以**同一条命令**复现。
#
# 两条纪律（上一轮踩过的坑，写在这里防止复发）：
#   1. 不允许用 `cmd || echo 跳过` 这种写法：它会把「工具缺失」伪装成「检查通过」。
#      必须在 preflight 阶段显式失败，或显式列入 OPTIONAL。
#   2. 每个门禁都必须能在缺依赖时给出「怎么装」的提示，而不是默默变绿。

SHELL := /bin/bash
GO     ?= go
BIN    ?= bin
PKG    := github.com/context-flow/ic
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
COMMIT  ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo none)
DATE    ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
LDFLAGS := -X $(PKG)/internal/platform.Version=$(VERSION) \
           -X $(PKG)/internal/platform.Commit=$(COMMIT) \
           -X $(PKG)/internal/platform.Date=$(DATE)

.PHONY: help
help: ## 显示可用目标
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | awk 'BEGIN{FS=":.*?## "}{printf "  \033[36m%-20s\033[0m %s\n", $$1, $$2}'

# ---------------------------------------------------------------- 环境前置检查

.PHONY: preflight
preflight: ## 检查必备工具链（缺失即失败，不做静默跳过）
	@missing=0; \
	for tool in $(GO) node; do \
	  command -v $$tool >/dev/null 2>&1 || { echo "缺少必备工具：$$tool"; missing=1; }; \
	done; \
	[ $$missing -eq 0 ] || { echo "请安装缺失工具后重试（Go 1.24+ / Node 20+）"; exit 1; }; \
	echo "工具链就绪：$$($(GO) version | cut -d' ' -f3) / node $$(node -v)"

.PHONY: deps
deps: ## 安装本地依赖（Go modules + 前端）
	$(GO) mod download
	cd web && npm install --no-audit --no-fund

.PHONY: deps-web-check
deps-web-check: ## 校验前端依赖已安装（CI 前端任务用）
	@test -d web/node_modules || { echo "web/node_modules 缺失，请先执行 make deps"; exit 1; }

# ---------------------------------------------------------------- 构建

.PHONY: build
build: ## 构建服务端与运维 CLI
	@mkdir -p $(BIN)
	$(GO) build -ldflags "$(LDFLAGS)" -o $(BIN)/ic-server ./cmd/ic-server
	$(GO) build -ldflags "$(LDFLAGS)" -o $(BIN)/ic-cli    ./cmd/ic-cli

.PHONY: run
run: ## 本地启动服务端（开发模式，使用不安全开发密钥）
	IC_ALLOW_INSECURE_DEV_KEY=true IC_LOG_LEVEL=debug $(GO) run ./cmd/ic-server

.PHONY: web-build
web-build: ## 构建前端（产物由服务端同源托管）
	cd web && npm run build

.PHONY: dev
dev: ## 前端开发服务器（后端另行 make run）
	cd web && npm run dev

# ---------------------------------------------------------------- 契约

.PHONY: gen
gen: ## 从 contracts/ 生成代码并校验一致性（契约先行）
	node scripts/gen-contracts.mjs

.PHONY: gen-check
gen-check: ## 只校验生成物已提交（CI 用，不改文件）
	# 先自检解析器：校验脚本自己错了会伪装成「代码错了」
	node scripts/check-gen-parser.mjs
	node scripts/gen-contracts.mjs --check

# ---------------------------------------------------------------- 门禁

.PHONY: fmt
fmt: ## 格式化
	$(GO) fmt ./...
	cd web && npm exec -- prettier --write 'src/**/*.{ts,tsx,css}' 2>/dev/null || true

.PHONY: fmt-check
fmt-check: ## 校验格式（CI 门禁）
	@out=$$(gofmt -l . ); \
	if [ -n "$$out" ]; then echo "以下文件未格式化（请执行 make fmt）："; echo "$$out"; exit 1; fi; \
	echo "gofmt 通过"

.PHONY: vet
vet: ## go vet
	$(GO) vet ./...

.PHONY: lint
lint: ## 静态检查：vet + 架构约束 + CI 配置 + 前端 tsc
	$(GO) vet ./...
	node scripts/check-cnb-config.mjs
	node scripts/check-kernel-purity.mjs
	node scripts/check-node-schema.mjs
	node scripts/check-boundaries.mjs
	node scripts/check-features-boundary.mjs
	node scripts/check-file-size.mjs

.PHONY: tsc
tsc: deps-web-check ## 前端类型检查
	cd web && npm run lint

.PHONY: test
test: ## 单测 + 对抗用例（ATK-*）
	$(GO) test ./... -count=1 -timeout 300s -coverprofile=coverage.out
	cd web && npm test
	cd canvas-agent && node --test src/*.test.js

.PHONY: test-go
test-go: ## 仅 Go 单测（无前端依赖时用）
	$(GO) test ./... -count=1 -timeout 300s

.PHONY: test-agent
test-agent: ## 本机桥接器单测（事件归一化 + 安全边界）
	cd canvas-agent && node --test src/*.test.js

.PHONY: cover
cover: test ## 覆盖率报告
	$(GO) tool cover -func=coverage.out | tail -25

.PHONY: adversary
adversary: ## 对抗用例门禁：ATK-01..22 必须全部有可执行用例（缺一条即失败）
	node scripts/check-adversary.mjs

.PHONY: parity
parity: ## 对等矩阵覆盖率（--min 可指定门槛）
	node scripts/parity-report.mjs

.PHONY: parity-enforce
parity-enforce: ## 对等矩阵覆盖率强制门禁（发版口径 100%）
	node scripts/parity-report.mjs --min=1.0

.PHONY: boundaries
boundaries: ## 边界常量与文档一致（唯一真源 internal/graph/limits.go）
	node scripts/check-boundaries.mjs

.PHONY: sec
sec: ## 安全门禁：凭据脱敏、SSRF、插件权限、资产隔离、依赖漏洞
	$(GO) test ./internal/platform/ -run 'TestATK04|TestATK06|TestRedact|TestSSRF|TestDrill' -count=1
	$(GO) test ./internal/plugin/ -run 'TestATK09|TestSandbox|TestNetwork|TestInstallRejectsPermission' -count=1
	$(GO) test ./internal/asset/ -run 'TestATK05|TestATK16|TestUploadSanitizes|TestCrossWorkspace|TestGC' -count=1
	$(GO) test ./internal/provider/ -run 'TestATK03' -count=1
	node scripts/check-secrets.mjs
	node scripts/check-kernel-purity.mjs

.PHONY: vet-extra
vet-extra: ## 可选深度静态检查（工具缺失时显式报告，不影响 check）
	@if command -v staticcheck >/dev/null 2>&1; then staticcheck ./...; else echo "staticcheck 未安装（可选）：go install honnef.co/go/tools/cmd/staticcheck@latest"; fi
	@if command -v govulncheck >/dev/null 2>&1; then govulncheck ./...; else echo "govulncheck 未安装（可选）：go install golang.org/x/vuln/cmd/govulncheck@latest"; fi

.PHONY: drill
drill: ## 故障演练子集（时钟回拨 / 配置非法 / DB 不可用 / 重定向 SSRF / 冲突 / 恢复）
	$(GO) test ./internal/platform/ -run 'TestDrill' -count=1
	$(GO) test ./internal/graph/ -run 'TestVersionConflict|TestRebaseable|TestATK21' -count=1
	$(GO) test ./internal/exec/ -run 'TestATK02|TestATK15|TestResume|TestCancel|TestPartialFailure' -count=1
	$(GO) test ./internal/asset/ -run 'TestATK16|TestGC' -count=1
	$(GO) test ./internal/identity/ -run 'TestATK22' -count=1
	$(GO) test ./internal/contract/ -count=1

.PHONY: agent-check
agent-check: ## 桥接器静态检查（语法 + 安全约束）
	@for f in canvas-agent/src/*.js; do node --check "$$f" || exit 1; done
	@echo "canvas-agent 语法检查通过"

.PHONY: e2e
e2e: deps-web-check ## 端到端主链路（Playwright；缺浏览器时给出安装提示）
	$(GO) test ./internal/api/apitest/ -count=1 -v
	cd web && npm exec -- playwright test 2>/dev/null || { echo "Playwright 浏览器未安装：cd web && npx playwright install --with-deps chromium"; exit 1; }

.PHONY: perf
perf: deps-web-check ## 性能预算校验（内核 + 视口；见 docs/design/13 §3.3）
	node scripts/perf-budget.mjs

.PHONY: check
check: preflight gen-check fmt-check lint test test-agent agent-check adversary boundaries parity sec drill ## 本地全套门禁（CI 用这一个）

.PHONY: check-all
check-all: check tsc e2e perf ## 全套 + 前端类型/端到端/性能

.PHONY: clean
clean: ## 清理构建产物
	rm -rf $(BIN) coverage.out web/dist web/playwright-report web/test-results upstream research

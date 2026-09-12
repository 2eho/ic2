package contract_test

import (
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// TestContractsAreGenerated 校验「代码与契约一致」这条纪律本身可执行（docs/design/08 §6）。
//
// 为什么把它写成 Go 测试：`make gen` 只有被真的跑过才有意义。
// 如果只在 CI 里跑一个 node 脚本，本地开发者很容易绕过；
// 放进 go test 后，任何一次 `go test ./...` 都会强制检查契约同步。
func TestContractsAreGenerated(t *testing.T) {
	repoRoot := repoRootFrom(t)
	cmd := exec.Command("node", "scripts/gen-contracts.mjs", "--check")
	cmd.Dir = repoRoot
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("契约校验失败（请执行 make gen 并提交生成物）:\n%s", out)
	}
	if !strings.Contains(string(out), "契约与代码一致") {
		t.Fatalf("未能确认契约一致性，输出为:\n%s", out)
	}
}

// TestErrorCodesHaveI18n 校验每个稳定错误码都有中英文案。
// 服务端返回 code、前端展示文案，中间少一环用户就会看到裸 code。
func TestErrorCodesHaveI18n(t *testing.T) {
	repoRoot := repoRootFrom(t)
	cmd := exec.Command("node", "scripts/gen-contracts.mjs", "--check")
	cmd.Dir = repoRoot
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("契约校验失败，i18n 覆盖无法确认:\n%s", out)
	}
}

func repoRootFrom(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("无法定位测试文件路径")
	}
	// internal/contract/conformance_test.go → 仓库根
	return filepath.Dir(filepath.Dir(filepath.Dir(file)))
}

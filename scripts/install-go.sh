#!/usr/bin/env bash
# 在「只带 node 的镜像」里安装 Go 工具链（幂等，可重复执行）。
#
# 为什么是「装工具链」而不是「换镜像」：
#   门禁的入口是 scripts/ci-exec.mjs，而它本身是 node 脚本 ——
#   所以任务镜像**必须**有 node。此前 gate 用 golang:1.24（无 node），
#   结果第一步 `node scripts/ci-exec.mjs probe ...` 就 127，
#   后面 13 个 stage 全部 skip；换回 node:22 又没 go，e2e 卡在 go 缺失。
#   两个镜像都不满足「node + go」，唯一自洽的做法是：以 node 为基准镜像，
#   在任务内装 Go。
#
# 与 docs/design/13 里「不要现场补工具链」那条纪律的关系：
#   那条纪律反对的是**猜发行版的包管理器**（书里实测 bookworm 只有 golang 1.19，
#   版本不达标会让门禁在错误的环境里跑，比直接红更危险）。
#   这里不是猜：官方 tarball 自带确定版本，装完立刻校验 `go version` 精确匹配，
#   不匹配即失败。因此它满足「宁可红，也不要假绿」这条底线。
set -euo pipefail

GO_VERSION="${GO_VERSION:-1.24.6}"
GO_ROOT="${GO_ROOT:-/usr/local/go}"
ARCH="$(uname -m)"
case "$ARCH" in
  x86_64 | amd64) GOARCH=amd64 ;;
  aarch64 | arm64) GOARCH=arm64 ;;
  *)
    echo "不支持的架构：$ARCH（请改用自带 Go 的镜像）" >&2
    exit 1
    ;;
esac

# 幂等：已经装了正确版本就直接返回。重复执行不会重新下载，
# 也让「本地已有 go」的开发机可以安全地跑同一条命令。
if [ -x "$GO_ROOT/bin/go" ] && "$GO_ROOT/bin/go" version 2>/dev/null | grep -q "go${GO_VERSION}\b"; then
  echo "Go 已就绪：$("$GO_ROOT/bin/go" version)"
  exit 0
fi

# PATH 里已有正确版本也直接返回（自托管 Runner 可能预装了 go）。
if command -v go >/dev/null 2>&1 && go version 2>/dev/null | grep -q "go${GO_VERSION}\b"; then
  echo "Go 已就绪（PATH）：$(go version)"
  exit 0
fi

TARBALL="go${GO_VERSION}.linux-${GOARCH}.tar.gz"
URL="https://go.dev/dl/${TARBALL}"
TMP="$(mktemp -d)"
trap 'rm -rf "$TMP"' EXIT

echo "下载 ${URL}"
# 优先 curl，其次 wget：两者都缺就显式失败，不静默跳过。
if command -v curl >/dev/null 2>&1; then
  curl -fsSL --retry 3 -o "$TMP/$TARBALL" "$URL"
elif command -v wget >/dev/null 2>&1; then
  wget -q -O "$TMP/$TARBALL" "$URL"
else
  echo "缺少 curl / wget，无法下载 Go（请改用自带 Go 的镜像）" >&2
  exit 1
fi

rm -rf "$GO_ROOT"
mkdir -p "$(dirname "$GO_ROOT")"
tar -C "$(dirname "$GO_ROOT")" -xzf "$TMP/$TARBALL"

# 校验安装结果：版本必须**精确**等于期望值。
# 「装上了」不等于「装对了」——版本不符会让门禁在错误环境里跑。
ACTUAL="$("$GO_ROOT/bin/go" version)"
echo "$ACTUAL"
case "$ACTUAL" in
  *"go${GO_VERSION} "*) ;;
  *)
    echo "Go 版本不符：期望 go${GO_VERSION}，实际：$ACTUAL" >&2
    exit 1
    ;;
esac

# 把 go / gofmt 暴露到 PATH：后续 stage 由 ci-exec 的 `command -v` 定位，
# 不依赖调用方的 shell 配置，因此这里写系统目录而不是 ~/.bashrc。
for bin in go gofmt; do
  ln -sf "$GO_ROOT/bin/$bin" "/usr/local/bin/$bin"
done

echo "Go 工具链安装完成：$(command -v go) / $(command -v gofmt)"

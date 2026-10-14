#!/usr/bin/env sh
# ReadScope 端到端演示（m1+m2）：在一个 Linux 容器里挂载默认拒绝视图，
# 运行内置 mock agent，观察内联授权与暴露清单落盘。
#
# 用法:   ./examples/run-demo-docker.sh
# 前提:   Docker Desktop / Docker Engine（本机无 FUSE 的 macOS 也适用）。
set -eu

REPO_ROOT=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
OUT_DIR="${TMPDIR:-/tmp}/readscope-demo-out"
rm -rf "$OUT_DIR"
mkdir -p "$OUT_DIR"

echo "==> 交叉编译 linux 单二进制（随宿主 Docker 架构）"
LINUX_ARCH=$(docker version --format '{{.Server.Arch}}' | sed 's/x86_64/amd64/;s/aarch64/arm64/')
( cd "$REPO_ROOT" && CGO_ENABLED=0 GOOS=linux GOARCH="$LINUX_ARCH" \
  go build -o "$OUT_DIR/readscope" ./cmd/readscope )

echo "==> 准备一次性演示工作区（可写副本，不污染仓库）"
cp -R "$REPO_ROOT/examples/demo-workspace" "$OUT_DIR/workspace"

echo "==> 容器内运行：挂载默认拒绝视图，运行 mock agent"
echo "    （agent 请求 .env 时，终端弹出 y/N 内联授权提示；演示时输入 y 放行 .env）"
docker run --rm -it \
  --cap-add SYS_ADMIN --device /dev/fuse \
  -e READSCOPE_SRC=/src \
  -v "$OUT_DIR/readscope:/usr/local/bin/readscope:ro" \
  -v "$OUT_DIR/workspace":/src \
  -v "$REPO_ROOT/policies":/policies:ro \
  -v "$OUT_DIR":/out \
  alpine:3.20 sh -c 'apk add --no-cache fuse >/dev/null 2>&1 && \
    readscope run --src /src --policy /policies/default.yaml --out /out \
      --model deepseek-v4.1-flash -- readscope mockagent'

echo
echo "==> 运行产物在: $OUT_DIR"
echo "    decisions.jsonl / grants.jsonl / exposure-manifest-*.json"
echo "    宿主机复核摘要: readscope inspect \"\$OUT_DIR\"/<run_id>"

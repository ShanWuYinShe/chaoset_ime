#!/bin/bash
# 在 Docker 容器内搭建可复现的 ibus 验证环境（Ubuntu 24.04 + ibus 1.5）。
# 用法（在仓库根目录）：
#   shims/linux/setup-container.sh        # 创建/重置容器并安装环境
# 之后用 shims/linux/test.sh 做端到端验证。
set -e

ROOT=$(cd "$(dirname "$0")/../.." && pwd)
IMAGE=ubuntu:24.04
NAME=chaoset-ibus

docker rm -f "$NAME" >/dev/null 2>&1 || true

# 容器主进程负责安装环境并常驻：这样 dbus/ibus-daemon/引擎都在
# PID 1 的进程树下，不会被后续 docker exec 会话的退出清理掉。
docker run -d --name "$NAME" -v "$ROOT":/src -w /src "$IMAGE" bash -c '
apt-get update -qq
DEBIAN_FRONTEND=noninteractive apt-get install -y -qq \
  build-essential pkg-config libibus-1.0-dev \
  ibus dbus gir1.2-ibus-1.0 libgtk-3-dev xvfb >/dev/null
mkdir -p /usr/local/share/chaoset
Xvfb :99 -screen 0 1280x800x24 >/tmp/xvfb.log 2>&1 &
export DISPLAY=:99
sleep 1
dbus-daemon --session --fork --print-address > /tmp/dbus_addr
export DBUS_SESSION_BUS_ADDRESS=$(cat /tmp/dbus_addr)
ibus-daemon --daemonize --panel=disable
touch /tmp/READY
echo "ibus environment ready"
sleep infinity'

echo "等待环境就绪（首次需安装软件包，约 1-3 分钟）..."
READY=
for _ in $(seq 1 60); do
  if docker exec "$NAME" test -f /tmp/READY >/dev/null 2>&1; then READY=1; break; fi
  sleep 5
done
if [ -z "$READY" ]; then
  echo "错误：容器环境未就绪" >&2
  docker logs "$NAME" 2>&1 | tail -30 >&2 || true
  exit 1
fi
echo "就绪"

# 原生引擎必须在 Linux 容器内构建（libibus 头文件/ABI 不能从 macOS 交叉编译）。
docker exec "$NAME" sh -c 'cd /src && sh shims/linux/engine/build.sh /usr/local/bin/chaoset-engine'

# Go daemon 按容器架构在宿主机交叉编译。
ARCH=$(docker exec "$NAME" uname -m)
case "$ARCH" in
  aarch64) GOARCH=arm64 ;;
  x86_64) GOARCH=amd64 ;;
  *) echo "不支持的容器架构: $ARCH" >&2; exit 1 ;;
esac
mkdir -p "$ROOT/build"
( cd "$ROOT" && GOOS=linux GOARCH="$GOARCH" CGO_ENABLED=0 go build -o build/chaosetd ./cmd/chaosetd )
docker cp "$ROOT/build/chaosetd" "$NAME:/usr/local/bin/chaosetd"
docker cp "$ROOT/data/dict_common.tsv" "$NAME:/usr/local/share/chaoset/dict.tsv"
docker exec "$NAME" chmod +x /usr/local/bin/chaoset-engine /usr/local/bin/chaosetd
echo "部署完成：chaoset-engine / chaosetd / 词库 已就位"
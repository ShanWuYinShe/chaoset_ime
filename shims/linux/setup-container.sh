#!/bin/bash
# 在 Docker 容器内搭建可复现的 ibus 验证环境（Ubuntu 24.04 + ibus 1.5）。
# 用法（在仓库根目录）：
#   shims/linux/setup-container.sh        # 创建/重置容器并安装环境
# 之后用 shims/linux/test.sh 做端到端验证。
set -e

IMAGE=ubuntu:24.04
NAME=chaoset-ibus

docker rm -f $NAME >/dev/null 2>&1 || true

# 容器主进程负责安装环境并常驻：这样 dbus/ibus-daemon/引擎都在
# PID 1 的进程树下，不会被后续 docker exec 会话的退出清理掉。
docker run -d --name $NAME $IMAGE bash -c '
apt-get update -qq
DEBIAN_FRONTEND=noninteractive apt-get install -y -qq ibus dbus gir1.2-ibus-1.0 python3-gi >/dev/null
mkdir -p /usr/local/share/chaoset
dbus-daemon --session --fork --print-address > /tmp/dbus_addr
export DBUS_SESSION_BUS_ADDRESS=$(cat /tmp/dbus_addr)
ibus-daemon --daemonize --panel=disable
touch /tmp/READY
echo "ibus environment ready"
sleep infinity'

echo "等待环境就绪（首次需安装软件包，约 1-3 分钟）..."
for i in $(seq 1 30); do
  docker exec $NAME ls /tmp/READY >/dev/null 2>&1 && { echo "就绪"; break; }
  sleep 10
done

# 部署 chaoset 引擎二进制（macOS 上交叉编译：GOOS=linux GOARCH=arm64 go build）
cd "$(dirname "$0")/../.."
GOOS=linux GOARCH=arm64 go build -o build/chaoset-ibus-engine ./shims/linux/ibus-engine/
docker cp build/chaoset-ibus-engine $NAME:/usr/local/bin/chaoset-engine
docker cp data/dict_seed.tsv $NAME:/usr/local/share/chaoset/dict.tsv
docker exec $NAME chmod +x /usr/local/bin/chaoset-engine
echo "部署完成：chaoset-engine 与词库已就位"

#!/bin/sh
# 阳和财务系统 · 导入离线镜像
# 与 docker-compose.offline.yml 放在同一层目录，镜像在 images/ 下。

set -eu

here=$(cd "$(dirname "$0")" && pwd)
root=$(cd "$here/.." && pwd)
cd "$root"

arch=$(uname -m)
case "$arch" in
  x86_64 | amd64) want=amd64 ;;
  aarch64 | arm64) want=arm64 ;;
  *)
    echo "无法识别的 CPU 架构：$arch"
    exit 1
    ;;
esac

echo "当前架构：$arch（需要 $want 镜像）"

if [ ! -d images ]; then
  echo "找不到 images 目录，请在解压后的交付包根目录执行本脚本。"
  exit 1
fi

loaded=0
skip=0

for f in images/*.tar; do
  [ -e "$f" ] || continue
  case "$f" in
    *-"$want".tar | *-"$want".tar.gz) ;;
    *)
      echo "跳过（架构不匹配）：$f"
      skip=$((skip + 1))
      continue
      ;;
  esac
  echo "导入：$f"
  docker load -i "$f"
  loaded=$((loaded + 1))
done

if [ "$loaded" -eq 0 ]; then
  echo "没有找到与 $want 匹配的镜像包（跳过了 $skip 个）。"
  echo "说明这个交付包不是给当前架构用的，请换 $want 的包。"
  exit 1
fi

# 导入成功不等于能用：镜像本身可能是另一种架构（拿错了包、或包里打错了）。
# 在安装阶段就核对一次，比等容器起来 exec format error 好排查得多。
check_arch() {
  img="$1"
  got=$(docker image inspect --format '{{.Architecture}}' "$img" 2>/dev/null || echo '')
  case "$want:$got" in
    amd64:amd64 | arm64:arm64) return 0 ;;
  esac
  echo
  echo "!! 镜像 $img 的架构是 [$got]，当前机器需要 [$want]。"
  echo "!! 这个交付包用不了，请换对应架构的离线包重新执行。"
  return 1
}

echo
echo "核对镜像架构…"
bad=0
for img in $(docker images --format '{{.Repository}}:{{.Tag}}' | grep -E '^yanghe-finance:|^postgres:16-alpine' || true); do
  check_arch "$img" || bad=1
done

if [ "$bad" -ne 0 ]; then
  exit 1
fi
echo "架构核对通过（$want）"

echo
echo "已导入 $loaded 个镜像："
docker images | grep -E 'yanghe-finance|postgres' || true
echo
echo "下一步："
echo "  1. cp .env.example .env  并修改 DB_PASSWORD / APP_IMAGE"
echo "  2. docker compose -f docker-compose.offline.yml up -d"

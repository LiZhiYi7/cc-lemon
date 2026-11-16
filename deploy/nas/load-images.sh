#!/bin/sh
# 柠檬账 · 导入离线镜像
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
  exit 1
fi

echo
echo "已导入 $loaded 个镜像："
docker images | grep -E 'cc-lemon|postgres' || true
echo
echo "下一步："
echo "  1. cp .env.example .env  并修改 DB_PASSWORD / APP_IMAGE"
echo "  2. docker compose -f docker-compose.offline.yml up -d"

#!/bin/sh
# 备份容器的入口：每 24 小时导出一次数据库，并清理超过 KEEP_DAYS 天的旧备份。
# 恢复：pg_restore -h <主机> -U yanghe_finance -d yanghe_finance --clean --if-exists <备份文件>
set -u
BACKUP_DIR=/backups
KEEP_DAYS="${KEEP_DAYS:-30}"

while true; do
  target="$BACKUP_DIR/yanghe-finance-$(date +%Y%m%d-%H%M%S).dump"
  # 先写临时文件，成功后再改名，避免留下不完整的备份
  if pg_dump -h db -U yanghe_finance -d yanghe_finance -Fc -f "$target.partial"; then
    mv "$target.partial" "$target"
    echo "备份完成 $target"
  else
    echo "备份失败，将在下个周期重试" >&2
  fi
  find "$BACKUP_DIR" -name 'yanghe-finance-*.dump' -mtime "+$KEEP_DAYS" -delete
  find "$BACKUP_DIR" -name 'yanghe-finance-*.partial' -mtime +1 -delete
  sleep 86400
done

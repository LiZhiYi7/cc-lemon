#!/bin/sh
# 备份容器的入口：每 24 小时导出一次数据库与凭证图片，并清理超过 KEEP_DAYS 天的旧备份。
# 恢复数据库：pg_restore -h <主机> -U yanghe_finance -d yanghe_finance --clean --if-exists <备份文件>
# 恢复图片：  tar -xzf yanghe-finance-files-<时间>.tar.gz -C <附件目录>
set -u
BACKUP_DIR=/backups
KEEP_DAYS="${KEEP_DAYS:-30}"

while true; do
  stamp="$(date +%Y%m%d-%H%M%S)"
  dump="$BACKUP_DIR/yanghe-finance-$stamp.dump"
  files="$BACKUP_DIR/yanghe-finance-files-$stamp.tar.gz"

  # 数据库：先写临时文件，成功后再改名，避免留下不完整的备份
  if pg_dump -h db -U yanghe_finance -d yanghe_finance -Fc -f "$dump.partial"; then
    mv "$dump.partial" "$dump"
    echo "数据库备份完成 $dump"
  else
    rm -f "$dump.partial"
    echo "数据库备份失败，将在下个周期重试" >&2
  fi

  # 凭证图片在文件系统里（数据库没有对应的表），必须单独打包，
  # 否则恢复数据库之后图片全部丢失。
  if [ -d /attachments ] && [ -n "$(ls -A /attachments 2>/dev/null)" ]; then
    if tar -czf "$files.partial" -C /attachments . 2>/dev/null; then
      mv "$files.partial" "$files"
      echo "图片附件备份完成 $files"
    else
      rm -f "$files.partial"
      echo "图片附件备份失败，将在下个周期重试" >&2
    fi
  fi

  find "$BACKUP_DIR" -name 'yanghe-finance-*.dump' -mtime "+$KEEP_DAYS" -delete
  find "$BACKUP_DIR" -name 'yanghe-finance-files-*.tar.gz' -mtime "+$KEEP_DAYS" -delete
  find "$BACKUP_DIR" -name '*.partial' -mtime +1 -delete
  sleep 86400
done

// Package audit 记录操作日志。写入失败只记日志，不影响业务。
package audit

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"

	"github.com/lizhiyi7/cc-lemon/internal/platform/auth"
	"github.com/lizhiyi7/cc-lemon/internal/platform/db"
)

func Log(ctx context.Context, d *db.DB, bookID int64, action, entity string, entityID any, detail any) {
	b, err := json.Marshal(detail)
	if err != nil || detail == nil {
		b = []byte("{}")
	}
	var book any
	if bookID != 0 {
		book = bookID
	}
	var uid any
	if u := auth.CurrentUser(ctx); u.ID != 0 {
		uid = u.ID
	}
	if _, err := d.Q(ctx).Exec(ctx,
		`insert into audit_logs(book_id, user_id, action, entity, entity_id, detail) values($1,$2,$3,$4,$5,$6)`,
		book, uid, action, entity, fmt.Sprint(entityID), b); err != nil {
		slog.Warn("写入操作日志失败", "err", err)
	}
}

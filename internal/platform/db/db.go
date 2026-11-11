// Package db 封装连接池、上下文事务与迁移。
//
// 事务通过 context 传播：模块内部统一用 db.Q(ctx) 取执行器，
// 调用方用 db.Tx(ctx, fn) 包裹即可让跨模块调用落在同一事务内。
package db

import (
	"context"
	"fmt"
	"io/fs"
	"sort"

	pgxdecimal "github.com/jackc/pgx-shopspring-decimal"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Querier 是连接池与事务的公共子集。
type Querier interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

type DB struct{ Pool *pgxpool.Pool }

type txKey struct{}

func Open(ctx context.Context, dsn string, maxConns int) (*DB, error) {
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, fmt.Errorf("解析数据库连接串: %w", err)
	}
	if maxConns > 0 {
		cfg.MaxConns = int32(maxConns)
	}
	cfg.AfterConnect = func(_ context.Context, c *pgx.Conn) error {
		pgxdecimal.Register(c.TypeMap())
		return nil
	}
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, err
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("连接数据库: %w", err)
	}
	return &DB{Pool: pool}, nil
}

func (d *DB) Close() { d.Pool.Close() }

// Q 返回当前上下文的执行器：在事务中返回事务，否则返回连接池。
func (d *DB) Q(ctx context.Context) Querier {
	if tx, ok := ctx.Value(txKey{}).(pgx.Tx); ok {
		return tx
	}
	return d.Pool
}

// Tx 在事务中执行 fn；若上下文已在事务中则直接复用（不嵌套）。
func (d *DB) Tx(ctx context.Context, fn func(ctx context.Context) error) error {
	if _, ok := ctx.Value(txKey{}).(pgx.Tx); ok {
		return fn(ctx)
	}
	tx, err := d.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx) //nolint:errcheck // 提交后回滚为空操作
	if err := fn(context.WithValue(ctx, txKey{}, tx)); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// Migrate 依文件名顺序执行尚未应用的 .sql 迁移，每个文件一个事务。
func (d *DB) Migrate(ctx context.Context, fsys fs.FS) error {
	if _, err := d.Pool.Exec(ctx, `create table if not exists schema_migrations (
		version text primary key, applied_at timestamptz not null default now())`); err != nil {
		return err
	}
	names, err := fs.Glob(fsys, "*.sql")
	if err != nil {
		return err
	}
	sort.Strings(names)
	for _, name := range names {
		var done bool
		if err := d.Pool.QueryRow(ctx, `select exists(select 1 from schema_migrations where version=$1)`, name).Scan(&done); err != nil {
			return err
		}
		if done {
			continue
		}
		sql, err := fs.ReadFile(fsys, name)
		if err != nil {
			return err
		}
		err = d.Tx(ctx, func(ctx context.Context) error {
			if _, err := d.Q(ctx).Exec(ctx, string(sql)); err != nil {
				return err
			}
			_, err := d.Q(ctx).Exec(ctx, `insert into schema_migrations(version) values($1)`, name)
			return err
		})
		if err != nil {
			return fmt.Errorf("迁移 %s 失败: %w", name, err)
		}
	}
	return nil
}

// Collect 执行查询并用 scan 逐行收集结果。
func Collect[T any](ctx context.Context, q Querier, scan func(pgx.Rows) (T, error), sql string, args ...any) ([]T, error) {
	rows, err := q.Query(ctx, sql, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []T{}
	for rows.Next() {
		v, err := scan(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

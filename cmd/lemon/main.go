// 柠檬账 —— 局域网财务系统服务端入口。
package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/lizhiyi7/cc-lemon/internal/app"
	"github.com/lizhiyi7/cc-lemon/internal/platform/config"
	"github.com/lizhiyi7/cc-lemon/internal/platform/db"
)

func main() {
	if err := run(); err != nil {
		slog.Error("启动失败", "err", err)
		os.Exit(1)
	}
}

func run() error {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	cfg, err := config.Load()
	if err != nil {
		return err
	}
	// 容器编排下数据库可能稍晚就绪，启动时重试一段时间。
	var d *db.DB
	for attempt := 1; ; attempt++ {
		if d, err = db.Open(ctx, cfg.Database.DSN, cfg.Database.MaxConns); err == nil {
			break
		}
		if attempt >= 30 {
			return err
		}
		slog.Warn("数据库未就绪，稍后重试", "attempt", attempt, "err", err)
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(2 * time.Second):
		}
	}
	defer d.Close()

	handler, err := app.New(ctx, cfg, d)
	if err != nil {
		return err
	}
	srv := &http.Server{Addr: cfg.Server.Addr, Handler: handler, ReadHeaderTimeout: 10 * time.Second}
	go func() {
		<-ctx.Done()
		shutdown, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdown)
	}()
	slog.Info("服务已启动", "addr", cfg.Server.Addr)
	if err := srv.ListenAndServe(); !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

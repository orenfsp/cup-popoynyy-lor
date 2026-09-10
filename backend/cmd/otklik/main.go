package main

import (
	"context"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/hackathon/otklik/backend/internal/config"
	"github.com/hackathon/otklik/backend/internal/httpapi"
	"github.com/hackathon/otklik/backend/internal/store"
	"github.com/redis/go-redis/v9"
)

func main() {
	log := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	cfg, err := config.Load()
	if err != nil { log.Error("configuration error", "error", err); os.Exit(1) }
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	st, err := store.New(ctx, cfg.DatabaseURL, []byte(cfg.TokenPepper))
	if err != nil { log.Error("database connection failed", "error", err); os.Exit(1) }
	defer st.Close()
	rdb := redis.NewClient(&redis.Options{Addr: cfg.RedisAddr})
	defer rdb.Close()

	srv := &http.Server{Addr: cfg.HTTPAddr, Handler: httpapi.New(cfg, st, rdb, log), ReadHeaderTimeout: 5*time.Second, ReadTimeout: 15*time.Second, WriteTimeout: 30*time.Second, IdleTimeout: 60*time.Second}
	go func(){ log.Info("http server started", "addr", cfg.HTTPAddr); if err:=srv.ListenAndServe(); err!=nil && err!=http.ErrServerClosed { log.Error("http server stopped", "error", err); stop() } }()
	<-ctx.Done()
	shutdown, cancel := context.WithTimeout(context.Background(), 10*time.Second); defer cancel()
	_ = srv.Shutdown(shutdown)
}

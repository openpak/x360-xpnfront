// Command x360-xpnfront serves Xbox Live presence and friends, Live service 1 (see internal/xpnfront).
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

	"github.com/openpak/x360-common/accountcore"
	"github.com/openpak/x360-common/wire"
	"github.com/openpak/x360-common/xuacsapi"
	"github.com/openpak/x360-xpnfront/internal/xpnfront"
)

func main() {
	log := slog.New(slog.NewTextHandler(os.Stderr, nil))
	addr := os.Getenv("X360_XPNFRONT_ADDR")
	if addr == "" {
		addr = ":10001"
	}
	core, err := accountcore.New(os.Getenv("X360_XPNFRONT_ACCOUNT_CORE_URL"), os.Getenv("X360_XPNFRONT_ACCOUNT_CORE_TOKEN"), nil)
	if err != nil {
		log.Error("account core (X360_XPNFRONT_ACCOUNT_CORE_URL, _TOKEN)", "err", err)
		os.Exit(2)
	}
	ids := &xuacsapi.Client{URL: os.Getenv("X360_XUACS_INTERNAL_URL"), Key: os.Getenv("X360_XUACS_INTERNAL_KEY")}
	if ids.URL == "" || ids.Key == "" {
		log.Error("X360_XUACS_INTERNAL_URL and X360_XUACS_INTERNAL_KEY are required")
		os.Exit(2)
	}
	h := &xpnfront.Handler{Social: core, IDs: ids, Log: log}
	srv := &http.Server{Addr: addr, Handler: wire.Guard(os.Getenv("X360_GATEWAY_KEY"), h), ReadHeaderTimeout: 10 * time.Second}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	go func() {
		<-ctx.Done()
		sc, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		srv.Shutdown(sc)
	}()
	log.Info("x360-xpnfront listening", "addr", addr)
	if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Error("serve", "err", err)
		os.Exit(1)
	}
}

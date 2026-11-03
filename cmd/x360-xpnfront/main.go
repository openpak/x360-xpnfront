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
	"github.com/openpak/x360-common/httplog"
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
	dbCtx, dbCancel := context.WithTimeout(context.Background(), 30*time.Second)
	// X360_XPNFRONT_STATE is only the old state.json, imported once when the database is empty.
	state, err := xpnfront.OpenState(dbCtx, os.Getenv("X360_XPNFRONT_DATABASE_URL"), xpnfront.Schema, os.Getenv("X360_XPNFRONT_STATE"))
	dbCancel()
	if err != nil {
		log.Error("state: X360_XPNFRONT_DATABASE_URL is required and must be reachable", "err", err)
		os.Exit(2)
	}
	defer state.Close()
	log.Info("state in Postgres", "schema", xpnfront.Schema)
	h := &xpnfront.Handler{Social: core, IDs: ids, Log: log, State: state}
	if u := os.Getenv("X360_XMESSAGING_INTERNAL_URL"); u != "" {
		h.Mail = &xpnfront.UnreadClient{URL: u, Key: os.Getenv("X360_XMESSAGING_INTERNAL_KEY")}
	}
	srv := &http.Server{Addr: addr, Handler: httplog.Wrap(wire.Guard(os.Getenv("X360_GATEWAY_KEY"), h)), ReadHeaderTimeout: 10 * time.Second}
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

// Commande filamentmanager : démarre le serveur web. Toute la configuration vient des
// variables d'environnement (voir SPEC.md §11).
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/MaximeBranger/filament_manager/internal/backup"
	"github.com/MaximeBranger/filament_manager/internal/config"
	"github.com/MaximeBranger/filament_manager/internal/db"
	"github.com/MaximeBranger/filament_manager/internal/mail"
	"github.com/MaximeBranger/filament_manager/internal/store"
	"github.com/MaximeBranger/filament_manager/internal/web"
)

// version est injectée au build (-ldflags "-X main.version=…").
var version = "dev"

func main() {
	if len(os.Args) > 1 && os.Args[1] == "healthcheck" {
		os.Exit(healthcheck())
	}
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "erreur :", err)
		os.Exit(1)
	}
}

// healthcheck interroge /healthz : utilisé par le HEALTHCHECK Docker (image sans shell ni curl).
func healthcheck() int {
	addr := os.Getenv("LISTEN_ADDR")
	if addr == "" {
		addr = ":8080"
	}
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return 1
	}
	if host == "" || host == "0.0.0.0" || host == "::" {
		host = "127.0.0.1"
	}
	client := &http.Client{Timeout: 3 * time.Second}
	resp, err := client.Get("http://" + net.JoinHostPort(host, port) + "/healthz")
	if err != nil {
		return 1
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return 1
	}
	return 0
}

func run() error {
	cfg, err := config.Load(os.Getenv)
	if err != nil {
		return fmt.Errorf("configuration invalide :\n%w", err)
	}

	var level slog.Level
	level.UnmarshalText([]byte(cfg.LogLevel))
	log := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: level}))

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	conn, err := db.Open(ctx, cfg.DatabasePath)
	if err != nil {
		return err
	}
	defer conn.Close()
	st := store.New(conn)

	mailer := &mail.SMTP{
		Host: cfg.SMTPHost, Port: cfg.SMTPPort, User: cfg.SMTPUser, Password: cfg.SMTPPassword,
		From: cfg.SMTPFrom, TLS: cfg.SMTPTLS,
	}
	srv, err := web.New(cfg, st, mailer, log, version)
	if err != nil {
		return err
	}

	go srv.PurgeLoop(ctx)
	go srv.SharePurgeLoop(ctx)
	if cfg.BackupDir != "" {
		runner := &backup.Runner{Dir: cfg.BackupDir, Interval: cfg.BackupInterval, Retention: cfg.BackupRetention, DB: st, Log: log}
		go runner.Run(ctx)
	} else {
		log.Warn("sauvegardes désactivées (BACKUP_DIR vide)")
	}

	httpServer := &http.Server{
		Addr:              cfg.ListenAddr,
		Handler:           srv.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      60 * time.Second,
		IdleTimeout:       120 * time.Second,
	}
	errCh := make(chan error, 1)
	go func() {
		log.Info("démarrage", "version", version, "addr", cfg.ListenAddr, "base_url", cfg.BaseURL.String())
		errCh <- httpServer.ListenAndServe()
	}()

	select {
	case err := <-errCh:
		if !errors.Is(err, http.ErrServerClosed) {
			return err
		}
	case <-ctx.Done():
		log.Info("arrêt demandé")
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		if err := httpServer.Shutdown(shutdownCtx); err != nil {
			return err
		}
	}
	return nil
}

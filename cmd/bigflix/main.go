package main

import (
	"bigflix/internal/catalog"
	"bigflix/internal/database"
	"bigflix/internal/downloads"
	"bigflix/internal/web"
	"context"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"syscall"
	"time"
)

func env(k, d string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return d
}
func main() {
	if err := run(); err != nil {
		slog.Error("Bigflix stopped", "error", err)
		os.Exit(1)
	}
}
func run() error {
	for {
		err := runOnce()
		if !errors.Is(err, errRestart) {
			return err
		}
	}
}

var errRestart = errors.New("network settings changed")

func runOnce() error {
	port, err := strconv.Atoi(env("PORT", "3000"))
	if err != nil || port < 1 || port > 65535 {
		return errors.New("PORT must be between 1 and 65535")
	}
	bindAddr := env("BIND_ADDR", "127.0.0.1")
	if net.ParseIP(bindAddr) == nil {
		return errors.New("BIND_ADDR must be an IP address")
	}
	torrentPort, err := strconv.Atoi(env("TORRENT_LISTEN_PORT", "51413"))
	if err != nil || (torrentPort != 0 && (torrentPort < 1024 || torrentPort > 65535)) {
		return errors.New("TORRENT_LISTEN_PORT must be 0 (dynamic) or between 1024 and 65535")
	}
	db, err := database.Open(filepath.Join(env("DATA_DIR", "data"), "bigflix.db"))
	if err != nil {
		return err
	}
	defer db.Close()
	service, err := catalog.New(db, env("YTS_API_URL", "https://movies-api.accel.li/api/v2/"), os.Getenv("TMDB_READ_TOKEN"))
	if err != nil {
		return err
	}
	budget, e := strconv.ParseInt(env("MEDIA_BUDGET_GB", "50"), 10, 64)
	if e != nil || budget < 1 || budget > 10000 {
		return errors.New("MEDIA_BUDGET_GB must be 1..10000")
	}
	reserve, e := strconv.ParseUint(env("MIN_FREE_GB", "10"), 10, 64)
	if e != nil || reserve > 10000 {
		return errors.New("MIN_FREE_GB must be 0..10000")
	}
	engine, err := downloads.Open(db, downloads.Options{MediaDir: env("MEDIA_DIR", "media/torrents"), MaxBytes: budget * 1_000_000_000, ReserveBytes: reserve * 1_000_000_000, ListenPort: torrentPort})
	if errors.Is(err, syscall.EADDRINUSE) {
		// A saved fixed port may have been taken by another process. Recover
		// with a dynamic listener so the settings page stays reachable.
		slog.Warn("torrent listener failed; retrying with a dynamic port", "error", err)
		if saveErr := db.SetTorrentPort(context.Background(), 0); saveErr != nil {
			return err
		}
		engine, err = downloads.Open(db, downloads.Options{MediaDir: env("MEDIA_DIR", "media/torrents"), MaxBytes: budget * 1_000_000_000, ReserveBytes: reserve * 1_000_000_000})
	}
	if err != nil {
		return err
	}
	defer engine.Close()
	restart := make(chan struct{}, 1)
	handler, err := web.NewWithRestart(service, engine, func() {
		select {
		case restart <- struct{}{}:
		default:
		}
	})
	if err != nil {
		return err
	}
	server := &http.Server{Addr: net.JoinHostPort(bindAddr, strconv.Itoa(port)), Handler: handler, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 20 * time.Second, WriteTimeout: 30 * time.Second, IdleTimeout: 60 * time.Second}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	done := make(chan error, 1)
	go func() {
		network, _ := engine.NetworkSettings()
		slog.Info("Bigflix ready", "url", "http://"+net.JoinHostPort(bindAddr, strconv.Itoa(port)), "torrent_listen_port", network.ActualPort, "tmdb_configured", service.HasTMDB())
		done <- server.ListenAndServe()
	}()
	select {
	case err := <-done:
		return err
	case <-restart:
		shutdown, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = server.Shutdown(shutdown)
		_ = server.Close()
		return errRestart
	case <-ctx.Done():
		shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		err := server.Shutdown(shutdown)
		_ = server.Close()
		return err
	}
}

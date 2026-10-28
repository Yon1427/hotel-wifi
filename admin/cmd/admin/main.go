package main

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"hotel/admin/internal/mail"
	"hotel/admin/internal/store"
	"hotel/admin/internal/web"
)

func main() {
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	slog.SetDefault(logger)

	addr := env("ADMIN_LISTEN", ":8080")
	webDir := env("ADMIN_WEB_DIR", "web")
	tzName := env("HOTEL_TIMEZONE", "UTC")
	tz, err := time.LoadLocation(tzName)
	if err != nil {
		slog.Error("invalid HOTEL_TIMEZONE", "value", tzName, "err", err)
		os.Exit(1)
	}

	st, err := store.Open(dsn())
	if err != nil {
		slog.Error("open database", "err", err)
		os.Exit(1)
	}
	defer st.Close()

	if err := waitForDB(st, 30*time.Second); err != nil {
		slog.Error("database not ready", "err", err)
		os.Exit(1)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	n, err := st.CountAdmins(ctx)
	if err != nil {
		slog.Error("count admins", "err", err)
		os.Exit(1)
	}
	if n == 0 {
		user := env("ADMIN_USER", "admin")
		pass := os.Getenv("ADMIN_PASSWORD")
		if len(pass) < 12 || strings.HasPrefix(strings.ToLower(pass), "change-me") {
			slog.Error("no admin account exists; set a real ADMIN_PASSWORD (>=12 chars, not the placeholder) for first boot")
			os.Exit(1)
		}
		hash, err := web.HashPassword(pass)
		if err != nil {
			slog.Error("hash admin password", "err", err)
			os.Exit(1)
		}
		if err := st.CreateAdmin(ctx, user, hash); err != nil {
			slog.Error("create admin", "err", err)
			os.Exit(1)
		}
		slog.Info("created initial admin", "username", user)
	}

	mailCfg := mail.Config{
		Host:     os.Getenv("SMTP_HOST"),
		Port:     env("SMTP_PORT", "587"),
		User:     os.Getenv("SMTP_USER"),
		Password: os.Getenv("SMTP_PASSWORD"),
		From:     os.Getenv("SMTP_FROM"),
		Mode:     env("SMTP_TLS", "starttls"),
	}
	var mailer web.Mailer
	if mailCfg.Enabled() {
		mailer = mailCfg
		slog.Info("guest email enabled", "host", mailCfg.Host, "from", mailCfg.From)
	} else {
		slog.Warn("guest email disabled: set SMTP_HOST and SMTP_FROM")
	}

	srv, err := web.New(web.Config{
		Store:         st,
		WebDir:        webDir,
		SecureCookies: envBool("ADMIN_SECURE_COOKIES", false),
		TrustProxy:    envBool("ADMIN_TRUST_PROXY", false),
		HotelTZ:       tz,
		Mailer:        mailer,
		HotelName:     env("HOTEL_NAME", "Hotel"),
	})
	if err != nil {
		slog.Error("init web", "err", err)
		os.Exit(1)
	}

	httpSrv := &http.Server{
		Addr:              addr,
		Handler:           srv.Handler(),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	go func() {
		slog.Info("admin listening", "addr", addr, "timezone", tzName)
		if err := httpSrv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			slog.Error("listen", "err", err)
			os.Exit(1)
		}
	}()

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)
	<-stop
	slog.Info("shutting down")
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer shutdownCancel()
	_ = httpSrv.Shutdown(shutdownCtx)
}

func waitForDB(st *store.Store, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		err := st.Ping(ctx)
		cancel()
		if err == nil {
			return nil
		}
		if time.Now().After(deadline) {
			return err
		}
		time.Sleep(2 * time.Second)
	}
}

func dsn() string {
	if v := os.Getenv("DATABASE_URL"); v != "" {
		return v
	}
	u := &url.URL{
		Scheme: "postgres",
		User:   url.UserPassword(env("PGUSER", env("POSTGRES_USER", "hotel")), env("PGPASSWORD", os.Getenv("POSTGRES_PASSWORD"))),
		Host:   net.JoinHostPort(env("PGHOST", "postgres"), env("PGPORT", "5432")),
		Path:   "/" + env("PGDATABASE", env("POSTGRES_DB", "hotel_wifi")),
	}
	q := url.Values{}
	q.Set("sslmode", env("PGSSLMODE", "disable"))
	u.RawQuery = q.Encode()
	return u.String()
}

func env(key, def string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return def
}

func envBool(key string, def bool) bool {
	v := strings.ToLower(strings.TrimSpace(os.Getenv(key)))
	switch v {
	case "1", "true", "yes", "on":
		return true
	case "0", "false", "no", "off":
		return false
	default:
		return def
	}
}

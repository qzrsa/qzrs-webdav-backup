// Command qzrs-webdav-backup is a lightweight backup appliance for OpenWrt-class
// routers: it archives local directories and pushes them to any WebDAV server,
// and can pull them back again. Everything is managed from a built-in web
// console.
package main

import (
	"context"
	"embed"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/qzrsa/qzrs-webdav-backup/internal/api"
	"github.com/qzrsa/qzrs-webdav-backup/internal/config"
	"github.com/qzrsa/qzrs-webdav-backup/internal/cryptoutil"
	"github.com/qzrsa/qzrs-webdav-backup/internal/engine"
	"github.com/qzrsa/qzrs-webdav-backup/internal/scheduler"
)

// Version is overridden at build time.
var Version = "dev"

//go:embed all:web
var embeddedWeb embed.FS

func main() {
	var (
		dataDir     = flag.String("data", defaultDataDir(), "configuration and state directory")
		listen      = flag.String("listen", "", "listen address, e.g. 0.0.0.0:8787 (overrides the config file)")
		logFile     = flag.String("log-file", "", "also append logs to this file")
		showVersion = flag.Bool("version", false, "print the version and exit")
		setPassword = flag.String("set-password", "", "set the admin password, save it and exit")
		genPassword = flag.Bool("reset-password", false, "generate a new random admin password, save it and exit")
		trustProxy  = flag.Bool("trust-proxy", false, "trust X-Forwarded-For (only behind a reverse proxy you control)")
		// Diagnostic mode: probe a WebDAV endpoint and exit. Password may also
		// come from WDB_DAV_PASSWORD to keep it out of the shell history.
		davProbe    = flag.String("dav-probe", "", "probe a WebDAV URL step by step and exit")
		davUser     = flag.String("dav-user", "", "username for -dav-probe")
		davPass     = flag.String("dav-pass", "", "password for -dav-probe (or set WDB_DAV_PASSWORD)")
		davDir      = flag.String("dav-dir", "", "optional remote subdirectory to list during -dav-probe")
		davInsecure = flag.Bool("dav-insecure", false, "skip TLS verification during -dav-probe")
		davTimeout  = flag.Int("dav-timeout", 20, "per-request timeout in seconds for -dav-probe")
	)
	flag.Parse()

	if *showVersion {
		fmt.Printf("qzrs-webdav-backup %s\n", Version)
		return
	}

	// Probe before touching the config store: it must work even when the state
	// directory is broken, which is one of the reasons to run it.
	if *davProbe != "" {
		os.Exit(runDAVProbe(*davProbe, *davUser, *davPass, *davDir, *davInsecure, *davTimeout))
	}

	logger, closeLog, err := newLogger(*logFile)
	if err != nil {
		fmt.Fprintf(os.Stderr, "cannot initialise logging: %v\n", err)
		os.Exit(1)
	}
	defer closeLog()

	configPath := filepath.Join(*dataDir, "config.json")
	store, created, err := config.NewStore(configPath)
	if err != nil {
		fatal(logger, "cannot load configuration: %v", err)
	}

	// --- password management subcommands ----------------------------------
	if *setPassword != "" || *genPassword {
		pw := *setPassword
		if *genPassword {
			pw = generatePassword(16)
		}
		if len(pw) < 5 {
			fatal(logger, "password must be at least 5 characters")
		}
		hash, err := cryptoutil.HashPassword(pw)
		if err != nil {
			fatal(logger, "cannot hash password: %v", err)
		}
		if _, err := store.Update(func(c *config.Config) error {
			c.Admin.PasswordHash = hash
			return nil
		}); err != nil {
			fatal(logger, "cannot save password: %v", err)
		}
		fmt.Printf("admin password updated.\n  username: %s\n  password: %s\n",
			store.Snapshot().Admin.Username, pw)
		return
	}

	cfg := store.Snapshot()
	if *listen != "" {
		if _, err := store.Update(func(c *config.Config) error {
			c.Listen = *listen
			return nil
		}); err != nil {
			fatal(logger, "cannot apply listen override: %v", err)
		}
		cfg = store.Snapshot()
	}
	if *trustProxy {
		if _, err := store.Update(func(c *config.Config) error {
			c.TrustedProxy = true
			return nil
		}); err != nil {
			fatal(logger, "cannot apply trust-proxy flag: %v", err)
		}
		cfg = store.Snapshot()
	}

	// --- first-run bootstrap ----------------------------------------------
	// Default credentials are admin/admin so a fresh LAN install is
	// immediately reachable without copying a one-time secret. The banner
	// still asks the operator to change it from the web console.
	if created || cfg.Admin.PasswordHash == "" {
		pw := "admin"
		hash, err := cryptoutil.HashPassword(pw)
		if err != nil {
			fatal(logger, "cannot hash default password: %v", err)
		}
		if _, err := store.Update(func(c *config.Config) error {
			c.Admin.PasswordHash = hash
			return nil
		}); err != nil {
			fatal(logger, "cannot store default password: %v", err)
		}
		printBanner(pw, cfg, logger)
	}

	// --- wire the application ---------------------------------------------
	stateDir := filepath.Join(cfg.DataDir, "state")
	history, err := engine.NewHistory(stateDir, cfg.HistoryKeep)
	if err != nil {
		fatal(logger, "cannot open history store: %v", err)
	}

	runner := engine.NewRunner(store, history, func(format string, args ...any) {
		logger.Printf(format, args...)
	})

	sched := scheduler.New(runner, store, func(format string, args ...any) {
		logger.Printf(format, args...)
	})
	if err := sched.Start(); err != nil {
		logger.Printf("scheduler did not start cleanly: %v", err)
	}
	defer sched.Stop()

	webFS, err := fs.Sub(embeddedWeb, "web")
	if err != nil {
		fatal(logger, "embedded console is missing: %v", err)
	}

	server := api.New(api.Deps{
		Store:      store,
		Runner:     runner,
		Scheduler:  sched,
		Web:        webFS,
		Logf:       func(format string, args ...any) { logger.Printf(format, args...) },
		TrustProxy: store.Snapshot().TrustedProxy,
		StartedAt:  time.Now(),
	})
	api.Version = Version

	httpServer := &http.Server{
		Addr:    store.Snapshot().Listen,
		Handler: server.Handler(),
		// Read/write timeouts are generous because a restore streams a file
		// through, but unlimited idle connections are not wanted on a router.
		ReadHeaderTimeout: 15 * time.Second,
		IdleTimeout:       120 * time.Second,
		ErrorLog:          logger,
	}

	errc := make(chan error, 1)
	go func() {
		logger.Printf("qzrs-webdav-backup %s listening on %s", Version, httpServer.Addr)
		logger.Printf("console: http://%s/", displayAddr(httpServer.Addr))
		logger.Printf("state directory: %s", cfg.DataDir)
		if err := httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			errc <- err
		}
	}()

	sigc := make(chan os.Signal, 1)
	signal.Notify(sigc, syscall.SIGINT, syscall.SIGTERM)

	select {
	case err := <-errc:
		logger.Printf("server error: %v", err)
		os.Exit(1)
	case sig := <-sigc:
		logger.Printf("received %s, shutting down", sig)
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if err := httpServer.Shutdown(shutdownCtx); err != nil {
		logger.Printf("graceful shutdown failed: %v", err)
	}
	logger.Printf("bye")
}

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

func defaultDataDir() string {
	if env := os.Getenv("QZRS_WEBDAV_BACKUP_DATA"); env != "" {
		return env
	}
	// Legacy variable kept so pre-rename scripts keep working.
	if env := os.Getenv("WEBDAV_BACKUP_DATA"); env != "" {
		return env
	}
	// Prefer a persistent location on embedded systems; /etc survives reboots
	// on OpenWrt because the overlay is writable.
	return "/etc/qzrs-webdav-backup"
}

func newLogger(path string) (*log.Logger, func(), error) {
	writers := []io.Writer{os.Stdout}
	closer := func() {}
	if path != "" {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return nil, nil, err
		}
		f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
		if err != nil {
			return nil, nil, err
		}
		writers = append(writers, f)
		closer = func() { _ = f.Close() }
	}
	return log.New(io.MultiWriter(writers...), "", log.LstdFlags), closer, nil
}

func fatal(logger *log.Logger, format string, args ...any) {
	logger.Printf(format, args...)
	os.Exit(1)
}

func displayAddr(addr string) string {
	host, port, err := splitHostPort(addr)
	if err != nil {
		return addr
	}
	if host == "" || host == "0.0.0.0" || host == "::" {
		host = "<this-device-ip>"
	}
	return host + ":" + port
}

func splitHostPort(addr string) (string, string, error) {
	i := strings.LastIndex(addr, ":")
	if i < 0 {
		return addr, "8787", nil
	}
	return addr[:i], addr[i+1:], nil
}

// generatePassword produces a readable random password, avoiding characters
// that are easily confused when copied off a terminal.
func generatePassword(n int) string {
	const alphabet = "abcdefghijkmnpqrstuvwxyzABCDEFGHJKLMNPQRSTUVWXYZ23456789"
	raw := cryptoutil.RandomBytes(n)
	out := make([]byte, n)
	for i := range out {
		out[i] = alphabet[int(raw[i])%len(alphabet)]
	}
	return string(out)
}

// printBanner shows the one-time generated password. It goes to stdout *and*
// the log file, because the OpenWrt installer greps the log to display the
// credentials to the operator after a fresh install.
func printBanner(password string, cfg *config.Config, logger *log.Logger) {
	sep := "=========================================================="
	banner := "\n" + sep + "\n" +
		"  qzrs-webdav-backup 已初始化\n" +
		sep + "\n\n" +
		"  已创建默认管理账号，请使用它登录后立即修改密码：\n\n" +
		"      用户名: " + cfg.Admin.Username + "\n" +
		"      密  码: " + password + "（默认值，登录后请在「设置」中修改）\n\n" +
		sep + "\n"
	fmt.Print(banner)
	logger.Print(banner)
}

package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"sync"
	"syscall"
	"time"

	"github.com/nabu-sh/nabu/internal/api"
	"github.com/nabu-sh/nabu/internal/automation"
	"github.com/nabu-sh/nabu/internal/config"
	"github.com/nabu-sh/nabu/internal/credentials"
	"github.com/nabu-sh/nabu/internal/eventbus"
	"github.com/nabu-sh/nabu/internal/logging"
	"github.com/nabu-sh/nabu/internal/operator"
	"github.com/nabu-sh/nabu/internal/runner"
	"github.com/nabu-sh/nabu/internal/store"
	"github.com/nabu-sh/nabu/webassets"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "nabud:", err)
		os.Exit(1)
	}
}

func run() error {
	paths, err := config.Ensure()
	if err != nil {
		return err
	}
	logger, closeLog, err := newLogger(paths.Logs)
	if err != nil {
		return err
	}
	defer closeLog()
	pathsOnly, err := config.Resolve()
	if err != nil {
		return err
	}
	address := "127.0.0.1:7777"
	// Resolve a configured address without mutating recovery state. The default
	// is used when the database has not been initialized yet.
	if existing, openErr := store.Open(pathsOnly.Database); openErr == nil {
		if configured, settingsErr := existing.GetSettings(context.Background()); settingsErr == nil && configured.ServerAddress != "" {
			address = configured.ServerAddress
		}
		_ = existing.Close()
	}
	if err := validateLocalAddress(address); err != nil {
		return err
	}
	listener, err := net.Listen("tcp", address)
	if err != nil {
		return fmt.Errorf("another Nabu daemon may already be running on %s: %w", address, err)
	}
	defer listener.Close()

	database, err := store.Open(paths.Database)
	if err != nil {
		return err
	}
	defer database.Close()
	recovery, err := database.RecoverInterrupted(context.Background())
	if err != nil {
		return err
	}
	if recovery.TasksInterrupted > 0 || recovery.RunsInterrupted > 0 ||
		recovery.ScriptRunsInterrupted > 0 || recovery.ScheduleClaimsReleased > 0 || recovery.MessagesRequeued > 0 {
		logger.Warn("recovered interrupted work", "tasks", recovery.TasksInterrupted, "runs", recovery.RunsInterrupted,
			"script_runs", recovery.ScriptRunsInterrupted, "schedule_claims", recovery.ScheduleClaimsReleased,
			"chat_messages", recovery.MessagesRequeued)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	bus := eventbus.New()
	defer bus.Close()
	credentialBackend := credentials.NewPlatform()
	operatorService := operator.NewWithIntegrations(database, runner.NewSupervisor(runner.DefaultConfig()), paths, bus, logger, credentialBackend, nil, nil)
	if reportsCreated, reconcileErr := operatorService.ReconcileReports(context.Background()); reconcileErr != nil {
		logger.Warn("report reconciliation was incomplete", "created", reportsCreated, "error", reconcileErr)
	} else if reportsCreated > 0 {
		logger.Info("recovered durable reports", "created", reportsCreated)
	}
	operatorService.Start(ctx)
	automationEngine, err := automation.New(automation.Options{
		Store: database, ScriptsRoot: paths.Scripts, RunsRoot: paths.Runs,
		Credentials: credentialBackend,
		Logf:        func(message string, values ...any) { logger.Error(fmt.Sprintf(message, values...)) },
	})
	if err != nil {
		return err
	}
	operatorService.SetAutomation(automationEngine)
	var background sync.WaitGroup
	background.Add(1)
	go func() {
		defer background.Done()
		if err := automationEngine.Start(ctx); err != nil && ctx.Err() == nil {
			logger.Error("automation engine stopped", "error", err)
		}
	}()
	background.Add(1)
	go func() {
		defer background.Done()
		backupLoop(ctx, database, paths.Backups, logger)
	}()
	handler := api.New(operatorService, webassets.FS(), logger).Handler()
	server := &http.Server{
		Addr: address, Handler: handler, ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout: 15 * time.Second, WriteTimeout: 0, IdleTimeout: 75 * time.Second,
	}

	serverErrors := make(chan error, 1)
	go func() {
		logger.Info("Nabu is listening", "address", "http://"+address)
		serverErrors <- server.Serve(listener)
	}()

	select {
	case <-ctx.Done():
		shutdown, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		shutdownErr := server.Shutdown(shutdown)
		if err := operatorService.Stop(shutdown); err != nil {
			logger.Warn("operator did not stop before shutdown deadline", "error", err)
		}
		if !waitForBackground(&background, 10*time.Second) {
			logger.Warn("background services did not stop before shutdown deadline")
		}
		if shutdownErr != nil {
			return fmt.Errorf("shutdown HTTP server: %w", shutdownErr)
		}
		logger.Info("Nabu stopped cleanly")
		return nil
	case err := <-serverErrors:
		stop()
		shutdown, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if stopErr := operatorService.Stop(shutdown); stopErr != nil {
			logger.Warn("operator did not stop after HTTP server exit", "error", stopErr)
		}
		if !waitForBackground(&background, 10*time.Second) {
			logger.Warn("background services did not stop after HTTP server exit")
		}
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return fmt.Errorf("serve %s: %w", address, err)
	}
}

func backupLoop(ctx context.Context, database *store.Store, directory string, logger *slog.Logger) {
	backup := func() {
		today := time.Now().UTC().Format("2006-01-02")
		path := filepath.Join(directory, "nabu-"+today+".db")
		if _, err := os.Stat(path); err == nil {
			return
		}
		backupContext, cancel := context.WithTimeout(ctx, 2*time.Minute)
		defer cancel()
		if err := database.Backup(backupContext, path); err != nil {
			logger.Error("daily database backup failed", "error", err)
			return
		}
		logger.Info("daily database backup completed", "path", path)
	}
	backup()
	ticker := time.NewTicker(6 * time.Hour)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			backup()
		}
	}
}

func waitForBackground(group *sync.WaitGroup, timeout time.Duration) bool {
	done := make(chan struct{})
	go func() {
		group.Wait()
		close(done)
	}()
	select {
	case <-done:
		return true
	case <-time.After(timeout):
		return false
	}
}

func validateLocalAddress(address string) error {
	host := address
	for index := len(address) - 1; index >= 0; index-- {
		if address[index] == ':' {
			host = address[:index]
			break
		}
	}
	host = trimBrackets(host)
	if host != "127.0.0.1" && host != "localhost" && host != "::1" {
		return fmt.Errorf("refusing non-local server address %q; Nabu must bind to loopback", address)
	}
	return nil
}

func trimBrackets(value string) string {
	if len(value) >= 2 && value[0] == '[' && value[len(value)-1] == ']' {
		return value[1 : len(value)-1]
	}
	return value
}

func newLogger(logDirectory string) (*slog.Logger, func(), error) {
	path := filepath.Join(logDirectory, "nabud.log")
	file, err := logging.Open(path, logging.DefaultMaxBytes, logging.DefaultBackups)
	if err != nil {
		return nil, nil, fmt.Errorf("open daemon log: %w", err)
	}
	writer := io.MultiWriter(os.Stderr, file)
	logger := slog.New(slog.NewJSONHandler(writer, &slog.HandlerOptions{Level: slog.LevelInfo}))
	return logger, func() { _ = file.Close() }, nil
}

package main

import (
	"context"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"dockge-updater/internal/app"
	webui "dockge-updater/web"
)

var version = "dev"

func main() {
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: logLevel()}))
	listenAddress := env("LISTEN_ADDR", ":8080")
	stacksDirectory := env("STACKS_DIR", "/opt/stacks")
	dataDirectory := env("DATA_DIR", "/data")

	docker := app.NewDocker(app.ExecRunner{})
	validateContext, cancelValidation := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancelValidation()
	if err := docker.Validate(validateContext); err != nil {
		logger.Error("startup check failed", "error", err)
		os.Exit(1)
	}

	store := app.NewStore(filepath.Join(dataDirectory, "state.json"))
	manager, err := app.NewManager(stacksDirectory, docker, store, version, logger)
	if err != nil {
		logger.Error("initialize application", "error", err)
		os.Exit(1)
	}

	rootContext, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	go manager.RunScheduler(rootContext)

	server := &http.Server{
		Addr:              listenAddress,
		Handler:           app.NewServer(manager, webui.Files, logger),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	go func() {
		logger.Info("dockge updater started", "address", listenAddress, "stacks", stacksDirectory)
		if err := server.ListenAndServe(); err != nil && !app.IsServerClosed(err) {
			logger.Error("http server stopped", "error", err)
			stop()
		}
	}()

	<-rootContext.Done()
	shutdownContext, cancelShutdown := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancelShutdown()
	if err := server.Shutdown(shutdownContext); err != nil {
		logger.Error("graceful shutdown", "error", err)
	}
}

func env(name, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}

func logLevel() slog.Level {
	if os.Getenv("LOG_LEVEL") == "debug" {
		return slog.LevelDebug
	}
	return slog.LevelInfo
}

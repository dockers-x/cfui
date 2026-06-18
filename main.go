package main

import (
	"cfui/internal/cloudflared"
	"cfui/internal/config"
	"cfui/internal/logger"
	"cfui/internal/server"
	"cfui/internal/service"
	"context"
	"embed"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"syscall"
	"time"

	"cfui/version"
)

//go:embed all:web/dist
var assets embed.FS

//go:embed locales/*
var locales embed.FS

func main() {
	// Defer panic recovery and logger sync at the very start
	defer func() {
		if r := recover(); r != nil {
			if logger.Sugar != nil {
				logger.Sugar.Errorf("Fatal panic in main: %v", r)
				logger.Shutdown()
			} else {
				log.Printf("Fatal panic in main (logger not initialized): %v", r)
			}
			os.Exit(1)
		} else {
			// Normal shutdown - shutdown logger properly
			logger.Shutdown()
		}
	}()

	// Setup Config
	configDir := os.Getenv("DATA_DIR")
	if configDir == "" {
		configDir = "./data"
	}

	// Initialize logger first
	// Support separate LOG_DIR for Docker volume mounting
	// In Docker: /app/logs (can be mounted separately)
	// Local dev: ./data/logs
	logDir := os.Getenv("LOG_DIR")
	if logDir == "" {
		logDir = filepath.Join(configDir, "logs")
	}

	logConfig := &logger.Config{
		LogDir:     logDir,
		MaxSize:    100,  // 100 MB
		MaxBackups: 10,   // keep 10 backups
		MaxAge:     30,   // 30 days
		Compress:   true, // compress old logs
		LogLevel:   os.Getenv("LOG_LEVEL"),
	}
	if logConfig.LogLevel == "" {
		logConfig.LogLevel = "info"
	}

	if err := logger.Initialize(logConfig); err != nil {
		log.Fatalf("Failed to initialize logger: %v", err)
	}

	logger.Sugar.Infof("Starting Cloudflared Web Controller %s", version.GetFullVersion())
	logger.Sugar.Infof("Data directory: %s", configDir)
	logger.Sugar.Infof("Log directory: %s", logConfig.LogDir)
	runModeSelection := config.RunModeFromEnv()
	if runModeSelection.InvalidRaw != "" {
		logger.Sugar.Warnf("Invalid CFUI_RUN_MODE %q; falling back to %s", runModeSelection.InvalidRaw, runModeSelection.Mode)
	}
	logger.Sugar.Infof("Run mode: %s", runModeSelection.Mode)

	cfgMgr, err := config.NewManager(configDir)
	if err != nil {
		logger.Sugar.Errorf("Failed to init config: %v", err)
		log.Fatalf("Failed to init config: %v", err)
	}
	logger.Sugar.Info("Configuration manager initialized")

	runner := service.NewRunner(cfgMgr)

	// Claim SIGTERM/SIGINT before any tunnel can start: the embedded
	// cloudflared installs its own signal handlers per tunnel run, and with
	// several runs they crash the process on shutdown (double close of the
	// shared shutdown channel). cfui owns process signals exclusively;
	// internal/cloudflared keeps re-asserting this registration.
	shutdown := make(chan os.Signal, 1)
	cloudflared.OwnProcessSignals(shutdown, os.Interrupt, syscall.SIGTERM)

	if runModeSelection.Mode.AutoStartsLocalRunner() {
		runner.Initialize()
		logger.Sugar.Info("Tunnel runner initialized")
	} else {
		logger.Sugar.Info("Tunnel runner auto-start skipped in oauth mode")
	}

	// Setup Server
	srv := server.NewServerWithMode(cfgMgr, runner, assets, locales, runModeSelection.Mode)

	// Start DDNS service if enabled
	srv.StartDDNS()
	logger.Sugar.Info("DDNS service check complete")
	srv.StartS3WebDAV()
	logger.Sugar.Info("S3 WebDAV service check complete")
	// Bind Host
	bindHost := os.Getenv("BIND_HOST")
	if bindHost == "" {
		bindHost = "0.0.0.0"
	}
	// Run
	port := os.Getenv("PORT")
	if port == "" {
		port = "14333"
	}

	serveAddr := fmt.Sprintf("%s:%s", bindHost, port)

	fmt.Printf("Cloudflared Web Controller %s\n", version.GetFullVersion())
	fmt.Printf("Run mode: %s\n", runModeSelection.Mode)
	fmt.Printf("Server listening on %s\n", serveAddr)
	fmt.Printf("Local access: http://localhost:%s\n", port)
	fmt.Printf("Network access: http://<your-ip>:%s\n", port)
	logger.Sugar.Infof("Server starting on %s", serveAddr)

	// Create HTTP server with explicit configuration.
	// WriteTimeout stays unset because /api/logs/stream keeps an SSE
	// response open indefinitely.
	httpServer := &http.Server{
		Addr:              serveAddr,
		Handler:           srv.GetHandler(),
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       2 * time.Minute,
	}

	// Channel to signal when server has shut down
	serverErrors := make(chan error, 1)

	// Start server in goroutine
	go func() {
		serverErrors <- httpServer.ListenAndServe()
	}()

	// Block until we receive a signal or server error
	select {
	case sig := <-shutdown:
		logger.Sugar.Infof("Received shutdown signal: %v", sig)

		// Create context with timeout for shutdown
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()

		// Shutdown HTTP server gracefully. Close long-lived SSE streams
		// first so Shutdown doesn't stall until its timeout.
		logger.Sugar.Info("Shutting down HTTP server...")
		srv.PrepareShutdown()
		if err := httpServer.Shutdown(ctx); err != nil {
			logger.Sugar.Errorf("HTTP server shutdown error: %v", err)
			httpServer.Close()
		}

		// Stop DDNS service
		srv.StopDDNS()
		if err := srv.StopS3WebDAV(ctx); err != nil {
			logger.Sugar.Errorf("S3 WebDAV server shutdown error: %v", err)
		}

		// Shutdown runner (stops tunnel if running)
		if err := runner.Shutdown(); err != nil {
			logger.Sugar.Errorf("Runner shutdown error: %v", err)
		}

		logger.Sugar.Info("Graceful shutdown complete")

	case err := <-serverErrors:
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Sugar.Errorf("Server failed: %v", err)
			log.Fatal(err)
		}
	}
}

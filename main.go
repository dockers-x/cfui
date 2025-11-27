package main

import (
	"embed"
	"fmt"
	"log"
	"os"
	"path/filepath"

	"cfui/config"
	"cfui/logger"
	"cfui/server"
	"cfui/service"
	"cfui/version"
)

//go:embed web/dist/*
var assets embed.FS

//go:embed locales/*
var locales embed.FS

func main() {
	// Defer panic recovery and logger sync at the very start
	defer func() {
		if r := recover(); r != nil {
			if logger.Sugar != nil {
				logger.Sugar.Errorf("Fatal panic in main: %v", r)
				logger.Sync()
			} else {
				log.Printf("Fatal panic in main (logger not initialized): %v", r)
			}
			os.Exit(1)
		} else {
			// Normal shutdown - sync logs
			logger.Sync()
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

	cfgMgr, err := config.NewManager(configDir)
	if err != nil {
		logger.Sugar.Errorf("Failed to init config: %v", err)
		log.Fatalf("Failed to init config: %v", err)
	}
	logger.Sugar.Info("Configuration manager initialized")

	runner := service.NewRunner(cfgMgr)
	runner.Initialize()
	logger.Sugar.Info("Tunnel runner initialized")

	// Setup Server
	srv := server.NewServer(cfgMgr, runner, assets, locales)

	// Run
	port := os.Getenv("PORT")
	if port == "" {
		port = "14333"
	}

	fmt.Printf("Cloudflared Web Controller %s\n", version.GetFullVersion())
	fmt.Printf("Server listening on 0.0.0.0:%s\n", port)
	fmt.Printf("Local access: http://localhost:%s\n", port)
	fmt.Printf("Network access: http://<your-ip>:%s\n", port)
	logger.Sugar.Infof("Server starting on 0.0.0.0:%s", port)

	if err := srv.Run(":" + port); err != nil {
		logger.Sugar.Errorf("Server failed: %v", err)
		log.Fatal(err)
	}
}

package main

import (
	"embed"
	"fmt"
	"log"
	"os"
	"path/filepath"

	"cfui/config"
	"cfui/server"
	"cfui/service"
)

// Version is set during build time via ldflags
var Version = "dev"

//go:embed web/dist/*
var assets embed.FS

//go:embed locales/*
var locales embed.FS

func main() {
	// Setup Config
	configDir := os.Getenv("DATA_DIR")
	if configDir == "" {
		home, _ := os.UserHomeDir()
		configDir = filepath.Join(home, ".cloudflared-web")
	}

	cfgMgr, err := config.NewManager(configDir)
	if err != nil {
		log.Fatalf("Failed to init config: %v", err)
	}

	runner := service.NewRunner(cfgMgr)
	runner.Initialize()

	// Setup Server
	srv := server.NewServer(cfgMgr, runner, assets, locales)

	// Run
	port := os.Getenv("PORT")
	if port == "" {
		port = "14333"
	}

	fmt.Printf("Cloudflared Web Controller %s\n", Version)
	fmt.Printf("Server listening on 0.0.0.0:%s\n", port)
	fmt.Printf("Local access: http://localhost:%s\n", port)
	fmt.Printf("Network access: http://<your-ip>:%s\n", port)
	if err := srv.Run(":" + port); err != nil {
		log.Fatal(err)
	}
}

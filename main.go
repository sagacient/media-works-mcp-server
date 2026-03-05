// SPDX-License-Identifier: MPL-2.0
// Copyright 2026 Sagacient <sagacient@gmail.com>
//
// See CONTRIBUTORS.md for full contributor list.

// Media Works MCP Server — Docker-based media processing via MCP.
package main

import (
	"context"
	"log"
	"os"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"github.com/sagacient/media-works-mcp-server/config"
	"github.com/sagacient/media-works-mcp-server/executor"
	"github.com/sagacient/media-works-mcp-server/httpserver"
	"github.com/sagacient/media-works-mcp-server/scanner"
	"github.com/sagacient/media-works-mcp-server/storage"
	"github.com/sagacient/media-works-mcp-server/tools"
	"github.com/sagacient/media-works-mcp-server/workerpool"
)

func main() {
	// Load configuration
	cfg := config.DefaultConfig()
	cfg.LoadFromEnv()

	if err := cfg.Validate(); err != nil {
		log.Fatalf("Invalid configuration: %v", err)
	}

	// Log to stderr to avoid interfering with stdio MCP transport
	log.SetOutput(os.Stderr)
	cfg.LogConfig()

	// Initialize worker pool
	pool := workerpool.NewPool(cfg.MaxWorkers, cfg.AcquireTimeout)
	log.Printf("Worker pool initialized: %d workers", cfg.MaxWorkers)

	// Initialize malware scanner
	sc := scanner.NewScanner(scanner.Config{
		Enabled:     cfg.ScanUploads,
		FailOpen:    cfg.ScanOnFail == "allow",
		ClamdSocket: cfg.ClamdSocket,
	})

	// Initialize file store
	fileStore, err := storage.NewFileStore(cfg.StorageDir, cfg.UploadTTL, cfg.MaxUploadSize, sc)
	if err != nil {
		log.Fatalf("Failed to initialize file store: %v", err)
	}
	defer fileStore.Stop()
	log.Printf("File store initialized: %s (TTL: %s)", cfg.StorageDir, cfg.UploadTTL)

	// Initialize output manager
	outputManager, err := executor.NewOutputManager(cfg.OutputDir, cfg.OutputTTL)
	if err != nil {
		log.Fatalf("Failed to initialize output manager: %v", err)
	}
	if outputManager != nil {
		defer outputManager.Stop()
		log.Printf("Output manager initialized: %s (TTL: %s)", cfg.OutputDir, cfg.OutputTTL)
	}

	// Initialize Docker executor
	dockerExec, err := executor.NewDockerExecutor(
		cfg.DockerImage,
		cfg.BuildLocal,
		cfg.NetworkDisabled,
		cfg.MaxMemoryMB,
		cfg.MaxCPUs,
		cfg.TempDir,
		outputManager,
	)
	if err != nil {
		log.Fatalf("Failed to initialize Docker executor: %v", err)
	}
	dockerExec.EnsureImageAsync()
	log.Printf("Docker executor initialized: image=%s", cfg.DockerImage)

	// Create MCP server
	mcpServer := server.NewMCPServer(
		"Media Works MCP Server",
		"1.0.0",
		server.WithResourceCapabilities(true, true),
		server.WithLogging(),
	)

	// Register tools
	mediaTools := tools.NewMediaTools(dockerExec, fileStore, pool, cfg.ExecutionTimeout)
	mediaTools.RegisterTools(mcpServer)
	log.Printf("MCP tools registered")

	// Start server based on transport
	switch cfg.Transport {
	case "http":
		log.Printf("Starting HTTP transport on port %d", cfg.HTTPPort)
		httpSrv := httpserver.NewServer(mcpServer, fileStore, cfg.HTTPPort)
		if err := httpSrv.Start(); err != nil {
			log.Fatalf("HTTP server failed: %v", err)
		}
	case "stdio":
		log.Printf("Starting stdio transport")
		stdioServer := server.NewStdioServer(mcpServer)
		if err := stdioServer.Listen(context.Background(), os.Stdin, os.Stdout); err != nil {
			log.Fatalf("Stdio server failed: %v", err)
		}
	default:
		log.Fatalf("Unknown transport: %s", cfg.Transport)
	}

	// Suppress unused import warning for mcp package
	_ = mcp.Tool{}
	_ = time.Now()
}

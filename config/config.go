// SPDX-License-Identifier: MPL-2.0
// Copyright 2026 Sagacient <sagacient@gmail.com>
//
// See CONTRIBUTORS.md for full contributor list.

// Package config provides configuration management for the Media Works MCP Server.
package config

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// Config holds all server configuration.
type Config struct {
	// Worker pool settings
	MaxWorkers     int
	AcquireTimeout time.Duration

	// Execution settings
	ExecutionTimeout time.Duration
	MaxMemoryMB      int64
	MaxCPUs          float64

	// Docker settings
	DockerImage    string
	BuildLocal     bool
	NetworkDisabled bool

	// Transport
	Transport string // "stdio" or "http"
	HTTPPort  int

	// Storage settings
	StorageDir    string
	UploadTTL     time.Duration
	MaxUploadSize int64

	// Malware scanning
	ScanUploads bool
	ScanOnFail  string // "reject" or "allow"
	ClamdSocket string

	// Temp and output directories
	TempDir   string
	OutputDir string
	OutputTTL time.Duration
}

// DefaultConfig returns the default configuration.
func DefaultConfig() *Config {
	homeDir, _ := os.UserHomeDir()
	cacheDir := filepath.Join(homeDir, ".cache", "media-works")

	return &Config{
		// Worker pool
		MaxWorkers:     5,
		AcquireTimeout: 30 * time.Second,

		// Execution
		ExecutionTimeout: 300 * time.Second,
		MaxMemoryMB:      1024,
		MaxCPUs:          2.0,

		// Docker
		DockerImage:    "sagacient/mediaworks:latest",
		BuildLocal:     false,
		NetworkDisabled: true,

		// Transport
		Transport: "stdio",
		HTTPPort:  8080,

		// Storage
		StorageDir:    filepath.Join(cacheDir, "uploads"),
		UploadTTL:     1 * time.Hour,
		MaxUploadSize: 500 * 1024 * 1024, // 500MB (media files are larger)

		// Scanning
		ScanUploads: false,
		ScanOnFail:  "reject",

		// Temp and output
		TempDir:   "",
		OutputDir: "",
		OutputTTL: 24 * time.Hour,
	}
}

// LoadFromEnv loads configuration from environment variables.
func (c *Config) LoadFromEnv() {
	// Worker pool
	if v := os.Getenv("MAX_WORKERS"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			c.MaxWorkers = n
		}
	}
	if v := os.Getenv("ACQUIRE_TIMEOUT"); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			c.AcquireTimeout = d
		}
	}

	// Execution
	if v := os.Getenv("EXECUTION_TIMEOUT"); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			c.ExecutionTimeout = d
		}
	}
	if v := os.Getenv("MAX_MEMORY_MB"); v != "" {
		if n, err := strconv.ParseInt(v, 10, 64); err == nil && n > 0 {
			c.MaxMemoryMB = n
		}
	}
	if v := os.Getenv("MAX_CPUS"); v != "" {
		if f, err := strconv.ParseFloat(v, 64); err == nil && f > 0 {
			c.MaxCPUs = f
		}
	}

	// Docker
	if v := os.Getenv("DOCKER_IMAGE"); v != "" {
		c.DockerImage = v
	}
	if v := os.Getenv("BUILD_LOCAL"); v != "" {
		c.BuildLocal = strings.EqualFold(v, "true") || v == "1"
	}
	if v := os.Getenv("NETWORK_DISABLED"); v != "" {
		c.NetworkDisabled = strings.EqualFold(v, "true") || v == "1"
	}

	// Transport
	if v := os.Getenv("TRANSPORT"); v != "" {
		c.Transport = strings.ToLower(v)
	}
	if v := os.Getenv("HTTP_PORT"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			c.HTTPPort = n
		}
	}

	// Storage
	if v := os.Getenv("STORAGE_DIR"); v != "" {
		c.StorageDir = v
	}
	if v := os.Getenv("UPLOAD_TTL"); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			c.UploadTTL = d
		}
	}
	if v := os.Getenv("MAX_UPLOAD_SIZE"); v != "" {
		if n, err := strconv.ParseInt(v, 10, 64); err == nil && n > 0 {
			c.MaxUploadSize = n
		}
	}

	// Scanning
	if v := os.Getenv("SCAN_UPLOADS"); v != "" {
		c.ScanUploads = strings.EqualFold(v, "true") || v == "1"
	}
	if v := os.Getenv("SCAN_ON_FAIL"); v != "" {
		c.ScanOnFail = strings.ToLower(v)
	}
	if v := os.Getenv("CLAMD_SOCKET"); v != "" {
		c.ClamdSocket = v
	}

	// Temp and output
	if v := os.Getenv("TEMP_DIR"); v != "" {
		c.TempDir = v
	}
	if v := os.Getenv("OUTPUT_DIR"); v != "" {
		c.OutputDir = v
	}
	if v := os.Getenv("OUTPUT_TTL"); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			c.OutputTTL = d
		}
	}
}

// Validate checks the configuration for errors.
func (c *Config) Validate() error {
	if c.MaxWorkers < 1 {
		return fmt.Errorf("MAX_WORKERS must be at least 1")
	}
	if c.Transport != "stdio" && c.Transport != "http" {
		return fmt.Errorf("TRANSPORT must be 'stdio' or 'http', got '%s'", c.Transport)
	}
	if c.ScanOnFail != "reject" && c.ScanOnFail != "allow" {
		return fmt.Errorf("SCAN_ON_FAIL must be 'reject' or 'allow', got '%s'", c.ScanOnFail)
	}
	return nil
}

// LogConfig logs the current configuration.
func (c *Config) LogConfig() {
	log.Printf("Configuration:")
	log.Printf("  Max Workers: %d", c.MaxWorkers)
	log.Printf("  Execution Timeout: %s", c.ExecutionTimeout)
	log.Printf("  Max Memory: %dMB", c.MaxMemoryMB)
	log.Printf("  Docker Image: %s", c.DockerImage)
	log.Printf("  Build Local: %v", c.BuildLocal)
	log.Printf("  Network Disabled: %v", c.NetworkDisabled)
	log.Printf("  Transport: %s", c.Transport)
	if c.Transport == "http" {
		log.Printf("  HTTP Port: %d", c.HTTPPort)
	}
	log.Printf("  Storage Dir: %s", c.StorageDir)
	log.Printf("  Upload TTL: %s", c.UploadTTL)
	log.Printf("  Max Upload Size: %d bytes", c.MaxUploadSize)
	log.Printf("  Scan Uploads: %v", c.ScanUploads)
	log.Printf("  Scan On Fail: %s", c.ScanOnFail)
	if c.OutputDir != "" {
		log.Printf("  Output Dir: %s", c.OutputDir)
		log.Printf("  Output TTL: %s", c.OutputTTL)
	}
}

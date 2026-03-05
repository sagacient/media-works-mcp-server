// SPDX-License-Identifier: MPL-2.0
// Copyright 2026 Sagacient <sagacient@gmail.com>
//
// See CONTRIBUTORS.md for full contributor list.

// Package executor provides Docker-based media processing execution.
package executor

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// OutputManager manages output directories for media processing executions.
type OutputManager struct {
	baseDir string
	ttl     time.Duration
	mu      sync.RWMutex
	stop    chan struct{}
}

// OutputFileInfo represents metadata about an output file.
type OutputFileInfo struct {
	Name string `json:"name"`
	Size int64  `json:"size"`
	Path string `json:"-"`
}

// ExecutionMetadata holds metadata about an execution's output.
type ExecutionMetadata struct {
	ExecutionID string    `json:"execution_id"`
	CreatedAt   time.Time `json:"created_at"`
	ExpiresAt   time.Time `json:"expires_at"`
	Tool        string    `json:"tool,omitempty"`
}

// NewOutputManager creates a new output manager.
func NewOutputManager(baseDir string, ttl time.Duration) (*OutputManager, error) {
	if baseDir == "" {
		return nil, nil
	}

	if err := os.MkdirAll(baseDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create output directory: %w", err)
	}

	m := &OutputManager{
		baseDir: baseDir,
		ttl:     ttl,
		stop:    make(chan struct{}),
	}

	go m.cleanupLoop()

	return m, nil
}

// CreateExecutionDir creates a new execution-specific output directory.
func (m *OutputManager) CreateExecutionDir(execID string) (string, error) {
	if m == nil {
		return "", fmt.Errorf("output manager not configured")
	}

	execDir := filepath.Join(m.baseDir, execID)
	if err := os.MkdirAll(execDir, 0777); err != nil {
		return "", fmt.Errorf("failed to create execution directory: %w", err)
	}
	// Ensure permissions for Docker container access
	if err := os.Chmod(execDir, 0777); err != nil {
		log.Printf("Warning: failed to chmod execution dir: %v", err)
	}

	// Write metadata
	meta := ExecutionMetadata{
		ExecutionID: execID,
		CreatedAt:   time.Now(),
		ExpiresAt:   time.Now().Add(m.ttl),
	}
	metaPath := filepath.Join(execDir, ".metadata.json")
	metaData, _ := json.Marshal(meta)
	os.WriteFile(metaPath, metaData, 0644)

	return execDir, nil
}

// ListExecutions lists all execution directories with their files.
func (m *OutputManager) ListExecutions() ([]map[string]interface{}, error) {
	if m == nil {
		return nil, fmt.Errorf("output manager not configured")
	}

	entries, err := os.ReadDir(m.baseDir)
	if err != nil {
		return nil, fmt.Errorf("failed to read output directory: %w", err)
	}

	var executions []map[string]interface{}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}

		execDir := filepath.Join(m.baseDir, entry.Name())
		files, _ := m.ScanOutputFiles(execDir)

		// Read metadata if available
		meta := m.readMetadata(execDir)

		exec := map[string]interface{}{
			"execution_id": entry.Name(),
			"files":        files,
			"file_count":   len(files),
		}
		if meta != nil {
			exec["created_at"] = meta.CreatedAt
			exec["expires_at"] = meta.ExpiresAt
			exec["tool"] = meta.Tool
		}

		executions = append(executions, exec)
	}

	return executions, nil
}

// ScanOutputFiles scans a directory for output files.
func (m *OutputManager) ScanOutputFiles(dir string) ([]OutputFileInfo, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}

	var files []OutputFileInfo
	for _, entry := range entries {
		if entry.IsDir() || strings.HasPrefix(entry.Name(), ".") {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			continue
		}
		files = append(files, OutputFileInfo{
			Name: entry.Name(),
			Size: info.Size(),
			Path: filepath.Join(dir, entry.Name()),
		})
	}
	return files, nil
}

// GetFile reads the contents of an output file.
func (m *OutputManager) GetFile(execID, filename string) ([]byte, *OutputFileInfo, error) {
	if m == nil {
		return nil, nil, fmt.Errorf("output manager not configured")
	}

	// Prevent path traversal
	if strings.Contains(execID, "..") || strings.Contains(filename, "..") {
		return nil, nil, fmt.Errorf("invalid path")
	}

	filePath := filepath.Join(m.baseDir, execID, filename)

	// Verify the resolved path is within the base directory
	absPath, err := filepath.Abs(filePath)
	if err != nil {
		return nil, nil, fmt.Errorf("invalid path")
	}
	absBase, _ := filepath.Abs(m.baseDir)
	if !strings.HasPrefix(absPath, absBase) {
		return nil, nil, fmt.Errorf("path traversal detected")
	}

	info, err := os.Stat(filePath)
	if err != nil {
		return nil, nil, fmt.Errorf("file not found: %s/%s", execID, filename)
	}

	fileInfo := &OutputFileInfo{
		Name: filename,
		Size: info.Size(),
		Path: filePath,
	}

	// For text files, read contents
	if isTextFile(filename) {
		data, err := os.ReadFile(filePath)
		if err != nil {
			return nil, fileInfo, fmt.Errorf("failed to read file: %w", err)
		}
		return data, fileInfo, nil
	}

	// For binary files, return metadata only
	return nil, fileInfo, nil
}

// GetFileReader returns a reader for an output file (for binary downloads).
func (m *OutputManager) GetFileReader(execID, filename string) (io.ReadCloser, *OutputFileInfo, error) {
	if m == nil {
		return nil, nil, fmt.Errorf("output manager not configured")
	}

	if strings.Contains(execID, "..") || strings.Contains(filename, "..") {
		return nil, nil, fmt.Errorf("invalid path")
	}

	filePath := filepath.Join(m.baseDir, execID, filename)

	absPath, err := filepath.Abs(filePath)
	if err != nil {
		return nil, nil, fmt.Errorf("invalid path")
	}
	absBase, _ := filepath.Abs(m.baseDir)
	if !strings.HasPrefix(absPath, absBase) {
		return nil, nil, fmt.Errorf("path traversal detected")
	}

	info, err := os.Stat(filePath)
	if err != nil {
		return nil, nil, fmt.Errorf("file not found: %s/%s", execID, filename)
	}

	f, err := os.Open(filePath)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to open file: %w", err)
	}

	fileInfo := &OutputFileInfo{
		Name: filename,
		Size: info.Size(),
		Path: filePath,
	}

	return f, fileInfo, nil
}

// DeleteExecution removes an execution's output directory.
func (m *OutputManager) DeleteExecution(execID string) error {
	if m == nil {
		return fmt.Errorf("output manager not configured")
	}

	if strings.Contains(execID, "..") {
		return fmt.Errorf("invalid execution ID")
	}

	execDir := filepath.Join(m.baseDir, execID)
	if _, err := os.Stat(execDir); os.IsNotExist(err) {
		return fmt.Errorf("execution not found: %s", execID)
	}

	if err := os.RemoveAll(execDir); err != nil {
		return fmt.Errorf("failed to delete execution: %w", err)
	}

	log.Printf("Deleted execution output: %s", execID)
	return nil
}

// Stop stops the cleanup goroutine.
func (m *OutputManager) Stop() {
	if m != nil {
		close(m.stop)
	}
}

// readMetadata reads execution metadata from .metadata.json.
func (m *OutputManager) readMetadata(execDir string) *ExecutionMetadata {
	metaPath := filepath.Join(execDir, ".metadata.json")
	data, err := os.ReadFile(metaPath)
	if err != nil {
		return nil
	}
	var meta ExecutionMetadata
	if err := json.Unmarshal(data, &meta); err != nil {
		return nil
	}
	return &meta
}

// cleanupLoop periodically removes expired execution outputs.
func (m *OutputManager) cleanupLoop() {
	ticker := time.NewTicker(5 * time.Minute)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			m.cleanupExpired()
		case <-m.stop:
			return
		}
	}
}

// cleanupExpired removes expired execution directories.
func (m *OutputManager) cleanupExpired() {
	entries, err := os.ReadDir(m.baseDir)
	if err != nil {
		return
	}

	now := time.Now()
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}

		execDir := filepath.Join(m.baseDir, entry.Name())
		meta := m.readMetadata(execDir)

		if meta != nil && now.After(meta.ExpiresAt) {
			if err := os.RemoveAll(execDir); err != nil {
				log.Printf("Warning: failed to cleanup expired execution %s: %v", entry.Name(), err)
			} else {
				log.Printf("Cleaned up expired execution: %s", entry.Name())
			}
		}
	}
}

// isTextFile checks if a file is a text file based on extension.
func isTextFile(filename string) bool {
	ext := strings.ToLower(filepath.Ext(filename))
	textExts := map[string]bool{
		".txt": true, ".csv": true, ".json": true, ".xml": true,
		".html": true, ".md": true, ".log": true, ".yaml": true,
		".yml": true, ".srt": true, ".vtt": true, ".ass": true,
	}
	return textExts[ext]
}

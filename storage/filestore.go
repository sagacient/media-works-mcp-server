// SPDX-License-Identifier: MPL-2.0
// Copyright 2026 Sagacient <sagacient@gmail.com>
//
// See CONTRIBUTORS.md for full contributor list.

// Package storage provides file upload storage with TTL-based expiration and malware scanning.
package storage

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/sagacient/media-works-mcp-server/scanner"
)

// FileInfo represents metadata about an uploaded file.
type FileInfo struct {
	ID        string    `json:"id"`
	Filename  string    `json:"filename"`
	Size      int64     `json:"size"`
	UploadedAt time.Time `json:"uploaded_at"`
	ExpiresAt time.Time `json:"expires_at"`
	Path      string    `json:"-"` // Internal path, not exposed via JSON
}

// ErrMalwareDetected is returned when malware is found in an uploaded file.
type ErrMalwareDetected struct {
	Threat string
}

func (e *ErrMalwareDetected) Error() string {
	return fmt.Sprintf("malware detected: %s", e.Threat)
}

// ErrScannerUnavailable is returned when the malware scanner is not available.
type ErrScannerUnavailable struct{}

func (e *ErrScannerUnavailable) Error() string {
	return "malware scanner unavailable"
}

// FileStore manages uploaded files with TTL-based expiration.
type FileStore struct {
	baseDir    string
	ttl        time.Duration
	maxSize    int64
	scanner    *scanner.Scanner
	mu         sync.RWMutex
	files      map[string]*FileInfo
	stopCleanup chan struct{}
}

// NewFileStore creates a new file store.
func NewFileStore(baseDir string, ttl time.Duration, maxSize int64, sc *scanner.Scanner) (*FileStore, error) {
	if err := os.MkdirAll(baseDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create storage directory: %w", err)
	}

	fs := &FileStore{
		baseDir:     baseDir,
		ttl:         ttl,
		maxSize:     maxSize,
		scanner:     sc,
		files:       make(map[string]*FileInfo),
		stopCleanup: make(chan struct{}),
	}

	// Start cleanup goroutine
	go fs.cleanupLoop()

	return fs, nil
}

// Upload stores a file with malware scanning and TTL.
func (fs *FileStore) Upload(filename string, r io.Reader) (*FileInfo, error) {
	// Generate unique ID
	id, err := generateID()
	if err != nil {
		return nil, fmt.Errorf("failed to generate file ID: %w", err)
	}

	// Sanitize filename
	safeName := sanitizeFilename(filename)
	if safeName == "" {
		safeName = id
	}

	// Create file
	filePath := filepath.Join(fs.baseDir, id+"_"+safeName)
	f, err := os.Create(filePath)
	if err != nil {
		return nil, fmt.Errorf("failed to create file: %w", err)
	}

	// Write with size limit
	written, err := io.Copy(f, io.LimitReader(r, fs.maxSize+1))
	f.Close()
	if err != nil {
		os.Remove(filePath)
		return nil, fmt.Errorf("failed to write file: %w", err)
	}
	if written > fs.maxSize {
		os.Remove(filePath)
		return nil, fmt.Errorf("file exceeds maximum size of %d bytes", fs.maxSize)
	}

	// Scan for malware if scanner is configured
	if fs.scanner != nil && fs.scanner.IsEnabled() {
		result := fs.scanner.Scan(filePath)
		if result.Error != nil {
			os.Remove(filePath)
			return nil, &ErrScannerUnavailable{}
		}
		if !result.Clean {
			os.Remove(filePath)
			log.Printf("SECURITY: Rejected malware upload - file=%s, threat=%s", filename, result.Threat)
			return nil, &ErrMalwareDetected{Threat: result.Threat}
		}
	}

	now := time.Now()
	info := &FileInfo{
		ID:         id,
		Filename:   safeName,
		Size:       written,
		UploadedAt: now,
		ExpiresAt:  now.Add(fs.ttl),
		Path:       filePath,
	}

	fs.mu.Lock()
	fs.files[id] = info
	fs.mu.Unlock()

	log.Printf("File uploaded: id=%s, name=%s, size=%d", id, safeName, written)
	return info, nil
}

// GetPath returns the file path for a given ID, or false if not found/expired.
func (fs *FileStore) GetPath(id string) (string, bool) {
	fs.mu.RLock()
	defer fs.mu.RUnlock()

	info, ok := fs.files[id]
	if !ok || time.Now().After(info.ExpiresAt) {
		return "", false
	}
	return info.Path, true
}

// Get returns the FileInfo for a given ID, or nil if not found/expired.
func (fs *FileStore) Get(id string) *FileInfo {
	fs.mu.RLock()
	defer fs.mu.RUnlock()

	info, ok := fs.files[id]
	if !ok || time.Now().After(info.ExpiresAt) {
		return nil
	}
	return info
}

// List returns all non-expired files.
func (fs *FileStore) List() []*FileInfo {
	fs.mu.RLock()
	defer fs.mu.RUnlock()

	now := time.Now()
	var result []*FileInfo
	for _, info := range fs.files {
		if now.Before(info.ExpiresAt) {
			result = append(result, info)
		}
	}
	return result
}

// Delete removes a file by ID.
func (fs *FileStore) Delete(id string) error {
	fs.mu.Lock()
	info, ok := fs.files[id]
	if !ok {
		fs.mu.Unlock()
		return fmt.Errorf("file not found: %s", id)
	}
	delete(fs.files, id)
	fs.mu.Unlock()

	if err := os.Remove(info.Path); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("failed to delete file: %w", err)
	}
	log.Printf("File deleted: id=%s, name=%s", id, info.Filename)
	return nil
}

// ResolveUploadURI resolves an upload:// URI to the actual filesystem path.
func (fs *FileStore) ResolveUploadURI(uri string) (string, error) {
	if !strings.HasPrefix(uri, "upload://") {
		return uri, nil
	}
	id := strings.TrimPrefix(uri, "upload://")
	path, ok := fs.GetPath(id)
	if !ok {
		return "", fmt.Errorf("uploaded file not found or expired: %s", id)
	}
	return path, nil
}

// Stop stops the cleanup goroutine.
func (fs *FileStore) Stop() {
	close(fs.stopCleanup)
}

// cleanupLoop periodically removes expired files.
func (fs *FileStore) cleanupLoop() {
	ticker := time.NewTicker(1 * time.Minute)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			fs.cleanupExpired()
		case <-fs.stopCleanup:
			return
		}
	}
}

// cleanupExpired removes expired files from the store.
func (fs *FileStore) cleanupExpired() {
	now := time.Now()

	fs.mu.Lock()
	var expired []string
	for id, info := range fs.files {
		if now.After(info.ExpiresAt) {
			expired = append(expired, id)
		}
	}

	for _, id := range expired {
		info := fs.files[id]
		delete(fs.files, id)
		if err := os.Remove(info.Path); err != nil && !os.IsNotExist(err) {
			log.Printf("Warning: failed to cleanup expired file %s: %v", info.Path, err)
		} else {
			log.Printf("Cleaned up expired file: id=%s, name=%s", id, info.Filename)
		}
	}
	fs.mu.Unlock()
}

// generateID generates a random hex ID.
func generateID() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

// sanitizeFilename removes dangerous characters from filenames.
func sanitizeFilename(name string) string {
	// Get base name (remove path components)
	name = filepath.Base(name)

	// Remove null bytes
	name = strings.ReplaceAll(name, "\x00", "")

	// Remove path separators
	name = strings.ReplaceAll(name, "/", "")
	name = strings.ReplaceAll(name, "\\", "")

	// Remove leading dots (hidden files)
	name = strings.TrimLeft(name, ".")

	// Truncate if too long
	if len(name) > 200 {
		ext := filepath.Ext(name)
		name = name[:200-len(ext)] + ext
	}

	return name
}

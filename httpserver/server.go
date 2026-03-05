// SPDX-License-Identifier: MPL-2.0
// Copyright 2026 Sagacient <sagacient@gmail.com>
//
// See CONTRIBUTORS.md for full contributor list.

// Package httpserver provides an HTTP server wrapping the MCP server with file storage endpoints.
package httpserver

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strings"

	"github.com/mark3labs/mcp-go/server"
	"github.com/sagacient/media-works-mcp-server/storage"
)

// Server wraps the MCP server and adds HTTP endpoints for file storage.
type Server struct {
	mcpServer *server.MCPServer
	fileStore *storage.FileStore
	port      int
}

// NewServer creates a new HTTP server.
func NewServer(mcpServer *server.MCPServer, fileStore *storage.FileStore, port int) *Server {
	return &Server{
		mcpServer: mcpServer,
		fileStore: fileStore,
		port:      port,
	}
}

// Start starts the HTTP server.
func (s *Server) Start() error {
	mux := http.NewServeMux()

	// MCP SSE endpoint
	sseServer := server.NewSSEServer(s.mcpServer)

	// Storage endpoints
	mux.HandleFunc("/storage/upload", s.corsMiddleware(s.handleUpload))
	mux.HandleFunc("/storage/list", s.corsMiddleware(s.handleList))
	mux.HandleFunc("/storage/download/", s.corsMiddleware(s.handleDownload))
	mux.HandleFunc("/storage/delete/", s.corsMiddleware(s.handleDelete))
	mux.HandleFunc("/health", s.corsMiddleware(s.handleHealth))

	// MCP SSE handler
	mux.Handle("/", sseServer)

	addr := fmt.Sprintf(":%d", s.port)
	log.Printf("Starting HTTP server on %s", addr)
	return http.ListenAndServe(addr, mux)
}

// corsMiddleware adds CORS headers.
func (s *Server) corsMiddleware(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, DELETE, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
		if r.Method == "OPTIONS" {
			w.WriteHeader(http.StatusOK)
			return
		}
		next(w, r)
	}
}

// handleUpload handles file uploads.
func (s *Server) handleUpload(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Parse multipart form
	if err := r.ParseMultipartForm(32 << 20); err != nil {
		http.Error(w, "Failed to parse form", http.StatusBadRequest)
		return
	}

	file, header, err := r.FormFile("file")
	if err != nil {
		http.Error(w, "No file provided", http.StatusBadRequest)
		return
	}
	defer file.Close()

	// Upload with scanning
	info, err := s.fileStore.Upload(header.Filename, file)
	if err != nil {
		switch e := err.(type) {
		case *storage.ErrMalwareDetected:
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusUnprocessableEntity)
			json.NewEncoder(w).Encode(map[string]interface{}{
				"error":  "Malware detected",
				"threat": e.Threat,
			})
			return
		case *storage.ErrScannerUnavailable:
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusServiceUnavailable)
			json.NewEncoder(w).Encode(map[string]interface{}{
				"error": "Malware scanner unavailable",
			})
			return
		default:
			http.Error(w, fmt.Sprintf("Upload failed: %v", err), http.StatusInternalServerError)
			return
		}
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(info)
}

// handleList lists uploaded files.
func (s *Server) handleList(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	files := s.fileStore.List()
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"files": files,
		"count": len(files),
	})
}

// handleDownload downloads a file by ID.
func (s *Server) handleDownload(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Extract ID from path
	id := strings.TrimPrefix(r.URL.Path, "/storage/download/")
	if id == "" {
		http.Error(w, "File ID required", http.StatusBadRequest)
		return
	}

	info := s.fileStore.Get(id)
	if info == nil {
		http.Error(w, "File not found or expired", http.StatusNotFound)
		return
	}

	f, err := os.Open(info.Path)
	if err != nil {
		http.Error(w, "Failed to read file", http.StatusInternalServerError)
		return
	}
	defer f.Close()

	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", info.Filename))
	io.Copy(w, f)
}

// handleDelete deletes a file by ID.
func (s *Server) handleDelete(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodDelete {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	id := strings.TrimPrefix(r.URL.Path, "/storage/delete/")
	if id == "" {
		http.Error(w, "File ID required", http.StatusBadRequest)
		return
	}

	if err := s.fileStore.Delete(id); err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{
		"status":  "deleted",
		"id":      id,
	})
}

// handleHealth returns server health status.
func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{
		"status": "ok",
	})
}

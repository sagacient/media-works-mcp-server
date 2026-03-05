// SPDX-License-Identifier: MPL-2.0
// Copyright 2026 Sagacient <sagacient@gmail.com>
//
// See CONTRIBUTORS.md for full contributor list.

package executor

import (
	"archive/tar"
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/docker/docker/api/types"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/image"
	"github.com/docker/docker/api/types/mount"
	"github.com/docker/docker/client"
	"github.com/docker/docker/pkg/stdcopy"
)

// ExecutionResult holds the result of a script execution.
type ExecutionResult struct {
	ExitCode    int
	Stdout      string
	Stderr      string
	TimedOut    bool
	Duration    time.Duration
	OutputFiles []OutputFileInfo
	OutputPath  string
	ExecutionID string
}

// DockerExecutor manages Docker containers for media processing.
type DockerExecutor struct {
	client          *client.Client
	imageName       string
	buildLocal      bool
	networkDisabled bool
	maxMemoryMB     int64
	maxCPUs         float64
	tempDir         string
	outputManager   *OutputManager

	mu           sync.RWMutex
	imageReady   bool
	imageErr     error
	imageChecked bool
}

// NewDockerExecutor creates a new Docker executor.
func NewDockerExecutor(imageName string, buildLocal, networkDisabled bool, maxMemoryMB int64, maxCPUs float64, tempDir string, outputManager *OutputManager) (*DockerExecutor, error) {
	socketPath := findDockerSocket()
	if socketPath == "" {
		return nil, fmt.Errorf("Docker socket not found. Ensure Docker is running")
	}

	cli, err := client.NewClientWithOpts(
		client.WithHost("unix://"+socketPath),
		client.WithAPIVersionNegotiation(),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create Docker client: %w", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := cli.Ping(ctx); err != nil {
		return nil, fmt.Errorf("Docker not accessible: %w", err)
	}

	e := &DockerExecutor{
		client:          cli,
		imageName:       imageName,
		buildLocal:      buildLocal,
		networkDisabled: networkDisabled,
		maxMemoryMB:     maxMemoryMB,
		maxCPUs:         maxCPUs,
		tempDir:         tempDir,
		outputManager:   outputManager,
	}

	return e, nil
}

// EnsureImageAsync starts pulling/building the Docker image in the background.
func (e *DockerExecutor) EnsureImageAsync() {
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
		defer cancel()

		e.mu.Lock()
		if e.imageChecked {
			e.mu.Unlock()
			return
		}
		e.imageChecked = true
		e.mu.Unlock()

		_, _, err := e.client.ImageInspectWithRaw(ctx, e.imageName)
		if err == nil {
			log.Printf("Docker image found locally: %s", e.imageName)
			e.mu.Lock()
			e.imageReady = true
			e.mu.Unlock()
			return
		}

		if e.buildLocal {
			log.Printf("Building Docker image locally: %s", e.imageName)
			err = e.buildImage(ctx)
		} else {
			log.Printf("Pulling Docker image: %s", e.imageName)
			err = e.pullImage(ctx)
		}

		e.mu.Lock()
		if err != nil {
			e.imageErr = err
			log.Printf("ERROR: Failed to prepare Docker image: %v", err)
		} else {
			e.imageReady = true
			log.Printf("Docker image ready: %s", e.imageName)
		}
		e.mu.Unlock()
	}()
}

func (e *DockerExecutor) pullImage(ctx context.Context) error {
	reader, err := e.client.ImagePull(ctx, e.imageName, image.PullOptions{})
	if err != nil {
		return fmt.Errorf("failed to pull image: %w", err)
	}
	defer reader.Close()
	io.Copy(io.Discard, reader)
	return nil
}

func (e *DockerExecutor) buildImage(ctx context.Context) error {
	dockerfilePaths := []string{
		"MediaWorks.Dockerfile",
		"/app/MediaWorks.Dockerfile",
	}

	var dockerfilePath string
	for _, p := range dockerfilePaths {
		if _, err := os.Stat(p); err == nil {
			dockerfilePath = p
			break
		}
	}
	if dockerfilePath == "" {
		return fmt.Errorf("MediaWorks.Dockerfile not found")
	}

	dockerfileContent, err := os.ReadFile(dockerfilePath)
	if err != nil {
		return fmt.Errorf("failed to read Dockerfile: %w", err)
	}

	var buf bytes.Buffer
	writeTarFile(&buf, "Dockerfile", dockerfileContent)

	scriptsDir := filepath.Join(filepath.Dir(dockerfilePath), "scripts")
	if info, err := os.Stat(scriptsDir); err == nil && info.IsDir() {
		filepath.Walk(scriptsDir, func(path string, info os.FileInfo, err error) error {
			if err != nil || info.IsDir() {
				return nil
			}
			relPath, _ := filepath.Rel(filepath.Dir(dockerfilePath), path)
			data, readErr := os.ReadFile(path)
			if readErr != nil {
				return nil
			}
			writeTarFile(&buf, relPath, data)
			return nil
		})
	}

	resp, err := e.client.ImageBuild(ctx, &buf, types.ImageBuildOptions{
		Tags:       []string{e.imageName},
		Dockerfile: "Dockerfile",
		Remove:     true,
	})
	if err != nil {
		return fmt.Errorf("failed to build image: %w", err)
	}
	defer resp.Body.Close()
	io.Copy(io.Discard, resp.Body)
	return nil
}

// IsImageReady returns whether the Docker image is ready.
func (e *DockerExecutor) IsImageReady() (bool, error) {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.imageReady, e.imageErr
}

// ExecuteScript executes a shell script in a Docker container with mounted files.
func (e *DockerExecutor) ExecuteScript(ctx context.Context, script string, files []string, timeout time.Duration) (*ExecutionResult, error) {
	ready, imgErr := e.IsImageReady()
	if imgErr != nil {
		return nil, fmt.Errorf("Docker image not available: %w", imgErr)
	}
	if !ready {
		return nil, fmt.Errorf("Docker image is still being prepared. Please try again in a moment")
	}

	tempDir, err := e.createAccessibleTempDir()
	if err != nil {
		return nil, fmt.Errorf("failed to create temp directory: %w", err)
	}
	defer os.RemoveAll(tempDir)

	scriptPath := filepath.Join(tempDir, "script.sh")
	if err := os.WriteFile(scriptPath, []byte(script), 0755); err != nil {
		return nil, fmt.Errorf("failed to write script: %w", err)
	}

	var outputDir string
	var execID string
	if e.outputManager != nil {
		execID = GenerateExecutionID()
		outputDir, err = e.outputManager.CreateExecutionDir(execID)
		if err != nil {
			return nil, fmt.Errorf("failed to create output directory: %w", err)
		}
	} else {
		outputDir = filepath.Join(tempDir, "output")
		if err := os.MkdirAll(outputDir, 0777); err != nil {
			return nil, fmt.Errorf("failed to create output directory: %w", err)
		}
	}

	mounts := []mount.Mount{
		{
			Type:     mount.TypeBind,
			Source:   scriptPath,
			Target:   "/script.sh",
			ReadOnly: true,
		},
		{
			Type:     mount.TypeBind,
			Source:   outputDir,
			Target:   "/output",
			ReadOnly: false,
		},
	}

	for i, f := range files {
		absPath, err := filepath.Abs(f)
		if err != nil {
			return nil, fmt.Errorf("failed to get absolute path for %s: %w", f, err)
		}
		mounts = append(mounts, mount.Mount{
			Type:     mount.TypeBind,
			Source:   absPath,
			Target:   fmt.Sprintf("/data/input_%d/%s", i, filepath.Base(f)),
			ReadOnly: true,
		})
	}

	containerConfig := &container.Config{
		Image: e.imageName,
		Cmd:   []string{"/script.sh"},
		Tty:   false,
	}

	hostConfig := &container.HostConfig{
		Mounts: mounts,
		Resources: container.Resources{
			Memory:   e.maxMemoryMB * 1024 * 1024,
			NanoCPUs: int64(e.maxCPUs * 1e9),
		},
		NetworkMode: "none",
		AutoRemove:  false,
	}

	if !e.networkDisabled {
		hostConfig.NetworkMode = "bridge"
	}

	timeoutCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	startTime := time.Now()

	resp, err := e.client.ContainerCreate(timeoutCtx, containerConfig, hostConfig, nil, nil, "")
	if err != nil {
		return nil, fmt.Errorf("failed to create container: %w", err)
	}
	containerID := resp.ID

	defer func() {
		rmCtx, rmCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer rmCancel()
		e.client.ContainerRemove(rmCtx, containerID, container.RemoveOptions{Force: true})
	}()

	if err := e.client.ContainerStart(timeoutCtx, containerID, container.StartOptions{}); err != nil {
		return nil, fmt.Errorf("failed to start container: %w", err)
	}

	statusCh, errCh := e.client.ContainerWait(timeoutCtx, containerID, container.WaitConditionNotRunning)

	var exitCode int
	var timedOut bool

	select {
	case err := <-errCh:
		if err != nil {
			if timeoutCtx.Err() != nil {
				timedOut = true
				stopCtx, stopCancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer stopCancel()
				e.client.ContainerStop(stopCtx, containerID, container.StopOptions{})
			} else {
				return nil, fmt.Errorf("container wait error: %w", err)
			}
		}
	case status := <-statusCh:
		exitCode = int(status.StatusCode)
	}

	duration := time.Since(startTime)

	// Collect logs
	logReader, err := e.client.ContainerLogs(context.Background(), containerID, container.LogsOptions{
		ShowStdout: true,
		ShowStderr: true,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to get container logs: %w", err)
	}
	defer logReader.Close()

	var stdoutBuf, stderrBuf bytes.Buffer
	demuxDockerLogs(logReader, &stdoutBuf, &stderrBuf)

	// Scan output files
	var outputFiles []OutputFileInfo
	if e.outputManager != nil {
		outputFiles, _ = e.outputManager.ScanOutputFiles(outputDir)
	} else {
		entries, _ := os.ReadDir(outputDir)
		for _, entry := range entries {
			if entry.IsDir() || strings.HasPrefix(entry.Name(), ".") {
				continue
			}
			info, _ := entry.Info()
			if info != nil {
				outputFiles = append(outputFiles, OutputFileInfo{
					Name: entry.Name(),
					Size: info.Size(),
					Path: filepath.Join(outputDir, entry.Name()),
				})
			}
		}
	}

	result := &ExecutionResult{
		ExitCode:    exitCode,
		Stdout:      stdoutBuf.String(),
		Stderr:      stderrBuf.String(),
		TimedOut:    timedOut,
		Duration:    duration,
		OutputFiles: outputFiles,
		OutputPath:  outputDir,
		ExecutionID: execID,
	}

	return result, nil
}

// ValidateFilePaths validates that all file paths are safe and exist.
func ValidateFilePaths(files []string) error {
	for _, f := range files {
		if strings.Contains(f, "..") {
			return fmt.Errorf("path traversal not allowed: %s", f)
		}
		info, err := os.Stat(f)
		if err != nil {
			return fmt.Errorf("file not found: %s", f)
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("not a regular file: %s", f)
		}
	}
	return nil
}

// GenerateExecutionID generates a short random execution ID.
func GenerateExecutionID() string {
	b := make([]byte, 4)
	rand.Read(b)
	return "exec-" + hex.EncodeToString(b)
}

// createAccessibleTempDir creates a temp directory accessible to Docker.
func (e *DockerExecutor) createAccessibleTempDir() (string, error) {
	baseDir := e.tempDir
	if baseDir == "" {
		if runtime.GOOS == "darwin" {
			baseDir = filepath.Join(os.TempDir(), "media-works")
		} else {
			baseDir = "/tmp/media-works"
		}
	}
	if err := os.MkdirAll(baseDir, 0777); err != nil {
		return "", fmt.Errorf("failed to create base temp dir: %w", err)
	}
	return os.MkdirTemp(baseDir, "mw-exec-")
}

// findDockerSocket finds the Docker socket path.
func findDockerSocket() string {
	paths := []string{
		"/var/run/docker.sock",
	}

	if runtime.GOOS == "darwin" {
		home, _ := os.UserHomeDir()
		paths = append(paths,
			filepath.Join(home, ".colima/default/docker.sock"),
			filepath.Join(home, ".docker/run/docker.sock"),
			filepath.Join(home, "Library/Containers/com.docker.docker/Data/docker.raw.sock"),
		)
	}

	for _, p := range paths {
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	return ""
}

// writeTarFile adds a file to a tar archive.
func writeTarFile(buf *bytes.Buffer, name string, content []byte) {
	tw := tar.NewWriter(buf)
	tw.WriteHeader(&tar.Header{
		Name: name,
		Size: int64(len(content)),
		Mode: 0644,
	})
	tw.Write(content)
	tw.Close()
}

// OutputManager returns the output manager.
func (e *DockerExecutor) OutputManager() *OutputManager {
	return e.outputManager
}

// demuxDockerLogs separates Docker container stdout and stderr streams.
func demuxDockerLogs(reader io.Reader, stdout, stderr *bytes.Buffer) {
	stdcopy.StdCopy(stdout, stderr, reader)
}

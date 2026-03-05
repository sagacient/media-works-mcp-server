// SPDX-License-Identifier: MPL-2.0
// Copyright 2026 Sagacient <sagacient@gmail.com>
//
// See CONTRIBUTORS.md for full contributor list.

// Package tools defines MCP tool definitions and handlers for media processing.
package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"github.com/sagacient/media-works-mcp-server/executor"
	"github.com/sagacient/media-works-mcp-server/storage"
	"github.com/sagacient/media-works-mcp-server/workerpool"
)

// MediaTools holds the dependencies for MCP tool handlers.
type MediaTools struct {
	executor  *executor.DockerExecutor
	fileStore *storage.FileStore
	pool      *workerpool.Pool
	timeout   time.Duration
}

// NewMediaTools creates a new MediaTools instance.
func NewMediaTools(exec *executor.DockerExecutor, fs *storage.FileStore, pool *workerpool.Pool, timeout time.Duration) *MediaTools {
	return &MediaTools{
		executor:  exec,
		fileStore: fs,
		pool:      pool,
		timeout:   timeout,
	}
}

// RegisterTools registers all media processing MCP tools.
func (t *MediaTools) RegisterTools(s *server.MCPServer) {
	// Tool 1: Extract audio from video
	s.AddTool(mcp.Tool{
		Name:        "extract_audio_from_video",
		Description: "Extract audio from a video file. Supports clipping with start/end times. Input: upload:// URI or file path. Outputs: audio file in specified format.",
		InputSchema: mcp.ToolInputSchema{
			Type: "object",
			Properties: map[string]interface{}{
				"file": map[string]interface{}{
					"type":        "string",
					"description": "Video file path or upload:// URI",
				},
				"output_format": map[string]interface{}{
					"type":        "string",
					"description": "Output audio format: mp3, wav, aac, flac, ogg (default: mp3)",
					"default":     "mp3",
				},
				"start_time": map[string]interface{}{
					"type":        "string",
					"description": "Start time for clipping (HH:MM:SS or seconds). Optional.",
				},
				"end_time": map[string]interface{}{
					"type":        "string",
					"description": "End time for clipping (HH:MM:SS or seconds). Optional.",
				},
				"bitrate": map[string]interface{}{
					"type":        "string",
					"description": "Audio bitrate (e.g., 128k, 192k, 320k). Default: 192k",
					"default":     "192k",
				},
			},
			Required: []string{"file"},
		},
	}, t.extractAudioHandler)

	// Tool 2: Extract frames from video
	s.AddTool(mcp.Tool{
		Name:        "extract_frames_from_video",
		Description: "Extract sampled frames from a video. Modes: 'interval' (every N seconds), 'count' (N evenly-spaced frames), 'keyframes' (I-frames only). Does NOT extract every frame.",
		InputSchema: mcp.ToolInputSchema{
			Type: "object",
			Properties: map[string]interface{}{
				"file": map[string]interface{}{
					"type":        "string",
					"description": "Video file path or upload:// URI",
				},
				"mode": map[string]interface{}{
					"type":        "string",
					"description": "Extraction mode: interval, count, or keyframes (default: interval)",
					"default":     "interval",
				},
				"param": map[string]interface{}{
					"type":        "string",
					"description": "Mode parameter: seconds for interval (default 5), frame count for count mode",
					"default":     "5",
				},
				"format": map[string]interface{}{
					"type":        "string",
					"description": "Output image format: jpg or png (default: jpg)",
					"default":     "jpg",
				},
				"quality": map[string]interface{}{
					"type":        "number",
					"description": "JPEG quality 1-100 (default: 85, only for jpg)",
					"default":     85,
				},
			},
			Required: []string{"file"},
		},
	}, t.extractFramesHandler)

	// Tool 3: Create video from images
	s.AddTool(mcp.Tool{
		Name:        "create_video_from_images",
		Description: "Create a video from a sequence of images with optional SRT subtitles overlay. Images are scaled and padded to target resolution.",
		InputSchema: mcp.ToolInputSchema{
			Type: "object",
			Properties: map[string]interface{}{
				"files": map[string]interface{}{
					"type":        "array",
					"items":       map[string]interface{}{"type": "string"},
					"description": "Array of image file paths or upload:// URIs, in order",
				},
				"output_format": map[string]interface{}{
					"type":        "string",
					"description": "Output video format: mp4, webm, avi (default: mp4)",
					"default":     "mp4",
				},
				"fps": map[string]interface{}{
					"type":        "number",
					"description": "Frames per second (default: 24)",
					"default":     24,
				},
				"duration_per_image": map[string]interface{}{
					"type":        "number",
					"description": "Seconds each image is shown (default: 3)",
					"default":     3,
				},
				"resolution": map[string]interface{}{
					"type":        "string",
					"description": "Output resolution WxH (default: 1920x1080)",
					"default":     "1920x1080",
				},
				"subtitle_file": map[string]interface{}{
					"type":        "string",
					"description": "Optional SRT subtitle file path or upload:// URI",
				},
			},
			Required: []string{"files"},
		},
	}, t.createVideoHandler)

	// Tool 4: Extract slides as images
	s.AddTool(mcp.Tool{
		Name:        "extract_slides_as_images",
		Description: "Export PPT/PPTX presentation slides as images using LibreOffice headless rendering. Supports selective slide export.",
		InputSchema: mcp.ToolInputSchema{
			Type: "object",
			Properties: map[string]interface{}{
				"file": map[string]interface{}{
					"type":        "string",
					"description": "PPT/PPTX file path or upload:// URI",
				},
				"format": map[string]interface{}{
					"type":        "string",
					"description": "Output image format: png or jpg (default: png)",
					"default":     "png",
				},
				"slides": map[string]interface{}{
					"type":        "string",
					"description": "Comma-separated slide numbers to export (default: all). E.g., '1,3,5'",
				},
			},
			Required: []string{"file"},
		},
	}, t.extractSlidesHandler)

	// Tool 5: Extract audio from presentation
	s.AddTool(mcp.Tool{
		Name:        "extract_audio_from_presentation",
		Description: "Extract embedded videos/audio from a PPTX file and convert them to audio files. Finds media in ppt/media/ inside the PPTX zip.",
		InputSchema: mcp.ToolInputSchema{
			Type: "object",
			Properties: map[string]interface{}{
				"file": map[string]interface{}{
					"type":        "string",
					"description": "PPTX file path or upload:// URI",
				},
				"output_format": map[string]interface{}{
					"type":        "string",
					"description": "Audio output format: mp3, wav, aac, flac (default: mp3)",
					"default":     "mp3",
				},
				"bitrate": map[string]interface{}{
					"type":        "string",
					"description": "Audio bitrate (default: 192k)",
					"default":     "192k",
				},
			},
			Required: []string{"file"},
		},
	}, t.extractPresentationAudioHandler)

	// Tool 6: List output files
	s.AddTool(mcp.Tool{
		Name:        "list_outputs",
		Description: "List all output files from media processing executions. Returns execution IDs and their output files.",
		InputSchema: mcp.ToolInputSchema{
			Type:       "object",
			Properties: map[string]interface{}{},
		},
	}, t.listOutputsHandler)

	// Tool 7: Get output file
	s.AddTool(mcp.Tool{
		Name:        "get_output",
		Description: "Get an output file from a media processing execution. Returns file contents for text files, metadata for binary files.",
		InputSchema: mcp.ToolInputSchema{
			Type: "object",
			Properties: map[string]interface{}{
				"execution_id": map[string]interface{}{
					"type":        "string",
					"description": "Execution ID from the processing result",
				},
				"filename": map[string]interface{}{
					"type":        "string",
					"description": "Name of the output file",
				},
			},
			Required: []string{"execution_id", "filename"},
		},
	}, t.getOutputHandler)

	// Tool 8: Delete output
	s.AddTool(mcp.Tool{
		Name:        "delete_output",
		Description: "Delete output files from a media processing execution.",
		InputSchema: mcp.ToolInputSchema{
			Type: "object",
			Properties: map[string]interface{}{
				"execution_id": map[string]interface{}{
					"type":        "string",
					"description": "Execution ID to delete",
				},
			},
			Required: []string{"execution_id"},
		},
	}, t.deleteOutputHandler)
}

// ─── Tool handlers ───

func (t *MediaTools) extractAudioHandler(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	file := getStringArg(request, "file")
	if file == "" {
		return mcp.NewToolResultError("'file' parameter is required"), nil
	}

	outputFormat := getStringArgDefault(request, "output_format", "mp3")
	startTime := getStringArg(request, "start_time")
	endTime := getStringArg(request, "end_time")
	bitrate := getStringArgDefault(request, "bitrate", "192k")

	resolvedFile, err := t.fileStore.ResolveUploadURI(file)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("Failed to resolve file: %v", err)), nil
	}

	if err := t.pool.Acquire(ctx); err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	defer t.pool.Release()

	containerPath := fmt.Sprintf("/data/input_0/%s", getFilename(resolvedFile))
	script := executor.ExtractAudioCommand(containerPath, outputFormat, startTime, endTime, bitrate)

	result, err := t.executor.ExecuteScript(ctx, script, []string{resolvedFile}, t.timeout)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("Execution failed: %v", err)), nil
	}

	return mcp.NewToolResultText(formatResult(result)), nil
}

func (t *MediaTools) extractFramesHandler(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	file := getStringArg(request, "file")
	if file == "" {
		return mcp.NewToolResultError("'file' parameter is required"), nil
	}

	mode := getStringArgDefault(request, "mode", "interval")
	param := getStringArgDefault(request, "param", "5")
	format := getStringArgDefault(request, "format", "jpg")
	quality := getIntArgDefault(request, "quality", 85)

	if mode != "interval" && mode != "count" && mode != "keyframes" {
		return mcp.NewToolResultError("'mode' must be 'interval', 'count', or 'keyframes'"), nil
	}

	resolvedFile, err := t.fileStore.ResolveUploadURI(file)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("Failed to resolve file: %v", err)), nil
	}

	if err := t.pool.Acquire(ctx); err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	defer t.pool.Release()

	containerPath := fmt.Sprintf("/data/input_0/%s", getFilename(resolvedFile))
	script := executor.ExtractFramesCommand(containerPath, mode, param, format, quality)

	result, err := t.executor.ExecuteScript(ctx, script, []string{resolvedFile}, t.timeout)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("Execution failed: %v", err)), nil
	}

	return mcp.NewToolResultText(formatResult(result)), nil
}

func (t *MediaTools) createVideoHandler(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	filesRaw, ok := request.GetArguments()["files"]
	if !ok {
		return mcp.NewToolResultError("'files' parameter is required"), nil
	}

	files, err := toStringSlice(filesRaw)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("Invalid 'files' parameter: %v", err)), nil
	}

	if len(files) == 0 {
		return mcp.NewToolResultError("'files' must contain at least one image path"), nil
	}

	outputFormat := getStringArgDefault(request, "output_format", "mp4")
	fps := getIntArgDefault(request, "fps", 24)
	durationPerImage := getFloatArgDefault(request, "duration_per_image", 3.0)
	resolution := getStringArgDefault(request, "resolution", "1920x1080")
	subtitleFile := getStringArg(request, "subtitle_file")

	// Resolve all file paths
	var resolvedFiles []string
	var containerPaths []string
	for i, f := range files {
		resolved, err := t.fileStore.ResolveUploadURI(f)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("Failed to resolve file %d: %v", i, err)), nil
		}
		resolvedFiles = append(resolvedFiles, resolved)
		containerPaths = append(containerPaths, fmt.Sprintf("/data/input_%d/%s", i, getFilename(resolved)))
	}

	// Resolve subtitle file if provided
	var subtitleContainerPath string
	if subtitleFile != "" {
		resolved, err := t.fileStore.ResolveUploadURI(subtitleFile)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("Failed to resolve subtitle file: %v", err)), nil
		}
		idx := len(resolvedFiles)
		resolvedFiles = append(resolvedFiles, resolved)
		subtitleContainerPath = fmt.Sprintf("/data/input_%d/%s", idx, getFilename(resolved))
	}

	if err := t.pool.Acquire(ctx); err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	defer t.pool.Release()

	script := executor.ImagesToVideoCommand(containerPaths, outputFormat, fps, durationPerImage, resolution, subtitleContainerPath)

	result, err := t.executor.ExecuteScript(ctx, script, resolvedFiles, t.timeout)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("Execution failed: %v", err)), nil
	}

	return mcp.NewToolResultText(formatResult(result)), nil
}

func (t *MediaTools) extractSlidesHandler(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	file := getStringArg(request, "file")
	if file == "" {
		return mcp.NewToolResultError("'file' parameter is required"), nil
	}

	format := getStringArgDefault(request, "format", "png")
	slides := getStringArg(request, "slides")

	resolvedFile, err := t.fileStore.ResolveUploadURI(file)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("Failed to resolve file: %v", err)), nil
	}

	if err := t.pool.Acquire(ctx); err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	defer t.pool.Release()

	containerPath := fmt.Sprintf("/data/input_0/%s", getFilename(resolvedFile))
	script := executor.PPTSlidesToImagesCommand(containerPath, format, slides)

	result, err := t.executor.ExecuteScript(ctx, script, []string{resolvedFile}, t.timeout)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("Execution failed: %v", err)), nil
	}

	return mcp.NewToolResultText(formatResult(result)), nil
}

func (t *MediaTools) extractPresentationAudioHandler(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	file := getStringArg(request, "file")
	if file == "" {
		return mcp.NewToolResultError("'file' parameter is required"), nil
	}

	outputFormat := getStringArgDefault(request, "output_format", "mp3")
	bitrate := getStringArgDefault(request, "bitrate", "192k")

	resolvedFile, err := t.fileStore.ResolveUploadURI(file)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("Failed to resolve file: %v", err)), nil
	}

	if err := t.pool.Acquire(ctx); err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	defer t.pool.Release()

	containerPath := fmt.Sprintf("/data/input_0/%s", getFilename(resolvedFile))
	script := executor.PPTExtractAudioCommand(containerPath, outputFormat, bitrate)

	result, err := t.executor.ExecuteScript(ctx, script, []string{resolvedFile}, t.timeout)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("Execution failed: %v", err)), nil
	}

	return mcp.NewToolResultText(formatResult(result)), nil
}

func (t *MediaTools) listOutputsHandler(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	if t.executor == nil {
		return mcp.NewToolResultError("Output management not configured"), nil
	}

	executions, err := t.executor.OutputManager().ListExecutions()
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("Failed to list outputs: %v", err)), nil
	}

	if len(executions) == 0 {
		return mcp.NewToolResultText("No output files found."), nil
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Found %d execution(s):\n\n", len(executions)))
	for _, exec := range executions {
		sb.WriteString(fmt.Sprintf("Execution: %s\n", exec["execution_id"]))
		if tool, ok := exec["tool"]; ok && tool != "" {
			sb.WriteString(fmt.Sprintf("  Tool: %s\n", tool))
		}
		sb.WriteString(fmt.Sprintf("  Files: %d\n", exec["file_count"]))
		if files, ok := exec["files"].([]executor.OutputFileInfo); ok {
			for _, f := range files {
				sb.WriteString(fmt.Sprintf("    - %s (%d bytes)\n", f.Name, f.Size))
			}
		}
		sb.WriteString("\n")
	}

	return mcp.NewToolResultText(sb.String()), nil
}

func (t *MediaTools) getOutputHandler(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	execID := getStringArg(request, "execution_id")
	filename := getStringArg(request, "filename")
	if execID == "" || filename == "" {
		return mcp.NewToolResultError("'execution_id' and 'filename' are required"), nil
	}

	if t.executor == nil {
		return mcp.NewToolResultError("Output management not configured"), nil
	}

	data, fileInfo, err := t.executor.OutputManager().GetFile(execID, filename)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("Failed to get file: %v", err)), nil
	}

	if data != nil {
		// Text file — return content
		return mcp.NewToolResultText(fmt.Sprintf("File: %s (%d bytes)\n\n%s", fileInfo.Name, fileInfo.Size, string(data))), nil
	}

	// Binary file — return metadata
	return mcp.NewToolResultText(fmt.Sprintf("Binary file: %s (%d bytes)\nUse the download endpoint to retrieve this file.", fileInfo.Name, fileInfo.Size)), nil
}

func (t *MediaTools) deleteOutputHandler(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	execID := getStringArg(request, "execution_id")
	if execID == "" {
		return mcp.NewToolResultError("'execution_id' is required"), nil
	}

	if t.executor == nil {
		return mcp.NewToolResultError("Output management not configured"), nil
	}

	if err := t.executor.OutputManager().DeleteExecution(execID); err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("Failed to delete: %v", err)), nil
	}

	return mcp.NewToolResultText(fmt.Sprintf("Deleted execution output: %s", execID)), nil
}

// ─── Helper functions ───

func getStringArg(req mcp.CallToolRequest, key string) string {
	v, ok := req.GetArguments()[key]
	if !ok {
		return ""
	}
	s, ok := v.(string)
	if !ok {
		return ""
	}
	return s
}

func getStringArgDefault(req mcp.CallToolRequest, key, defaultVal string) string {
	v := getStringArg(req, key)
	if v == "" {
		return defaultVal
	}
	return v
}

func getIntArgDefault(req mcp.CallToolRequest, key string, defaultVal int) int {
	v, ok := req.GetArguments()[key]
	if !ok {
		return defaultVal
	}
	switch n := v.(type) {
	case float64:
		return int(n)
	case int:
		return n
	default:
		return defaultVal
	}
}

func getFloatArgDefault(req mcp.CallToolRequest, key string, defaultVal float64) float64 {
	v, ok := req.GetArguments()[key]
	if !ok {
		return defaultVal
	}
	n, ok := v.(float64)
	if !ok {
		return defaultVal
	}
	return n
}

func toStringSlice(v interface{}) ([]string, error) {
	arr, ok := v.([]interface{})
	if !ok {
		return nil, fmt.Errorf("expected array, got %T", v)
	}
	var result []string
	for _, item := range arr {
		s, ok := item.(string)
		if !ok {
			return nil, fmt.Errorf("expected string in array, got %T", item)
		}
		result = append(result, s)
	}
	return result, nil
}

func getFilename(path string) string {
	parts := strings.Split(path, "/")
	if len(parts) > 0 {
		return parts[len(parts)-1]
	}
	return path
}

func formatResult(result *executor.ExecutionResult) string {
	output := ""

	if result.TimedOut {
		output += "WARNING: Execution timed out!\n\n"
	}

	if result.Stdout != "" {
		output += result.Stdout
	}

	if result.Stderr != "" {
		if output != "" {
			output += "\n"
		}
		output += "=== Stderr ===\n" + result.Stderr
	}

	if result.ExitCode != 0 {
		if output != "" {
			output += "\n"
		}
		output += fmt.Sprintf("=== Error ===\nExit code: %d", result.ExitCode)
	}

	output += fmt.Sprintf("\n\n[Execution completed in %v with exit code %d]", result.Duration.Round(time.Millisecond), result.ExitCode)

	// Append execution metadata as parseable JSON for downstream clients
	// This enables secure file serving and proper URL generation
	if result.ExecutionID != "" {
		metadata := map[string]interface{}{
			"execution_id": result.ExecutionID,
			"output_files": result.OutputFiles,
			"output_path":  result.OutputPath,
		}
		metadataJSON, err := json.Marshal(metadata)
		if err == nil {
			output += "\n\n[EXECUTION_METADATA]" + string(metadataJSON) + "[/EXECUTION_METADATA]"
		}
	}

	log.Printf("Execution complete: exit=%d, duration=%s, files=%d",
		result.ExitCode, result.Duration.Round(time.Millisecond), len(result.OutputFiles))

	return output
}

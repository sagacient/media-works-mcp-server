# Media Works MCP Server

A Docker-based media processing server implementing the [Model Context Protocol (MCP)](https://modelcontextprotocol.io/). Processes video, audio, images, and PowerPoint files via containerized ffmpeg and LibreOffice.

## Features

- **Extract Audio from Video** — Full or clipped audio extraction with configurable format/bitrate
- **Extract Frames from Video** — Sampled frame extraction (interval, count, or keyframes)
- **Create Video from Images** — Build video from image sequences with optional SRT subtitles
- **Export PPT Slides as Images** — High-quality slide rendering via LibreOffice headless
- **Extract Audio from Presentations** — Extract embedded media from PPTX and convert to audio
- **File Upload with TTL** — Upload files with automatic expiration and malware scanning
- **Output Management** — TTL-based output cleanup with list/get/delete operations

## Architecture

```
┌─────────────────────────────┐
│     MCP Client (LLM)        │
│  (Claude, GPT, etc.)        │
└──────────┬──────────────────┘
           │ stdio / HTTP+SSE
┌──────────▼──────────────────┐
│  Media Works MCP Server     │
│  (Go binary)                │
│  ┌───────────────────────┐  │
│  │ Tools Layer           │  │
│  │ - extract_audio       │  │
│  │ - extract_frames      │  │
│  │ - create_video        │  │
│  │ - extract_slides      │  │
│  │ - extract_ppt_audio   │  │
│  │ - list/get/del output │  │
│  ├───────────────────────┤  │
│  │ Executor Layer        │  │
│  │ - Command builders    │  │
│  │ - Docker orchestrator │  │
│  │ - Output manager      │  │
│  ├───────────────────────┤  │
│  │ Storage Layer         │  │
│  │ - File upload + TTL   │  │
│  │ - ClamAV scanning     │  │
│  │ - upload:// resolver  │  │
│  └───────────────────────┘  │
└──────────┬──────────────────┘
           │ Docker API
┌──────────▼──────────────────┐
│  MediaWorks Container       │
│  (Ubuntu 24.04)             │
│  - ffmpeg                   │
│  - LibreOffice headless     │
│  - python3 + python-pptx    │
│  - Helper scripts           │
└─────────────────────────────┘
```

## Quick Start

### Prerequisites

- Docker (Docker Desktop, Colima, or similar)
- Go 1.24+ (for building from source)

### Run with Docker Compose (HTTP mode)

```bash
docker compose up media-works-http
```

### Run from Source (stdio mode)

```bash
go build -o media-works-server .
./media-works-server
```

### Run from Source (HTTP mode)

```bash
TRANSPORT=http HTTP_PORT=8080 go run .
```

## MCP Tools

### `extract_audio_from_video`
Extract audio from a video file with optional clipping.

| Parameter | Type | Required | Default | Description |
|-----------|------|----------|---------|-------------|
| `file` | string | Yes | — | Video file path or `upload://` URI |
| `output_format` | string | No | `mp3` | Audio format: mp3, wav, aac, flac, ogg |
| `start_time` | string | No | — | Start time (HH:MM:SS or seconds) |
| `end_time` | string | No | — | End time (HH:MM:SS or seconds) |
| `bitrate` | string | No | `192k` | Audio bitrate |

### `extract_frames_from_video`
Extract sampled frames from a video. Never extracts every frame.

| Parameter | Type | Required | Default | Description |
|-----------|------|----------|---------|-------------|
| `file` | string | Yes | — | Video file path or `upload://` URI |
| `mode` | string | No | `interval` | Mode: `interval`, `count`, `keyframes` |
| `param` | string | No | `5` | Interval seconds or frame count |
| `format` | string | No | `jpg` | Output format: jpg, png |
| `quality` | number | No | `85` | JPEG quality (1-100) |

### `create_video_from_images`
Create a video from image sequence with optional subtitles.

| Parameter | Type | Required | Default | Description |
|-----------|------|----------|---------|-------------|
| `files` | array | Yes | — | Array of image paths or `upload://` URIs |
| `output_format` | string | No | `mp4` | Video format: mp4, webm, avi |
| `fps` | number | No | `24` | Frames per second |
| `duration_per_image` | number | No | `3` | Seconds per image |
| `resolution` | string | No | `1920x1080` | Output resolution |
| `subtitle_file` | string | No | — | SRT subtitle file |

### `extract_slides_as_images`
Export PPT/PPTX slides as images via LibreOffice headless.

| Parameter | Type | Required | Default | Description |
|-----------|------|----------|---------|-------------|
| `file` | string | Yes | — | PPT/PPTX path or `upload://` URI |
| `format` | string | No | `png` | Output: png, jpg |
| `slides` | string | No | all | Comma-separated slide numbers |

### `extract_audio_from_presentation`
Extract embedded media from PPTX and convert to audio.

| Parameter | Type | Required | Default | Description |
|-----------|------|----------|---------|-------------|
| `file` | string | Yes | — | PPTX path or `upload://` URI |
| `output_format` | string | No | `mp3` | Audio format |
| `bitrate` | string | No | `192k` | Audio bitrate |

### `list_outputs`
List all output files from media processing executions.

### `get_output`
Retrieve a specific output file by execution ID and filename.

### `delete_output`
Delete output files from a specific execution.

## Configuration

All settings via environment variables:

| Variable | Default | Description |
|----------|---------|-------------|
| `MAX_WORKERS` | `5` | Concurrent execution slots |
| `EXECUTION_TIMEOUT` | `300s` | Per-execution timeout |
| `MAX_MEMORY_MB` | `1024` | Container memory limit (MB) |
| `MAX_CPUS` | `2.0` | Container CPU limit |
| `DOCKER_IMAGE` | `sagacient/mediaworks:latest` | MediaWorks Docker image |
| `BUILD_LOCAL` | `false` | Build image from MediaWorks.Dockerfile |
| `NETWORK_DISABLED` | `true` | Disable network in containers |
| `TRANSPORT` | `stdio` | Transport: `stdio` or `http` |
| `HTTP_PORT` | `8080` | HTTP port (when TRANSPORT=http) |
| `STORAGE_DIR` | `~/.cache/media-works/uploads` | Upload storage directory |
| `UPLOAD_TTL` | `1h` | Upload file expiration |
| `MAX_UPLOAD_SIZE` | `524288000` | Max upload size (500MB) |
| `SCAN_UPLOADS` | `false` | Enable ClamAV malware scanning |
| `SCAN_ON_FAIL` | `reject` | Action when scanner unavailable: `reject` or `allow` |
| `OUTPUT_DIR` | — | Persistent output directory |
| `OUTPUT_TTL` | `24h` | Output expiration |

## Security

- **Container isolation** — All media processing runs in ephemeral Docker containers
- **No network** — Containers have no network access by default
- **Non-root** — MediaWorks container runs as UID 1000
- **Resource limits** — Memory and CPU limits enforced per container
- **Malware scanning** — Optional ClamAV integration for uploaded files
- **TTL cleanup** — Automatic expiration of uploads and outputs
- **Path validation** — Protection against path traversal attacks

## Project Structure

```
media-works-mcp-server/
├── main.go                    # Entry point
├── config/config.go           # Configuration from env vars
├── executor/
│   ├── docker.go              # Docker container orchestration
│   ├── commands.go            # Shell script command builders
│   └── output.go              # Output directory management
├── tools/tools.go             # MCP tool definitions & handlers
├── storage/filestore.go       # File upload with TTL + scanning
├── scanner/clamav.go          # ClamAV malware scanner
├── httpserver/server.go       # HTTP transport + file endpoints
├── workerpool/pool.go         # Semaphore-based worker pool
├── Dockerfile                 # Server container (Go + ClamAV)
├── MediaWorks.Dockerfile      # Execution environment (synced)
├── entrypoint.sh              # Container entrypoint
└── docker-compose.yml         # Docker Compose configs
```

## License

[Mozilla Public License 2.0 (MPL-2.0)](LICENCE)

# Dockerfile to build the Media Works MCP Server as a container
# This runs the Go server inside Docker with Docker-in-Docker support
# Includes ClamAV for malware scanning of uploaded files

# Build stage
FROM golang:1.24-alpine AS builder

WORKDIR /build

# Install git for go mod download
RUN apk add --no-cache git

# Copy go mod files first for better caching
COPY go.mod go.sum ./
RUN go mod download

# Copy source code
COPY . .

# Build the binary
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-w -s" -o media-works-server .

# Runtime stage
FROM docker:27-cli

# Install ca-certificates for HTTPS and ClamAV for malware scanning
RUN apk add --no-cache \
    ca-certificates \
    clamav \
    clamav-daemon \
    clamav-libunrar \
    bash

# Create clamav user directories and set permissions
RUN mkdir -p /var/lib/clamav /var/run/clamav /var/log/clamav && \
    chown -R clamav:clamav /var/lib/clamav /var/run/clamav /var/log/clamav

# Configure ClamAV
RUN sed -i 's/^#LocalSocket /LocalSocket /' /etc/clamav/clamd.conf && \
    sed -i 's/^#LocalSocketMode /LocalSocketMode /' /etc/clamav/clamd.conf && \
    echo "TCPSocket 3310" >> /etc/clamav/clamd.conf && \
    echo "TCPAddr 127.0.0.1" >> /etc/clamav/clamd.conf

# Download initial virus definitions
RUN freshclam --stdout || echo "Initial freshclam failed, will retry at startup"

WORKDIR /app

# Copy the binary from builder
COPY --from=builder /build/media-works-server .

# Copy the MediaWorks Dockerfile (required for local image building)
COPY MediaWorks.Dockerfile .

# Copy entrypoint script
COPY entrypoint.sh .
RUN chmod +x entrypoint.sh

# Create storage directory for HTTP mode file uploads
RUN mkdir -p /storage && chmod 755 /storage

# Create temp directory for script execution
RUN mkdir -p /tmp/media-works && chmod 755 /tmp/media-works

# Environment variables (can be overridden)
ENV MAX_WORKERS=5 \
    EXECUTION_TIMEOUT=300s \
    DOCKER_IMAGE=sagacient/mediaworks:latest \
    TRANSPORT=stdio \
    STORAGE_DIR=/storage \
    UPLOAD_TTL=1h \
    MAX_UPLOAD_SIZE=524288000 \
    SCAN_UPLOADS=true \
    SCAN_ON_FAIL=reject \
    TEMP_DIR=/tmp/media-works \
    OUTPUT_DIR="" \
    OUTPUT_TTL=24h

# Expose HTTP port (only used when TRANSPORT=http)
EXPOSE 8080

# The server needs access to Docker socket
# Run with: docker run -v /var/run/docker.sock:/var/run/docker.sock media-works-mcp-server

ENTRYPOINT ["./entrypoint.sh"]

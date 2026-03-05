#!/bin/bash
# Entrypoint for Media Works MCP Server container.
# Starts ClamAV daemon (if enabled) and then launches the MCP server.

set -e

# Log to stderr to avoid interfering with stdio MCP
log() {
    echo "[entrypoint] $*" >&2
}

# Start ClamAV if scanning is enabled
if [ "${SCAN_UPLOADS}" = "true" ]; then
    log "Starting ClamAV daemon..."

    # Update virus definitions
    freshclam --stdout >&2 2>&1 || log "freshclam update failed (will use existing definitions)"

    # Start clamd in background
    clamd &

    # Wait for clamd to be ready (up to 30 seconds)
    TRIES=0
    while [ $TRIES -lt 30 ]; do
        if clamdscan --ping 2>/dev/null; then
            log "ClamAV daemon ready"
            break
        fi
        TRIES=$((TRIES + 1))
        sleep 1
    done

    if [ $TRIES -ge 30 ]; then
        log "WARNING: ClamAV daemon did not start within 30 seconds"
        if [ "${SCAN_ON_FAIL}" = "reject" ]; then
            log "SCAN_ON_FAIL=reject: uploads will be rejected until scanner is ready"
        else
            log "SCAN_ON_FAIL=allow: uploads will be allowed without scanning"
        fi
    fi
else
    log "Malware scanning disabled"
fi

# Start the MCP server
log "Starting Media Works MCP Server..."
exec ./media-works-server "$@"

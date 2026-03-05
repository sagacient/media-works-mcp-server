# MediaWorks - Pre-built media processing environment for MCP servers
# This is a synced copy of mediaworks/Dockerfile for local builds.
# Keep in sync with the standalone mediaworks/ project.

FROM ubuntu:24.04

LABEL org.opencontainers.image.title="MediaWorks"
LABEL org.opencontainers.image.description="Pre-built media processing environment with ffmpeg, LibreOffice, and python-pptx for MCP servers"

ENV DEBIAN_FRONTEND=noninteractive \
    PYTHONUNBUFFERED=1 \
    PYTHONDONTWRITEBYTECODE=1 \
    PIP_NO_CACHE_DIR=1 \
    PIP_DISABLE_PIP_VERSION_CHECK=1

RUN apt-get update && apt-get install -y --no-install-recommends \
    ffmpeg \
    libreoffice-impress \
    libreoffice-common \
    python3 \
    python3-pip \
    python3-venv \
    fonts-liberation \
    fonts-dejavu-core \
    fonts-noto-core \
    bash \
    coreutils \
    && rm -rf /var/lib/apt/lists/*

RUN pip3 install --no-cache-dir --break-system-packages \
    'python-pptx>=1.0.0' \
    'Pillow>=11.0.0'

RUN useradd -m -s /bin/bash -u 1000 mediaworks

RUN mkdir -p /data /output /scripts && \
    chown -R mediaworks:mediaworks /data /output

COPY scripts/ /scripts/
RUN chmod +x /scripts/*.sh && \
    chown -R mediaworks:mediaworks /scripts

USER mediaworks
WORKDIR /home/mediaworks
ENTRYPOINT ["bash"]

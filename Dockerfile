# ==========================================
# Build Stage
# ==========================================
FROM golang:1.27.2-alpine AS builder

WORKDIR /build

# Copy dependency definition
COPY go.mod ./

# Copy source code
COPY . .

# Compile stripped, statically linked binary
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -trimpath -o tools-server main.go

# ==========================================
# Runtime Stage
# ==========================================
FROM alpine:3.20

# Install FFmpeg, Gifsicle, CA certificates, timezone data, and curl for healthchecks
RUN apk add --no-cache \
    ffmpeg \
    gifsicle \
    ca-certificates \
    tzdata \
    curl

# Create unprivileged application user
RUN addgroup -S appgroup && adduser -S appuser -G appgroup

WORKDIR /app

# Copy binary from builder stage
COPY --from=builder /build/tools-server /app/tools-server

# Set up scratch directory with write permissions for appuser
RUN mkdir -p /tmp/tools-scratch && chown -R appuser:appgroup /tmp/tools-scratch /app

USER appuser

# Default environment settings
ENV PORT=8080 \
    SCRATCH_DIR=/tmp/tools-scratch \
    MAX_CONCURRENT_JOBS=2 \
    RATE_LIMIT_REQUESTS=5 \
    RATE_LIMIT_WINDOW=10m \
    MAX_UPLOAD_SIZE_MB=100 \
    MAX_DURATION_SECONDS=20

EXPOSE 8080

# Built-in container health monitoring
HEALTHCHECK --interval=30s --timeout=5s --start-period=5s --retries=3 \
  CMD curl -f http://localhost:8080/healthz || exit 1

ENTRYPOINT ["/app/tools-server"]

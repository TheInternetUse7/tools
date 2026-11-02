# Tools

A minimalist, self-hosted web utility suite written in Go.

---

## Quickstart with Docker & Cloudflare Tunnel

### 1. Run via Docker Compose

Use the included `docker-compose.yml` on your server.

Start the container:

```bash
docker compose pull
docker compose up -d
```

### 2. Configure Cloudflare Tunnel (`cloudflared`)

Add an ingress rule to your Cloudflare Tunnel config (`config.yml` or Zero Trust dashboard):

```yaml
ingress:
  - hostname: tools.example.com
    service: http://127.0.0.1:8080
  - service: http_status:404
```

---

## Environment Variables

| Variable               | Default              | Description                                                   |
| :--------------------- | :------------------- | :------------------------------------------------------------ |
| `PORT`                 | `8080`               | Port the HTTP server listens on                               |
| `MAX_CONCURRENT_JOBS`  | `2`                  | Maximum simultaneous FFmpeg processes (returns 429 when busy) |
| `RATE_LIMIT_REQUESTS`  | `5`                  | Maximum conversions allowed per IP within the window          |
| `RATE_LIMIT_WINDOW`    | `10m`                | Sliding time window for the IP rate limiter                   |
| `MAX_UPLOAD_SIZE_MB`   | `100`                | Maximum video upload size in megabytes                        |
| `MAX_DURATION_SECONDS` | `20.0`               | Maximum allowable GIF duration                                |
| `SCRATCH_DIR`          | `/tmp/tools-scratch` | Directory for temporary conversion scratch files              |

---

## Health Monitoring

The service includes a `/healthz` endpoint returning origin health and active FFmpeg workloads:

```bash
curl http://localhost:8080/healthz
```

```json
{
  "active_jobs": 0,
  "ffmpeg": true,
  "max_concurrent_jobs": 2,
  "status": "ok",
  "tools_count": 1
}
```

---

## Native Host Build & Systemd (Alternative to Docker)

If you prefer to run natively on the host instead of Docker:

### 1. Compile with stripped debug symbols

```bash
go build -ldflags="-s -w" -trimpath -o tools-server main.go
```

### 2. Systemd Unit File (`/etc/systemd/system/tools.service`)

```ini
[Unit]
Description=Tools Web Suite
After=network.target

[Service]
Type=simple
User=www-data
Group=www-data
WorkingDirectory=/var/www/tools
ExecStart=/var/www/tools/tools-server
Restart=always
RestartSec=5
LimitNOFILE=65535
Environment="PORT=8080"
Environment="MAX_CONCURRENT_JOBS=2"
Environment="SCRATCH_DIR=/tmp/tools-scratch"

[Install]
WantedBy=multi-user.target
```

Enable and start the service:

```bash
sudo systemctl daemon-reload
sudo systemctl enable --now tools
```

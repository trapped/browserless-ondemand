# browserless-ondemand

A small Go supervisor layered on the upstream Browserless Chromium image. It
starts Browserless on the first HTTP request or WebSocket connection, and stops
Browserless, Xvfb and Chromium after the last request/session ends and the idle
timeout expires. Only the supervisor stays resident while asleep.

```yaml
services:
  browserless:
    image: ghcr.io/trapped/browserless-ondemand:latest
    restart: unless-stopped
    environment:
      TOKEN: ${BROWSERLESS_TOKEN}
      ONDEMAND_IDLE_TIMEOUT: 60s
    ports:
      - "127.0.0.1:3000:3000"
    shm_size: 512mb
    stop_grace_period: 15s
```

Connect Puppeteer/Playwright exactly as with upstream, for example
`ws://localhost:3000?token=...`. Existing Browserless authentication, routes,
query parameters and configuration remain upstream's responsibility. No Docker
socket, privileged mode, controller service or external API credentials needed.
The container retains upstream's non-root user and filesystem.

## Configuration

| Variable | Default | Meaning |
| --- | --- | --- |
| `HOST` | `0.0.0.0` | Public supervisor bind address |
| `PORT` | `3000` | Public HTTP/WebSocket port |
| `ONDEMAND_BACKEND_PORT` | `3001` | Loopback-only Browserless port; must differ from PORT |
| `ONDEMAND_IDLE_TIMEOUT` | `60s` | Grace period after all requests/sessions finish |
| `ONDEMAND_START_TIMEOUT` | `60s` | Maximum cold-start wait |

Durations use Go syntax (`500ms`, `60s`, `5m`). Other environment variables pass
through unchanged; the child receives its internal HOST/PORT. Long HTTP responses
and upgraded WebSockets count as active until they close. HTTP keep-alive by
itself does not keep Browserless awake. Startup is serialized across concurrent
requests. A crashed backend is restarted by the next request; existing sessions
cannot be recovered. SIGTERM stops the complete child process group.

`GET /_ondemand/health` returns supervisor liveness without waking Browserless.
Use it for container health checks. All upstream health/metrics requests **do**
wake Browserless; update external probes accordingly. The reserved health route
contains no backend state or credentials.

Cold starts add latency: clients must allow sufficient connect time. Browserless
session reconnect/reuse state is lost when the backend stops; an open WebSocket
is protected, but disconnected reconnect sessions are not. Configure the idle
timeout accordingly. Upstream-generated URLs retain the public HOST/PORT by default; set upstream
`PROXY_URL` when using a public hostname or TLS reverse proxy. Streaming, uploads and WebSocket traffic are proxied
without application-level buffering. This is a sleep/wake overlay, not a sandbox,
resource limiter or multi-container autoscaler.

## Development and releases

Linux is required (process-group supervision). Run `go test -race ./...`,
`docker build -t browserless-ondemand:test .`, then `tests/smoke.sh`. The real-browser
smoke test checks cold start, a live WebSocket beyond the idle timeout, screenshot,
sleep and a second cold start. CI gates image publication; main produces `latest`
and `sha-<full-commit>` for ARM64 and AMD64. Go is cross-compiled, so the image
assembly requires no QEMU. The Go supervisor has no external Go dependencies.

Dependabot checks Docker base images (Browserless and Go), the Go module and
GitHub Actions. Dependabot updates, including major updates, are eligible for
auto-merge only after required CI passes. The privileged auto-merge workflow
never checks out PR content. A separate successful-CI `workflow_run` publishes
images, including after bot merges, without relying on a bot-triggered push.

## License

The original Go supervisor and repository tooling are MIT licensed (see LICENSE).
Browserless and the contents of its base image retain their upstream licenses;
this overlay does not relicense them. Browserless is distributed under
[SSPL-1.0 or its commercial license](https://github.com/browserless/browserless/blob/main/LICENSE).

# Jev MCP Hub

[English](README.md) | [&#31616;&#20307;&#20013;&#25991;](README.zh-CN.md)

Jev MCP Hub is a small Go gateway that exposes TypeSafe's Jev model through the Model Context Protocol. It forwards each caller's TypeSafe API key to `https://api.typesafe.ai/v1/systemone` and does not store, log, or share keys between sessions.

It exposes four MCP tools:

- `jev_noul` - yes/no probability
- `jev_choice` - one option with probabilities and confidence
- `jev_score` - probability-weighted score across ordered levels
- `jev_evaluate` - several named questions in one request

The request and answer shapes follow TypeSafe's System One API: `state`, `model` (defaults to `jev-latest`), and typed `questions`. See the [TypeSafe API reference](https://docs.typesafe.ai/api) for the full question shape.

## Run with Docker

```bash
docker compose up -d --build
curl http://127.0.0.1:8080/healthz
```

The compose file binds to loopback by default. Set `JEV_BIND_IP` and `JEV_PORT` only when an HTTPS reverse proxy is in front of the service. The MCP endpoint is `http://127.0.0.1:8080/mcp` for local use.

Docker builds pull the Go and distroless base images through DaoCloud and pin both multi-platform manifests by digest for reproducible builds.

## One-token client setup

Create the local setup file and replace its placeholder with the TypeSafe API key from the user:

```bash
go run ./cmd/jev-mcphub init --config jev-client.json
# edit jev-client.json: token = your TypeSafe API key
go run ./cmd/jev-mcphub install --client all --config jev-client.json
```

PowerShell users can run `scripts/install.ps1 -Client all -Config .\jev-client.json`; Unix users can run `sh scripts/install.sh all ./jev-client.json`. Each config write is atomic, creates a timestamped `*.backup-*` file when a config already exists, preserves unrelated configuration values, and requests owner-only permissions where the operating system supports them.

The installer writes:

```toml
# ~/.codex/config.toml
[mcp_servers.jev]
url = "https://your-host.example/mcp"
http_headers = { Authorization = "Bearer YOUR_TYPESAFE_TOKEN" }
```

The Claude Code file (`~/.claude.json`) contains:

```json
{"mcpServers":{"jev":{"type":"http","url":"https://your-host.example/mcp","headers":{"Authorization":"Bearer YOUR_TYPESAFE_TOKEN"}}}}
```

For a public deployment, use an HTTPS URL. The server rejects browser-origin requests, limits request bodies to 1 MiB, uses stateless Streamable HTTP, and accepts only an `Authorization: Bearer ...` header. It retries TypeSafe `429` and `529` responses with bounded backoff.

HTTP requests and MCP tool calls are emitted as JSON logs to stderr/Docker logs. Prometheus-compatible HTTP and tool counters and duration metrics are available at `GET /metrics`; logs and metric labels contain no tokens or request content.

## Local development

```bash
go test ./...
go vet ./...
go run ./cmd/jev-mcphub serve
```

HTTP mode reads `TYPESAFE_BASE_URL` (default `https://api.typesafe.ai/v1`) and `JEV_ADDR` (default `0.0.0.0:8080`). A stdio mode is available for local clients that cannot use remote HTTP:

```bash
TYPESAFE_API_KEY=... go run ./cmd/jev-mcphub serve --transport stdio
```

Do not commit `jev-client.json`, API keys, `.env` files, or generated backups. `.gitignore` already excludes them.

## Project layout

`internal/typesafe` contains the upstream client and validation, `internal/hub` contains MCP tools and HTTP hardening, `internal/install` performs safe Codex/Claude config merges, and `cmd/jev-mcphub` contains the CLI and server lifecycle.

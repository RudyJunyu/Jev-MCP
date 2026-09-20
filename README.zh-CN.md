# Jev MCP Hub

[English](README.md) | [简体中文](README.zh-CN.md)

Jev MCP Hub 是一个轻量的 Go 网关，通过模型上下文协议（MCP）提供 TypeSafe Jev 模型能力。服务会将每位调用者自己的 TypeSafe API Key 转发至 `https://api.typesafe.ai/v1/systemone`，不会存储、记录或在不同会话之间共享密钥。

项目提供四个 MCP 工具：

- `jev_noul` - 返回是／否问题的概率
- `jev_choice` - 从候选项中选择一个结果，并返回概率分布与置信度
- `jev_score` - 按有序等级进行评分，并返回概率加权分数
- `jev_evaluate` - 在单次请求中执行多个具名问题

请求和响应结构遵循 TypeSafe System One API，主要字段包括 `state`、`model`（默认使用 `jev-latest`）和类型化的 `questions`。完整的问题结构可参考 [TypeSafe API 文档](https://docs.typesafe.ai/api)。

## 使用 Docker 运行

```bash
docker compose up -d --build
curl http://127.0.0.1:8080/healthz
```

Compose 默认只绑定本机回环地址。只有在服务前配置了 HTTPS 反向代理时，才应修改 `JEV_BIND_IP` 和 `JEV_PORT`。本地使用的 MCP 地址为 `http://127.0.0.1:8080/mcp`。

## 只需 Token 的客户端配置

先生成本地配置文件，然后将其中的占位符替换为用户自己的 TypeSafe API Key：

```bash
go run ./cmd/jev-mcphub init --config jev-client.json
# 编辑 jev-client.json，只需替换 token
go run ./cmd/jev-mcphub install --client all --config jev-client.json
```

PowerShell 用户可以运行 `scripts/install.ps1 -Client all -Config .\jev-client.json`；Unix 用户可以运行 `sh scripts/install.sh all ./jev-client.json`。每个配置文件都会原子写入；原配置存在时会生成带时间戳的 `*.backup-*` 备份；安装过程会保留其他配置项，并在操作系统支持时设置为仅文件所有者可读写。

安装器会写入以下 Codex 配置：

```toml
# ~/.codex/config.toml
[mcp_servers.jev]
url = "https://your-host.example/mcp"
http_headers = { Authorization = "Bearer YOUR_TYPESAFE_TOKEN" }
```

Claude Code 的 `~/.claude.json` 配置如下：

```json
{"mcpServers":{"jev":{"type":"http","url":"https://your-host.example/mcp","headers":{"Authorization":"Bearer YOUR_TYPESAFE_TOKEN"}}}}
```

公网部署必须使用 HTTPS。服务会拒绝来自浏览器 Origin 的请求，将请求体限制为 1 MiB，使用无状态 Streamable HTTP，并且只接受 `Authorization: Bearer ...` 请求头。遇到 TypeSafe 返回 `429` 或 `529` 时，服务会执行次数和时长受限的退避重试。

## 本地开发

```bash
go test ./...
go vet ./...
go run ./cmd/jev-mcphub serve
```

HTTP 模式支持以下环境变量：`TYPESAFE_BASE_URL`（默认值为 `https://api.typesafe.ai/v1`）和 `JEV_ADDR`（默认值为 `0.0.0.0:8080`）。对于不支持远程 HTTP MCP 的本地客户端，也可以使用 stdio 模式：

```bash
TYPESAFE_API_KEY=... go run ./cmd/jev-mcphub serve --transport stdio
```

请勿提交 `jev-client.json`、API Key、`.env` 文件或自动生成的备份文件；项目的 `.gitignore` 已经排除了这些内容。

## 项目结构

`internal/typesafe` 包含上游 API 客户端和输入校验；`internal/hub` 包含 MCP 工具及 HTTP 安全处理；`internal/install` 负责安全合并 Codex 和 Claude Code 配置；`cmd/jev-mcphub` 包含命令行入口和服务生命周期管理。

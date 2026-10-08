# Omniroute WhatsApp集成服务 (GoWA / whatsmeow)

[English](README.md) | [Indonesian](README.id.md)

---

基于**Golang**构建的自托管WhatsApp集成服务，使用[whatsmeow](https://github.com/tulir/whatsmeow)库，连接**Omniroute AI API**，并自动将对话历史上下文存储在**PostgreSQL**数据库中。

采用模块化架构（*Clean Architecture / 标准Go项目布局*）、**Gin**框架、**pgxpool**连接池以及异步*Worker Pool*，确保在生产环境中具有高响应性和可靠性。

---

## 🌟 核心功能

1. **WhatsApp会话持久化**：
   - 已注册的会话直接存储在PostgreSQL内部表（`sqlstore`）或本地SQLite文件中。
   - 应用重启时会话保持连接（*持久登录*）。

2. **两种QR码显示模式**：
   - **终端控制台**：启动时自动打印，使用UTF-8半块字符表示。
   - **网页浏览器（`GET /qr`）**：现代HTML仪表板动态显示QR码（base64 PNG格式），具备自动刷新和自动轮询配对状态功能。直接图片端点位于`GET /qr/image`。

3. **PostgreSQL聊天上下文管理**：
   - `chat_histories`表存储对话历史（`user`与`assistant`消息）。
   - 查询检索最近`N`条消息并按时间升序排序，用于AI上下文提示。
   - 配备*复合索引*`(phone_number, created_at DESC, id DESC)`，实现亚毫秒级查询性能。

4. **异步处理（Worker Pool模式）**：
   - 通过whatsmeow事件处理器接收传入消息事件，过滤群组/广播消息和 outbound 消息。
   - 由受管goroutine worker池处理：调用Omniroute AI、保存到数据库、将响应发送回WhatsApp。

5. **智能文本分块**：
   - 超过WhatsApp限制（~4,000字符）的AI回复消息会智能分块，基于段落（`\n\n`）、换行符（`\n`）、句子结尾（`. `、`! `、`? `）或单词空格，不会断开单词或多字节/emoji字符。

6. **优雅关闭**：
   - 正确处理`SIGINT`和`SIGTERM`信号：停止Gin HTTP服务器、完成worker池中的in-flight任务（`sync.WaitGroup`）、断开WhatsApp连接、干净关闭PostgreSQL连接池。

---

## 🏗️ 项目目录结构

```
omniroute-api-wa/
├── cmd/
│   └── api/
│       └── main.go                 # 应用入口点 & 生命周期编排
├── internal/
│   ├── config/
│   │   ├── config.go              # 环境变量解析器 & 验证器
│   │   └── config_test.go         # 配置单元测试
│   ├── domain/
│   │   └── chat.go                # 领域实体、Omniroute & WhatsApp DTO
│   ├── repository/
│   │   └── postgres/
│   │       └── chat_repository.go # PostgreSQL查询层（pgxpool）
│   ├── service/
│   │   ├── omniroute_service.go   # Omniroute AI HTTP客户端集成
│   │   ├── omniroute_service_test.go
│   │   └── gowa_service.go        # whatsmeow客户端、QR流式处理 & 发送器
│   ├── delivery/
│   │   └── http/
│   │       ├── handler.go         # Gin控制器（/health、/status、/qr）
│   │       └── middleware.go      # 日志、CORS、恐慌恢复
│   └── worker/
│       └── pool.go                # 有界Worker Pool & AI管道
├── pkg/
│   └── textutil/
│       ├── chunker.go             # WhatsApp长消息拆分辅助工具
│       └── chunker_test.go        # 文本分块单元测试
├── migrations/
│   └── 000001_create_chat_histories_table.up.sql # PostgreSQL模式DDL
├── docker-compose.yml              # PostgreSQL & App容器配置
├── Dockerfile                      # 多阶段生产构建
├── .env.example                    # 环境配置模板
├── go.mod
└── go.sum
```

---

## ⚙️ 环境变量（.env）

> **注意**：将`.env.example`复制为`.env`并填写适当值。`.env`文件**不得**提交到存储库（已列在`.gitignore`中）。

| 变量 | 类型 | 默认值 | 必填 | 描述 |
|---|---|---|:---:|---|
| `APP_PORT` | String | `8080` | | HTTP Gin服务器端口。使用`APP_PORT`（而非`PORT`）以避免与Omniroute CLI默认端口（`20128`）冲突 |
| `GIN_MODE` | String | `release` | | Gin模式：`debug`或`release` |
| `DB_HOST` | String | — | ✅ | PostgreSQL服务器主机 |
| `DB_PORT` | String | — | ✅ | PostgreSQL服务器端口（通常为`5432`） |
| `DB_USER` | String | — | ✅ | PostgreSQL数据库用户名 |
| `DB_PASSWORD` | String | — | ✅ | PostgreSQL数据库密码 |
| `DB_NAME` | String | — | ✅ | PostgreSQL数据库名称 |
| `DB_SSLMODE` | String | `disable` | | PostgreSQL连接SSL模式（`disable`、`require`、`verify-full`） |
| `OMNIROUTE_API_BASE_URL` | String | — | ✅ | Omniroute AI基础端点。Omniroute CLI默认端口：`http://localhost:20128/v1` |
| `OMNIROUTE_API_KEY` | String | — | | Omniroute身份验证API密钥（如果服务器未配置则为可选） |
| `OMNIROUTE_MODEL` | String | `auto` | | 要使用的AI模型名称（`auto`表示自动选择） |
| `MAX_CONTEXT_MESSAGES` | Integer | `10` | | 作为上下文发送到AI的最近消息数量 |
| `WHATSAPP_SESSION_STORE` | String | `postgres` | | WhatsApp会话存储介质：`postgres`或`sqlite` |
| `WHATSAPP_SQLITE_PATH` | String | `whatsapp_session.db` | | SQLite文件路径（仅在`WHATSAPP_SESSION_STORE=sqlite`时适用） |
| `WORKER_POOL_SIZE` | Integer | `5` | | 并行处理AI消息的goroutine worker数量 |
| `WORKER_QUEUE_SIZE` | Integer | `100` | | 传入消息的缓冲通道容量 |

---

## 🚀 快速开始

### 前置要求

- [Go](https://go.dev/dl/) 1.21+
- [Docker](https://www.docker.com/) & Docker Compose（用于运行PostgreSQL）
- 本地机器运行Omniroute CLI（默认端口`20128`）

### 1. 环境配置

```bash
# 复制配置模板
cp .env.example .env

# 编辑.env并填写适当值（DB_PASSWORD、OMNIROUTE_API_KEY等）
```

### 2. 通过Docker Compose运行PostgreSQL数据库

```bash
# 仅运行PostgreSQL容器
docker compose up -d postgres

# 或一次性运行整个堆栈（PostgreSQL + App）
docker compose up -d
```

> **注意**：通过Docker运行`app`时，`DB_HOST`自动设置为`postgres`（Docker网络中的服务名称）。确保`OMNIROUTE_API_BASE_URL`指向`http://host.docker.internal:20128/v1`，使容器能够访问主机上的Omniroute CLI。

### 3. 运行Golang应用程序（本地）

```bash
# 下载依赖项
go mod download

# 运行服务
go run ./cmd/api/main.go
```

### 4. WhatsApp身份验证 / 配对

1. 服务首次启动时，QR码会自动打印到终端。
2. 打开浏览器并访问：
   ```
   http://localhost:8080/qr
   ```
3. 从手机WhatsApp扫描QR码：
   - 打开**设置 / 三个点** → **已连接的设备** → **连接设备**。
   - 将摄像头对准浏览器或终端显示的QR码。
4. 成功配对后，网页会自动将状态更新为**WhatsApp已连接**并重定向到`/status`。

---

## 📡 HTTP端点列表

| 方法 | 端点 | 描述 |
|---|---|---|
| `GET` | `/` | 根据登录状态重定向到`/qr`或`/status` |
| `GET` | `/health` | DB和WhatsApp连接组件的健康检查 |
| `GET` | `/status` | 应用状态、会话JID、DB和配置的JSON详情 |
| `GET` | `/qr` | Web HTML自动刷新QR码 / 已连接状态显示 |
| `GET` | `/qr?format=json` | 以JSON格式检索原始QR字符串数据 |
| `GET` | `/qr/image` | 以纯PNG格式检索QR码图片 |

---

## 🧪 运行测试（单元测试）

```bash
go test -v ./...
```

输出：
```
=== RUN   TestConfigDefaults
--- PASS: TestConfigDefaults (0.00s)
=== RUN   TestConfigPostgresDSN
--- PASS: TestConfigPostgresDSN (0.00s)
=== RUN   TestOmnirouteService_GenerateResponse
--- PASS: TestOmnirouteService_GenerateResponse (0.00s)
=== RUN   TestOmnirouteService_FallbackResponse
--- PASS: TestOmnirouteService_FallbackResponse (0.00s)
=== RUN   TestChunkTextShort
--- PASS: TestChunkTextShort (0.00s)
=== RUN   TestChunkTextLongParagraph
--- PASS: TestChunkTextLongParagraph (0.00s)
=== RUN   TestChunkTextUnicodeRunes
--- PASS: TestChunkTextUnicodeRunes (0.00s)
PASS
```
所有测试均无错误通过。

# assan-server-store

个人日常工具的 Go 后端，配套前端为 [`assan-fe-store`](https://github.com/Assan233/assan-fe-store)。使用标准库 `net/http`、`log/slog` 和 Go Modules，可直接用 GoLand 打开。部署目标为支持 Docker 的 NAS（`linux/amd64`、`linux/arm64`）。

## 本地开发

要求 Go 1.26+；部署使用 Docker Engine / Desktop 和 Compose v2。Make 为可选便利工具。

```sh
go run ./cmd/server
# 或 make run
curl http://localhost:8080/healthz
curl http://localhost:8080/api/v1/ping
```

GoLand：打开仓库根目录，选择 Go 1.26+ SDK，以 `./cmd/server` 为运行包，工作目录为仓库根目录；环境变量可填入 Run Configuration。IDE 的个人配置不入库。

```sh
make fmt       # 格式化
make check     # go vet、race 测试、格式检查
make build     # 生成 bin/server
```

无 Make 时可分别运行 `gofmt -w cmd internal`、`go vet ./...`、`go test -race ./...` 和 `go build -o bin/server ./cmd/server`。Race 检测需要支持的平台及 C 编译器。密码哈希使用 `x/crypto/argon2`，SQLite 使用纯 Go 的 `modernc.org/sqlite`。

## 项目结构

```text
cmd/server/            进程入口、依赖组装、信号处理与优雅退出
internal/config/       环境配置及启动校验
internal/auth/         SQLite 用户、密码哈希和会话
internal/httpapi/      HTTP 路由及接口测试
docs/api.md            前后端接口约定
.github/workflows/     Go 检查与双架构镜像构建 CI
Dockerfile             多阶段、非 root 镜像
compose.yaml           NAS 部署入口
AGENTS.md              仓库级 AI 协作规则
```

认证用户及会话存储在 SQLite 数据库中，Compose 命名卷 `assan-data` 持久保存 `/data/assan.db`。备份 NAS 上的 Docker volume 以保护账户数据。业务包与 HTTP 层分离；Go 的 `internal` 边界防止实现被外部模块导入。

## 配置

进程直接读取环境变量，**不会自动加载 `.env`**。本地开发时若未设置 `MUSIC_CREDENTIAL_KEY` 环境变量，会从工作目录的 `.env.local` 读取该密钥（仅此变量）；文件被 Git 和 Docker 构建忽略。环境变量优先。非法配置会导致启动失败。

| 变量 | 默认值 | 用途 |
| --- | --- | --- |
| `HTTP_PORT` | `8080` | 监听所有网卡的端口，范围 1–65535 |
| `LOG_LEVEL` | `INFO` | slog 级别，通常使用 DEBUG / INFO / WARN / ERROR |
| `SHUTDOWN_TIMEOUT` | `10s` | 等待正在处理的请求结束的正数时长 |
| `DATABASE_PATH` | `data/assan.db` | SQLite 数据库文件路径 |
| `SECURE_COOKIES` | `false` | 通过 HTTPS 部署时设为 `true`，只经 TLS 发送认证 Cookie |
| `TRUST_PROXY` | `false` | 仅当 API 无法绕过可信反向代理访问时设为 `true`，从 `X-Real-IP` 获取限流来源 |
| `MUSIC_CREDENTIAL_KEY` | 空（禁用音乐凭据接口） | Base64 编码的 32 字节随机密钥，使用 `openssl rand -base64 32` 生成，更新部署时保持不变 |

本地只需在后端仓库的 `.env.local` 中写入一次 `MUSIC_CREDENTIAL_KEY=生成的密钥`，之后在后端仓库根目录运行 `make run`、`go run ./cmd/server` 或配置 GoLand 工作目录为仓库根目录即可。请妥善备份该文件；不要提交密钥，也不要在每次启动时重新生成。

```sh
HTTP_PORT=9090 LOG_LEVEL=DEBUG go run ./cmd/server
```

服务以 JSON 格式向 stdout 输出生命周期日志，HTTP 服务错误也接入 slog。收到 SIGINT/SIGTERM 后停止接收连接并等待请求结束；超时后强制关闭并以非零状态退出。默认请求头读取 5 秒、请求读取 15 秒、响应写入 30 秒、空闲连接 60 秒；将来加入大文件或流式接口时需针对这些限制设计。

## Docker / NAS 部署

```sh
cp .env.example .env
docker compose config --quiet
docker compose up -d --build
docker compose ps
docker compose logs -f api
curl http://127.0.0.1:8080/healthz
docker compose down
```

Compose 自动使用 `.env` 插值：`HOST_PORT` 控制宿主机端口，`BIND_ADDRESS` 默认 `127.0.0.1`，`LOG_LEVEL` 传给进程，`SECURE_COOKIES=true` 适用于 HTTPS 反向代理。容器端口固定为 8080，退出等待为 10 秒，Compose 留出 15 秒停止时间。SQLite 数据位于命名卷 `assan-data`，`docker compose down` 不会删除该卷。

- NAS 宿主机上的反向代理可转发到 `127.0.0.1:8080`。若代理运行在另一个容器中，应接入共享 Docker 网络并访问 `api:8080`，而不是代理容器自己的 localhost。
- 建议通过提供 HTTPS 的 NAS 反向代理公开服务，并保持 `SECURE_COOKIES=true`。仅在可信内网测试时才考虑直接使用 HTTP（此时需设 `SECURE_COOKIES=false`）。
- 单独部署本 API 时保持 `TRUST_PROXY=false`；通过前端 Compose 内网访问时启用 `TRUST_PROXY=true`，且 API 不得向不可信网络公开。NAS 入口反向代理必须覆盖 `X-Real-IP` 为真实客户端 IP；前端 Nginx 仅转发该受信值，避免所有外部用户共用反代 IP 的限流额度。
- 前端开发服务器将 `/api` 代理到 `http://localhost:8080`，生产代理保留 `/api/v1/...` 完整路径。当前没有启用跨域策略。
- 镜像以 UID/GID `10001:10001` 运行；Compose 设置只读根文件系统、临时 `/tmp`、移除 capabilities 和日志轮转。
- Docker 定期检查 `/healthz`。`restart: unless-stopped` 对进程退出生效，单纯 unhealthy 状态不会自动重启容器。
- Auth API 提供首次管理员初始化、登录、当前用户、登出及修改密码；不再公开注册。管理员可添加、启用、禁用、删除子账号和重置子账号密码，管理接口强制校验角色。账号保存在本机 SQLite 数据卷，不依赖外部认证服务。认证依赖 HttpOnly 会话 Cookie 和 CSRF token，部署者应在开放访问前完成首次初始化。旧安装迁移/清空步骤见 `docs/api.md`。
- 忘记密码时，可在交互终端运行 `docker compose exec -it api /usr/local/bin/reset-password USERNAME`；输入不会回显，重置会撤销该账户的全部会话。
- 飞牛音乐集成按用户加密存储音乐登录凭据。前端 Compose 通过 `.env` 提供 `MUSIC_CREDENTIAL_KEY`；备份时同时安全保管该密钥，丢失或轮换后需要用户重新保存音乐账号。接口、安全边界与 Cookie 行为见 `docs/api.md`。

### 多架构镜像

在开发机为单一 NAS 架构构建并加载：

```sh
docker buildx build --platform linux/arm64 -t assan-server-store:local --load .
# Intel/AMD NAS 使用 linux/amd64
```

需要发布双架构镜像时，先登录自己的镜像仓库，然后替换下面的仓库地址及版本：

```sh
docker buildx build --platform linux/amd64,linux/arm64 \
  -t <registry>/<namespace>/assan-server-store:<version> --push .
```

NAS 使用预构建镜像时，将 Compose 的 `image` 改为发布地址并移除 `build`，随后运行 `docker compose pull && docker compose up -d`。当前 CI 只校验双架构构建，不自动发布。基础镜像跟踪指定 Go 小版本和 Alpine 发行版；需要严格可重现发布时应锁定经验证的镜像 digest 并定期更新。

## 接口与协作

接口路径、响应与前端联调说明见 [docs/api.md](docs/api.md)。仓库级规则见 [AGENTS.md](AGENTS.md)，HTTP 子目录有补充规则。现有 MIT 授权见 [LICENSE](LICENSE)。

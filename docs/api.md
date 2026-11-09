# HTTP API

此文档是 `assan-fe-store` 的前后端接口契约。所有业务接口使用 JSON，除特别说明外成功响应为 `{"data":...}`。

## 已提供接口

| 方法 | 路径 | 成功响应（200） |
| --- | --- | --- |
| GET | `/healthz` | `{"status":"ok"}` |
| GET | `/api/v1/ping` | `{"data":{"message":"pong"}}` |
| GET | `/api/v1/auth/csrf` | `{"data":{"token":"..."}}` |
| POST | `/api/v1/auth/signup` | `{"data":{"id":"...","username":"..."}}` |
| POST | `/api/v1/auth/login` | `{"data":{"id":"...","username":"..."}}` |
| GET | `/api/v1/auth/me` | `{"data":{"id":"...","username":"..."}}` |
| POST | `/api/v1/auth/logout` | `204 No Content` |
| POST | `/api/v1/auth/password` | `204 No Content` |

成功 JSON 为 `application/json; charset=utf-8`。错误响应格式为 `{"error":{"code":"...","message":"..."}}`，并使用标准 HTTP 状态码；未知路径及不支持的方法由 ServeMux 返回 404/405。JSON 请求体最多 16 KiB，拒绝未知字段和尾随 JSON 值。Go ServeMux 的 GET 路由也支持 HEAD。

## 认证

- 注册请求：`{"username":"...","password":"..."}`。用户名为 3–32 位 ASCII 字母、数字或下划线；密码为 12–128 个字符。开放注册，不要求邮箱。
- 登录使用同一请求体；失败返回 401 `invalid_credentials`。注册重复用户名返回 409 `username_exists`。
- 注册和登录返回用户 `id`、`username`，并设置 7 天 `assan_session` HttpOnly、SameSite=Lax Cookie。Cookie 仅保存随机会话凭证；数据库只保存凭证哈希。生产环境应在 HTTPS 反向代理后设置 `SECURE_COOKIES=true`。
- 所有 POST 请求（包括注册、登录、登出和改密）需要先 GET `/api/v1/auth/csrf`，随后携带返回的 `data.token` 于 `X-CSRF-Token` 请求头；浏览器同时自动发送 `assan_csrf` Cookie。失败返回 403 `csrf_failed`。
- 登录/注册按来源 IP 限制 15 分钟最多 10 次，超出返回 429 `rate_limited`。默认使用 TCP 连接来源地址。只有 API 被网络隔离、确保仅能经可信反向代理访问时才设置 `TRUST_PROXY=true`；此模式信任 `X-Real-IP`。NAS 入口反向代理必须覆盖该请求头为真实客户端 IP；前端 Compose 中的 Nginx 仅转发此值，API 也不得向不可信网络公开。
- 改密请求：`{"currentPassword":"...","newPassword":"..."}`，需要有效会话及 CSRF。成功后更新密码并撤销该用户全部会话，当前浏览器需要重新登录。
- 登出会撤销当前会话，并清除会话和 CSRF Cookie。忘记密码由运维人员处理，不提供公开重置接口。

`/healthz` 仅表示 HTTP 进程可响应，不检查外部依赖。后续引入数据库等依赖时，独立增加 readiness 语义，避免依赖故障导致 liveness 重启循环。

## 扩展约定

- 业务接口统一位于 `/api/v1`，成功响应以 `data` 包装；使用标准 HTTP 状态码。
- 认证与业务错误结构已定义；增加其他业务时按需定义分页等契约。
- 当前无 CORS 中间件。前端通过同源 `/api` 代理访问，代理应保留完整路径。
- 接口变更应同步本文件和行为测试，并核对 `../assan-fe-store` 中的消费者。
- 破坏性变更在本文件记录迁移步骤；无法兼容时考虑新主版本路径。

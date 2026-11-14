# HTTP API

此文档是 `assan-fe-store` 的前后端接口契约。所有业务接口使用 JSON，除特别说明外成功响应为 `{"data":...}`。

## 已提供接口

| 方法 | 路径 | 成功响应（200） |
| --- | --- | --- |
| GET | `/healthz` | `{"status":"ok"}` |
| GET | `/api/v1/ping` | `{"data":{"message":"pong"}}` |
| GET | `/api/v1/auth/csrf` | `{"data":{"token":"..."}}` |
| GET | `/api/v1/auth/setup` | `{"data":{"needsSetup":true}}` |
| POST | `/api/v1/auth/setup` | `{"data":{"id":1,"username":"...","role":"admin","disabled":false}}` |
| POST | `/api/v1/auth/login` | `{"data":{"id":1,"username":"...","role":"admin","disabled":false}}` |
| GET | `/api/v1/auth/me` | 同登录用户结构 |
| POST | `/api/v1/auth/logout` | `204 No Content` |
| POST | `/api/v1/auth/password` | `204 No Content` |
| GET | `/api/v1/admin/users` | `{"data":[{"id":2,"username":"...","role":"member","disabled":false}]}` |
| POST | `/api/v1/admin/users` | `204 No Content` |
| POST | `/api/v1/admin/users/{id}` | `204 No Content` |
| GET | `/api/v1/music/access` | `204 No Content`（Nginx 播放器鉴权子请求） |
| GET | `/api/v1/music/credential` | `{"data":{"configured":false}}` |
| GET | `/api/v1/music/credential?login` | `{"data":{"username":"...","password":"..."}}` |
| POST | `/api/v1/music/credential` | `204 No Content` |
| DELETE | `/api/v1/music/credential` | `204 No Content` |
| POST | `/api/v1/music/commands` | `204 No Content` |
| GET | `/api/v1/music/commands/next` | `{"data":{"action":"next"}}` 或 `{"data":{"action":null}}` |

成功 JSON 为 `application/json; charset=utf-8`。错误响应格式为 `{"error":{"code":"...","message":"..."}}`，并使用标准 HTTP 状态码；未知路径及不支持的方法由 ServeMux 返回 404/405。JSON 请求体最多 16 KiB，拒绝未知字段和尾随 JSON 值。Go ServeMux 的 GET 路由通常也支持 HEAD，但领取音乐操作的接口仅接受 GET，以免 HEAD 请求消耗队列。

## 认证

- 初始化请求：`{"username":"...","password":"..."}`。用户名为 3–32 位 ASCII 字母、数字或下划线；密码至少 8 个字符且不超过 128 字节（UTF-8），必须同时包含 ASCII 大写字母、小写字母、数字和特殊字符（标点或符号，空格不算）。此规则适用于初始化、创建子账号、修改密码和所有密码重置入口。仅首次初始化成功，并在同一事务中创建唯一管理员和安装标记。重复初始化返回 409 `setup_closed`；服务重启不重开入口。
- 登录使用同一请求体；失败（包括禁用账号）返回 401 `invalid_credentials`。添加子账号用户名重复返回 409 `username_exists`。
- 初始化和登录返回用户数字 `id`、`username`、`role`（admin/member）、`disabled`，并设置 7 天 `assan_session` HttpOnly、SameSite=Lax Cookie。Cookie 仅保存随机会话凭证；数据库只保存凭证哈希。生产环境应在 HTTPS 反向代理后设置 `SECURE_COOKIES=true`。
- 除公开的 `/api/v1/music/commands` 外，所有 POST 请求（包括初始化、登录、登出和改密）需要先 GET `/api/v1/auth/csrf`，随后携带返回的 `data.token` 于 `X-CSRF-Token` 请求头；浏览器同时自动发送 `assan_csrf` Cookie。失败返回 403 `csrf_failed`。
- 登录/初始化按来源 IP 限制 15 分钟最多 10 次，超出返回 429 `rate_limited`。默认使用 TCP 连接来源地址。只有 API 被网络隔离、确保仅能经可信反向代理访问时才设置 `TRUST_PROXY=true`；此模式信任 `X-Real-IP`。NAS 入口反向代理必须覆盖该请求头为真实客户端 IP；前端 Compose 中的 Nginx 仅转发此值，API 也不得向不可信网络公开。
- 改密请求：`{"currentPassword":"...","newPassword":"..."}`，需要有效会话及 CSRF。成功后更新密码并撤销该用户全部会话，当前浏览器需要重新登录。
- 登出会撤销当前会话，并清除会话和 CSRF Cookie。子账号忘记密码由管理员处理，管理员忘记密码使用本机 CLI，不提供公开重置接口。

## 飞牛音乐集成

音乐访问和凭据接口均需有效应用库会话（未登录 401），未配置 `MUSIC_CREDENTIAL_KEY` 时凭据接口返回 503。保存请求为 `{"username":"...","password":"..."}`，用户名最多 128 字节、密码最多 512 字节；凭据的 POST 与 DELETE 都需要 `assan_csrf` Cookie 和 `X-CSRF-Token` 请求头。密码以 AES-256-GCM 加密，使用用户 ID 作为附加认证数据，每位用户独立保存；删除应用库用户会级联删除其音乐凭据。GET 状态只返回布尔值，`?login` 在自动登录时返回该用户的明文凭据，响应携带 `Cache-Control: no-store`。仅在可信的同源播放器上下文使用此接口。

前端 Nginx 在 `/music/` 下代理飞牛音乐资源，每个请求通过 `/api/v1/music/access` 校验当前会话；给上游的 Cookie 仅含 `music-token`。登录或登出应用库会清理浏览器中原先的 `music-token`。飞牛播放器与应用库同源，播放器脚本具有访问同源 API 的能力；需信任上游代码。密钥须在数据库之外长期保管，丢失后无法解密原有密码。

远程播放控制：POST `/api/v1/music/commands` 请求体为 `{"action":"previous"}`、`{"action":"next"}` 或 `{"action":"toggle"}`，无需登录、Cookie 或 CSRF，供 Native Tools 等客户端直接调用；无效操作返回 400 `invalid_input`，队列达到 256 条时返回 429 `queue_full`。GET `/api/v1/music/commands/next` 同样无需鉴权，HEAD 返回 405 且不消费操作；无操作时返回 `action:null`，响应不缓存。每次请求仅按入队顺序原子取走一个全局操作；返回即视为消费，即使播放器之后未执行也不重发。操作保存在 SQLite 中，可跨服务重启；入队后 **20 秒**到期，过期操作在下一次入队或领取时清除，不占用队列容量，也不会下发。升级时原有缺少入队时间的待消费操作视为过期，在下一次访问队列时清除。播放器控件就绪时每 2 秒轮询，播放器页面未打开或不可用时操作最多保留 20 秒。

## 用户管理

所有 `/api/v1/admin/users` 接口要求有效管理员会话：未登录 401，子账号 403。列表不包含密码哈希或盐。添加请求为 `{"username":"...","password":"..."}`，只能创建 member。

管理请求为 `{"action":"disable"}`、`{"action":"enable"}`、`{"action":"delete"}` 或 `{"action":"reset-password","password":"..."}`。只能操作子账号，不能删除、禁用或通过该接口重置管理员；所有操作撤销目标账号会话。无效 id/action/密码返回 400，操作管理员返回 403。管理员通过普通改密接口修改自己的密码。

## 破坏性变更与升级

公开 `/api/v1/auth/signup` 已移除（404）。前后端需同步升级。数据库版本 1 增加角色、禁用状态和安装标记；旧账号不会自动获得管理员权限，非空旧库会保持初始化入口关闭，防止他人抢占。

本次采用清空旧账号后重新初始化的迁移策略。在停止 API 后，对实际使用的 SQLite 数据库执行：

```sql
PRAGMA foreign_keys=ON;
BEGIN IMMEDIATE;
DELETE FROM sessions;
DELETE FROM users;
-- 仅当数据库已升级到版本 1 时执行下一行；旧版本无此表。
DELETE FROM installation;
COMMIT;
```

这些语句永久删除全部账号和会话；随后启动 API，首次访问设置管理员。本地开发库通常为 `data/assan.db`，Docker 为卷内 `/data/assan.db`，两者是独立数据。应使用 SQLite 客户端执行事务，不单独删除 WAL/SHM 文件。镜像升级不会自动清空账号，每次启动也不会重置数据。

`/healthz` 仅表示 HTTP 进程可响应，不检查外部依赖。后续引入数据库等依赖时，独立增加 readiness 语义，避免依赖故障导致 liveness 重启循环。

## 扩展约定

- 业务接口统一位于 `/api/v1`，成功响应以 `data` 包装；使用标准 HTTP 状态码。
- 认证与业务错误结构已定义；增加其他业务时按需定义分页等契约。
- 当前无 CORS 中间件。前端通过同源 `/api` 代理访问，代理应保留完整路径。
- 接口变更应同步本文件和行为测试，并核对 `../assan-fe-store` 中的消费者。
- 破坏性变更在本文件记录迁移步骤；无法兼容时考虑新主版本路径。

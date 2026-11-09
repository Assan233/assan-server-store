# HTTP API

此文档是初始化阶段的前后端接口契约。配套前端仓库目前尚未定义接口消费者。

## 已提供接口

| 方法 | 路径 | 成功响应（200） |
| --- | --- | --- |
| GET | `/healthz` | `{"status":"ok"}` |
| GET | `/api/v1/ping` | `{"data":{"message":"pong"}}` |

成功响应为 `application/json; charset=utf-8`，并设置 `X-Content-Type-Options: nosniff`。Go ServeMux 的 GET 路由也支持 HEAD（实际 HTTP 响应无 body）。未知路径为 404，不支持的方法为 405；这两种框架默认错误目前返回纯文本，而非业务 JSON。

`/healthz` 仅表示 HTTP 进程可响应，不检查外部依赖。后续引入数据库等依赖时，独立增加 readiness 语义，避免依赖故障导致 liveness 重启循环。

## 扩展约定

- 业务接口统一位于 `/api/v1`，成功响应以 `data` 包装；使用标准 HTTP 状态码。
- 第一项业务功能引入时，再定义错误码、JSON 错误结构、认证与分页等具体契约。
- 当前无 CORS 中间件。前端通过同源 `/api` 代理访问，代理应保留完整路径。
- 接口变更应同步本文件和行为测试，并核对 `../assan-fe-store` 中的消费者。
- 破坏性变更在本文件记录迁移步骤；无法兼容时考虑新主版本路径。

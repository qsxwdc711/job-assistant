# 后端开发与权限接口

S1 已实现账号、会话、角色权限、管理员命令和 PostgreSQL 迁移。岗位、投递、简历、推荐与飞书按后续阶段接入；`capabilities` 描述权限策略，不代表相应功能已经上线。

## 运行基线

- Go 1.26.8；`go.mod` 固定工具链，`go.sum` 固定依赖校验。
- PostgreSQL 18.6；Docker Compose 提供数据库。
- API 使用 `net/http`，会话存储在 PostgreSQL，不依赖外部缓存。

系统 PATH 内旧版 Go 可以通过 Go 的自动工具链机制使用 `go.mod` 指定的版本。离线时使用已经安装的 Go 1.26.8 可执行文件；不要将 `GOTOOLCHAIN=local` 与低于 1.26 的工具链混用。

## 本机启动：Go 服务加容器数据库

在仓库根目录执行。初始化脚本生成随机数据库密码、管理员密码和 32 字节 CSRF 密钥；已存在 `.env` 时拒绝覆盖。用户名和密码保存在本机被 Git 忽略的 `.env` 中。

```powershell
./scripts/new-local-env.ps1
docker compose --env-file .env -f deploy/compose.yaml -f deploy/compose.local.yaml up -d db
```

数据库绑定 `127.0.0.1:15432`，使用项目独立持久卷。等待数据库健康后，把配置加载到当前进程，再迁移和初始化管理员：

```powershell
foreach ($taskLine in (Get-Content .env)) {
    if ($taskLine -match '^([A-Z_]+)=(.*)$') {
        [Environment]::SetEnvironmentVariable($Matches[1], $Matches[2], 'Process')
    }
}
go -C backend run ./cmd/admin migrate
go -C backend run ./cmd/admin bootstrap
go -C backend run ./cmd/api
```

访问 `http://localhost:8080/health/ready` 可查看数据库就绪状态。当前提供 JSON 接口，网页登录和看板在 S2 实现。`bootstrap` 只创建第一个管理员；此后通过已登录管理员的接口创建其他账号。

API 启动会检查迁移版本和校验和。新增迁移先执行 `admin migrate`，不要修改已经应用的 SQL。缺失数据库配置或 CSRF 密钥会明确退出。

## 容器配置

`deploy/compose.yaml` 提供 db、api 和 tools profile 的 admin。容器内数据库 URL 应指向服务名 `db:5432`；上述本机 `.env` 指向 `127.0.0.1:15432`，需另外准备被 Git 忽略的 `.env.compose`，填写对应容器配置。

```powershell
docker compose --env-file .env.compose -f deploy/compose.yaml up -d db
docker compose --env-file .env.compose -f deploy/compose.yaml --profile tools run --rm admin migrate
docker compose --env-file .env.compose -f deploy/compose.yaml --profile tools run --rm admin bootstrap
docker compose --env-file .env.compose -f deploy/compose.yaml up -d api
```

Dockerfile 固定 Go 1.26.8，使用非 root 的静态程序运行镜像。2026-10-14 已验证 Compose 配置解析和 PostgreSQL 容器；Go 构建镜像下载未通过完整性校验，因此完整镜像构建尚未验证。本机 Go 构建、Linux 交叉构建和真实 API 启动均已通过。HTTPS、网页同源代理和云端部署属于 S5。

生产环境默认 `APP_ENV=production`，必须填写 HTTPS 的 `APP_ORIGIN`，Cookie 自动启用 Secure；本地 HTTP 运行必须显式使用 development。

## 已实现接口

| 方法与路径 | 用途 | 权限 |
|---|---|---|
| GET /health/live | 进程存活 | 无需登录 |
| GET /health/ready | 实际数据库连接 | 无需登录 |
| POST /api/v1/auth/login | 用户名、密码登录 | 无需登录 |
| GET /api/v1/auth/me | 当前用户、有效角色、能力和 CSRF | 已登录 |
| POST /api/v1/auth/logout | 撤销当前会话 | 已登录及 CSRF |
| GET /api/v1/auth/sessions | 本人的有效设备会话 | 已登录 |
| DELETE /api/v1/auth/sessions/{id} | 撤销设备会话 | 本人或管理员及 CSRF |
| GET /api/v1/admin/users | 分页查询账号 | 管理员 |
| POST /api/v1/admin/users | 创建账号 | 管理员及 CSRF |
| PATCH /api/v1/admin/users/{id}/role | 授予或撤销 VIP、变更角色 | 管理员及 CSRF |
| PATCH /api/v1/admin/users/{id}/disabled | 停用或启用账号 | 管理员及 CSRF |
| GET /api/v1/admin/audit-events | 分页审计记录 | 管理员 |

列表默认 20 条，`limit` 最大 100，`offset` 最大 1000000。写入 JSON 最大 16 KiB，未知字段、多段 JSON、错误类型均拒绝。

登录请求是 `{"username":"账号","password":"密码"}`；创建账号另可填写 `role` 和 `vip_expires_at`。用户名规范化为小写，允许 3–40 个 ASCII 字母、数字、点、下划线或连字符，首字符为字母或数字。新密码限制 12–72 UTF-8 字节，正式入口使用 bcrypt cost 12。

角色更新请求是 `{"role":"vip","vip_expires_at":"未来的 RFC3339 时间"}`；VIP 到期为空表示长期有效，`standard` 或 `admin` 必须没有到期时间。停用请求是 `{"disabled":true}`。最后一个启用的管理员不能被停用或降级，并发变更也受保护。

登录 Cookie 为 `ja_session`，HttpOnly、SameSite=Lax，固定 7 天有效；数据库只保存随机令牌的 SHA-256 摘要。登录和当前用户响应返回 `csrf_token`，后续写请求通过 `X-CSRF-Token` 提交。CSRF 来自会话令牌和服务端密钥的 HMAC，多标签页可重新读取。携带 Origin 的写请求必须匹配 `APP_ORIGIN`。

没有登录返回 401，能力不允许返回 403，访问其他用户的会话返回 404。停用账号删除全部设备会话；重新启用不会恢复旧会话。角色变更保留会话，但后续请求按数据库当前角色重新判断。账号和审计读取也会在事务内复核当前权限。

未来私有接口必须采用同样的能力与归属检查，并增加真实跨用户测试；后台任务须在执行和发布结果前重新检查用户状态、权限版本及相关业务版本。S1 尚未实现后台推荐任务。

## 测试与检查

```powershell
go -C backend test ./... -count=1
go -C backend vet ./...
```

没有 `TEST_DATABASE_URL` 时，集成测试会明确跳过，不能视为数据库验证通过。测试库名称必须以 `_test` 结尾，每个测试创建随机独立 schema，结束后只清理自己的 schema。禁止使用生产数据库或真实简历。

以下命令创建临时专用测试库，密码只经环境变量传入，不写在命令参数中：

```powershell
$taskBytes = New-Object byte[] 32
$taskRandom = [Security.Cryptography.RandomNumberGenerator]::Create()
$taskRandom.GetBytes($taskBytes)
$taskRandom.Dispose()
$env:POSTGRES_PASSWORD = [Convert]::ToBase64String($taskBytes).Replace('+','-').Replace('/','_').TrimEnd('=')
docker run --rm -d --name job-assistant-dev-test --label com.job-assistant.scope=dev-test --tmpfs /var/lib/postgresql -p 127.0.0.1:15433:5432 -e POSTGRES_USER=jobassistant_test -e POSTGRES_DB=jobassistant_test -e POSTGRES_PASSWORD postgres:18.6
docker exec job-assistant-dev-test pg_isready -U jobassistant_test -d jobassistant_test
$env:TEST_DATABASE_URL = 'postgres://jobassistant_test:' + $env:POSTGRES_PASSWORD + '@127.0.0.1:15433/jobassistant_test?sslmode=disable'
go -C backend test ./... -count=1 -timeout=120s
docker stop job-assistant-dev-test
```

确认 `pg_isready` 返回 accepting connections 后再测试。此测试容器使用临时文件系统，停止后删除；本机开发的持久数据库不受影响。运行本机开发服务时重新加载 `.env` 中的密码配置。

支持 C 编译器的环境另运行 `go -C backend test -race ./... -count=1`。本轮 Windows race 检查遇到 C 编译器 / DLL 错误，未取得通过结果，应在兼容的开发或 CI 环境补验。

本轮实际检查和审查结果见 [S1 验收记录](../docs/verification/2026-10-14-s1-identity.md)。

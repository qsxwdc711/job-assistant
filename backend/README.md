# 后端开发与权限接口

S1–S4 已实现账号、会话、角色权限、官方岗位采集、组合筛选、收藏、个人投递、私有简历、脱敏预览、确认资料版本、按方向的资格/语义匹配、每日调度、站内摘要与飞书发件箱。DeepSeek 与飞书已完成隔离模拟验收；真实模型和群送达未验收，本轮保持 `NOTIFICATIONS_SEND_ENABLED=false`。详见 [S3 说明](../docs/operations/s3-resumes.md)和 [S4 运行与接口说明](../docs/operations/s4-notifications.md)。

推荐从根目录使用 `./scripts/start-local.ps1` 启动完整网页与容器服务，使用 `./scripts/collect-local.ps1` 更新岗位，详见根 README。以下也保留单独运行 Go API 的方法。

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
$env:PRIVATE_STORAGE_DIR = [IO.Path]::GetFullPath('.cache/private')
# 原生启动需将 Poppler 的 pdfinfo、pdftotext 加入 PATH。
go -C backend run ./cmd/api
```

访问 `http://localhost:8080/health/ready` 可查看数据库就绪状态。设置 `WEB_ROOT` 为已构建的 `web/dist` 绝对目录后，同一 API 同源提供网页登录和看板；不设置则只提供接口。`bootstrap` 只创建第一个管理员；此后通过已登录管理员的接口创建其他账号。

API 启动会检查迁移版本和校验和。新增迁移先执行 `admin migrate`，不要修改已经应用的 SQL。缺失数据库配置或 CSRF 密钥会明确退出。

## 容器配置

`deploy/compose.yaml` 提供 db、api、worker 和 tools profile 的 admin、collector，共享 `job-assistant-local` 镜像。`compose.web.yaml` 将已构建网页只读挂载到 API。容器内数据库 URL 指向 `db:5432`；本机 `.env` 指向 `127.0.0.1:15432`，`start-local.ps1` 自动生成忽略的容器配置。手动部署则另准备 `.env.compose`。

```powershell
docker compose --env-file .env.compose -f deploy/compose.yaml up -d db
docker compose --env-file .env.compose -f deploy/compose.yaml --profile tools run --rm admin migrate
docker compose --env-file .env.compose -f deploy/compose.yaml --profile tools run --rm admin bootstrap
docker compose --env-file .env.compose -f deploy/compose.yaml up -d api worker
```

Dockerfile 固定官方 Go 1.26.8 bookworm 镜像及 SHA-256 摘要，最终使用固定 Debian slim 摘要、含 Poppler 的非 root 运行镜像。2026-10-14 已补验完整构建、PostgreSQL 与 API 容器运行、初始化、登录和退出，以及正常关闭。HTTPS、网页同源代理和云端部署属于 S5。

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
| GET /api/v1/jobs、/jobs/{id}、/jobs/filters | 组合筛选、方向详情、筛选字典 | 已登录 |
| GET /api/v1/me/favorites | 本人收藏分页 | 已登录 |
| GET /api/v1/me/job-states | 当前页岗位的本人收藏、投递状态；重复 job_id 参数，1–100 个 UUID | 已登录，仅本人 |
| PUT、DELETE /api/v1/me/favorites/{jobID} | 收藏或取消 | 已登录及 CSRF |
| GET、POST /api/v1/me/applications | 本人投递列表、创建 | 已登录；写入须 CSRF |
| GET、PATCH、DELETE /api/v1/me/applications/{id} | 本人投递读取、修改、删除 | 归属授权；写入须 CSRF |
| GET /api/v1/me/applications/{id}/events | 最近 100 条个人进度历史 | 归属授权 |
| /api/v1/admin/applications 及对应详情/历史 | 管理员跨账号查询和维护 | 当前管理员；写入须 CSRF |
| GET、PATCH /api/v1/admin/companies、/sources、/jobs | 管理公司、来源、岗位 | 当前管理员；PATCH 路径加 code 或 id，写入须 CSRF |

列表默认 20 条，`limit` 最大 100，`offset` 最大 1000000。写入 JSON 最大 16 KiB，未知字段、多段 JSON、错误类型均拒绝。

登录请求是 `{"username":"账号","password":"密码"}`；创建账号另可填写 `role` 和 `vip_expires_at`。用户名规范化为小写，允许 3–40 个 ASCII 字母、数字、点、下划线或连字符，首字符为字母或数字。新密码限制 12–72 UTF-8 字节，正式入口使用 bcrypt cost 12。

角色更新请求是 `{"role":"vip","vip_expires_at":"未来的 RFC3339 时间"}`；VIP 到期为空表示长期有效，`standard` 或 `admin` 必须没有到期时间。停用请求是 `{"disabled":true}`。最后一个启用的管理员不能被停用或降级，并发变更也受保护。

登录 Cookie 为 `ja_session`，HttpOnly、SameSite=Lax，固定 7 天有效；数据库只保存随机令牌的 SHA-256 摘要。登录和当前用户响应返回 `csrf_token`，后续写请求通过 `X-CSRF-Token` 提交。CSRF 来自会话令牌和服务端密钥的 HMAC，多标签页可重新读取。携带 Origin 的写请求必须匹配 `APP_ORIGIN`。

没有登录返回 401，能力不允许返回 403，访问其他用户的会话返回 404。停用账号删除全部设备会话；重新启用不会恢复旧会话。角色变更保留会话，但后续请求按数据库当前角色重新判断。账号和审计读取也会在事务内复核当前权限。

岗位查询支持 `q/company/direction/major/city/industry/nature/tier/graduation_year/recruitment_type/written_test/deadline_before/status/include_unknown/sort/limit/offset`，相同维度可重复传参或用逗号分隔（company 和 q 为单值）。方向、专业、城市在同一 variant 内匹配。毕业年份只是日期范围筛选，具体境内外、学历与优先条件请看详情。

投递创建需要 `job_id`，默认 `status=draft`；修改提交 `status/notes/next_action/next_action_at/version`，删除提交 `version`。重复创建为 409 `application_exists`，旧版本修改为 409 `version_conflict`。管理员接口可用 `owner_user_id` 指定归属；列表留空查询全部，创建必须指定 owner。岗位关闭或隐藏不清空个人记录。

私有接口采用当前能力与归属检查；已实现的后台推荐任务在执行和发布结果前重新检查权限及版本。通知同样在生成和发送前核验权限、配置及当前资料/岗位版本；接口见 S4 说明。

## 官方采集与状态

`collector all` 或指定 `tencent-campus/huawei-campus/cmb-campus/tencent-internships/huawei-internships`；生产镜像包含 `/app/collector`。每来源完整成功后原子发布，内容改变才增加版本。来源错误和空结果不关闭旧岗位；失踪记录标为待核验，明确不属于当前计算机范围的已存候选隐藏待复核，不伪造关闭。管理员手动隐藏不会被下一轮采集自动重新发布。

腾讯暂限常规校招、应届实习和日常实习中国地点，华为暂限应届与 INTERN 实习的研发计算机方向，招商暂限校招及实习列表。腾讯青云等专项尚未接入。两个实习来源独立于全职来源：腾讯日常实习不继承应届实习的毕业范围；华为“主要面向2027届”保留为偏好，不按仅限该届排除。未披露的实习时长、出勤和笔试保持未知，推荐仍需本人确认资料。新增来源先执行 `admin migrate`，再使用 `./scripts/collect-local.ps1 -Source tencent-internships` 或 `-Source huawei-internships`。S4 worker 会在北京时间 08:00 后为当天启用来源排队；自动采集与 CLI 共享域名锁和三个全局读取名额。接口变更、过期类别 ID 或未知关闭业务码会作为来源异常保留旧数据。没有通用关闭推断。Node 和浏览器不进入生产 Go 镜像。

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

支持 C 编译器的环境另运行 `go -C backend test -race ./... -count=1`。Windows 本机检查遇到 C 编译器 / DLL 错误；已在 Linux amd64、Go 1.26.8 和 GCC 12.2.0 的容器中使用真实 PostgreSQL 补验通过。

在仓库根目录使用 **PowerShell 7** 可重跑容器验收和 Linux race：

```powershell
./scripts/verify-s1.ps1
```

脚本每次生成独立项目名和随机密码，数据库使用 tmpfs 且不暴露主机端口，API 只绑定 `127.0.0.1:18080`。检查迁移、重复管理员初始化拒绝、就绪、匿名 401、管理员登录、权限查询、CSRF 退出、真实数据库 race 和正常关闭。成功或失败后均清理本次临时容器和网络；日志与临时配置写入 Git 忽略的 `.cache/verification/`。

若官方下载在本机需要已配置的系统 HTTP 代理，可以使用：

```powershell
./scripts/verify-s1.ps1 -UseSystemProxy -GoProxy 'https://goproxy.cn,direct'
```

`-UseSystemProxy` 只为镜像构建客户端临时设置进程代理，构建后恢复；本机 HTTP 验收使用 IPv4 直连。默认仍为 Go 官方模块代理；示例模块代理只是本次网络环境选项，保留 `go.sum` 校验。`-HttpPort` 可修改测试端口，`-NoCache` 可在构建缓存异常时仅为本项目重建。`verify` 阶段带 Go、GCC 和测试源码；最终运行阶段只包含程序与根证书。

本轮实际检查和审查结果见 [S1 验收记录](../docs/verification/2026-10-14-s1-identity.md)及[容器与 Linux race 补验](../docs/verification/2026-10-14-s1-supplement.md)。

S2 可用 `./scripts/verify-s2.ps1` 重跑：真实 PostgreSQL、完整 Go race、vet、前端 frozen install、类型检查、构建和隔离 Edge 浏览器操作。测试库 tmpfs 无主机端口；API 只绑定本机 18080，结束后清理。本机未装 Edge 时需先准备 Playwright 配置指定的浏览器。结果见 [S2 验收记录](../docs/verification/2026-10-15-s2-jobs.md)。

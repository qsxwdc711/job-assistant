# S1 容器与 Linux race 补验

日期：2026-10-14。分支 `codex/s1-identity`。补齐原 [S1 验收记录](2026-10-14-s1-identity.md)中的完整 Docker 构建和 race 两项环境缺口，不扩展业务范围。

## 结论

最终完整验证脚本退出 **0**。官方 Go 构建镜像下载与校验、完整程序镜像构建、非 root API/CLI 运行、真实 PostgreSQL 身份流程和 Linux race 均通过。API 收到停止信号后正常退出，临时容器与网络已删除。

没有调用模型、上传简历、发送飞书消息或发布岗位。生产来源仍为 0/20，正式岗位为 0，下一阶段是 S2。

## 实际环境与变更

| 项目 | 实测值 |
|---|---|
| Docker Desktop | 4.36.0.175267，WSL 2 后端 |
| Engine / Compose / Buildx | 27.3.1 / 2.30.3 / 0.18.0 |
| 构建及测试 Go | go1.26.8 linux/amd64 |
| race C 编译器 | GCC 12.2.0，CGO_ENABLED=1 |
| 数据库 | PostgreSQL 18.6 (Debian 18.6-1.pgdg13+2) |
| 测试数据库 | jobassistant_test，tmpfs，无主机端口 |
| 最终验收项目 | job-assistant-verify-65e5809a |
| API 主机端口 | 127.0.0.1:18080 |
| 最终运行镜像用户 | 65532:65532，linux/amd64 |

Dockerfile 从官方 `registry-1.docker.io/library/golang:1.26.8-bookworm` 读取并固定摘要：

```text
sha256:dc9ad6c05acc7a88e5b71bde60a5fe3bd4b9f0db209011711b464107438a8107
```

该摘要通过官方 Registry 的 manifest 内容与 Docker-Content-Digest 核对。保留镜像和模块完整性校验，没有跳过校验来处理原下载故障。模块默认使用 Go 官方代理；本机此次使用 `https://goproxy.cn,direct` 并保留 `go.sum`。

新增独立 `verify` 构建阶段和临时 Compose 文件，测试镜像含 GCC、Go 与真实测试源码，最终 scratch 镜像只含 API、admin 和根证书。验证脚本使用随机项目名、随机密码、独立临时数据库，失败时保存 API 日志，退出时清理本次资源并恢复进程代理。

最终本机镜像标识（包含构建证明的 OCI index，未推送）：

```text
api:   sha256:6ec72aabf40fbe8daec1bdecb38d41b0278b316613133b480b50ee7fb06d7bac
tests: sha256:ff9150b2a4fe447d042fc176af151ca50a3b2896a10f89f9fd667a61e3183a37
```

## 实际检查

最终执行：

```powershell
./scripts/verify-s1.ps1 -GoProxy 'https://goproxy.cn,direct' -UseSystemProxy
```

| 检查 | 实际结果 |
|---|---|
| Compose 配置解析与完整镜像构建 | 通过；曾单独使用 `-NoCache` 完成冷构建以修复异常缓存 |
| `admin migrate` | 成功 |
| `admin bootstrap` | 首次成功；重复执行退出 1 且报告管理员已存在 |
| 数据库与 API 就绪 | 通过，返回 `status=ready` |
| 未登录读取身份 | 401 |
| 管理员登录、当前权限、账号列表 | 有效角色 admin，临时库只含一个管理员 |
| 携带 CSRF 的退出 | 204；随后读取身份返回 401 |
| `go test -race -v ./... -count=1 -timeout=180s` | 全部通过，真实集成测试 2.537s，无 race 报告 |
| SIGTERM 与关闭 | API 容器退出 0，记录 `API stopped cleanly` |
| 临时容器、网络 | 已删除；再查询标签无残留 |
| PowerShell 脚本语法与 `git diff --check` | 通过 |
| 故障路径代理恢复 | 注入 Docker 与清理均不可用的异常，HTTP_PROXY、HTTPS_PROXY、NO_PROXY 恢复到原值，含原先不存在的变量 |

race 实际运行了配置测试、12 个权限场景和以下 9 组真实数据库测试，未跳过：

1. TestIdentityMigrationAndBootstrapRace
2. TestIdentityCurrentRoleExpiryAndDisabledSessions
3. TestIdentitySessionOwnershipAndExpiry
4. TestIdentityLastAdministratorAndStaleActor
5. TestIdentityConcurrentLastAdministrator
6. TestIdentityAuditFailureRollsBack
7. TestIdentityUsernameAndCredentialFailures
8. TestIdentityHTTPFlow
9. TestIdentityHTTPInputAndSecureCookie

可公开的实测输出摘录见 [Linux race 日志](evidence/2026-10-14-s1-linux-race.txt)。完整 build、migration、bootstrap、smoke、race、api、result 和 cleanup 日志留在本机 `.cache/verification/job-assistant-verify-65e5809a/`；临时配置和随机密码不纳入 Git。

关闭日志实际时间：

```text
2026-10-14T14:56:58.038888771Z API started
2026-10-14T14:57:19.641304748Z API stopped cleanly
```

## 原故障处理与限制

- 原第三方镜像站的 size validation failed：改用官方完整仓库地址及已核验摘要。构建客户端需要本机系统代理时仅临时使用，构建完成后恢复。
- C 盘空间不足后 Docker/WSL 无响应：经用户明确授权关闭 WSL，完整备份和校验后迁移至 D 盘，见 [数据盘迁移记录](../operations/2026-10-14-docker-storage-migration.md)。
- 恢复时残留通信文件：保存只含一个零字节 socket 的临时目录，重建该目录后 Docker 成功启动。
- 原运行镜像缓存异常：提取最终 `/app/admin` 确认为零字节，而构建阶段的 ELF amd64 程序完整且可运行。只对本项目执行 `-NoCache` 重建后，CLI 迁移和初始化成功。未将这一观察扩大为对全部镜像或底层引擎故障原因的证明。
- 本机健康检查受构建代理影响：API 已能响应，但同一验证进程超时；现在在构建后恢复代理，并明确使用 `127.0.0.1` 直连。修复后的完整流程通过。

本轮只改变构建与验证配置和脚本，没有修改 Go 身份业务逻辑。原 Windows C 工具链问题仍存在，Linux race 补验关闭的是该检查的执行缺口。[Go 官方 race detector 文档](https://go.dev/doc/articles/race_detector)说明该检查依赖支持的平台和 C 编译器，且只覆盖实际执行过的路径；通过不代表尚未实现的岗位、简历和后台任务已经检验。

生产 HTTPS、网页端到端、应用数据库备份恢复、云部署和长期运行观察仍按后续阶段验收。

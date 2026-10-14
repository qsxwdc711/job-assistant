# Docker Desktop 数据盘迁移记录

日期：2026-10-14。Docker Desktop 4.36.0.175267，WSL 2 后端，Docker Engine 27.3.1。用户授权迁移至 D 盘，并明确允许关闭包括 Ubuntu-24.04 在内的整个 WSL 环境。

## 结果与位置

| 项目 | 迁移后的实际值 |
|---|---|
| Docker WSL 数据根目录 | `D:\DockerData\wsl` |
| 正在使用的数据盘 | `D:\DockerData\wsl\disk\docker_data.vhdx` |
| 保留的离线备份 | `D:\DockerData\migration-backup\docker_data.before.vhdx` |
| 迁移前配置备份 | `D:\DockerData\migration-backup\settings-store.at-migration.json` |
| Docker 设置文件 | `C:\Users\zcn20\AppData\Roaming\Docker\settings-store.json` |
| 已修改的设置 | `CustomWslDistroDir = D:\DockerData\wsl` |
| 刚迁移后 C 盘可用空间 | 20.13 GiB，迁移前约 0.08–0.30 GiB |
| 保留两份数据盘后 D 盘可用空间 | 52.15 GiB，启动后会随实际使用变化 |

原 C 盘文件为 `C:\Users\zcn20\AppData\Local\Docker\wsl\disk\docker_data.vhdx`。只有在备份和工作副本均完整校验后才删除该文件。Docker 恢复后核对 C 盘未重新生成该数据盘，D 盘工作副本的修改时间随启动更新。随后完成项目构建与 Linux race 补验时，再次检查 C 盘可用 19.02 GiB、D 盘可用 51.45 GiB；空余空间是当时快照，并非固定预留值。

迁移盘大小为 **22,676,504,576 字节（21.12 GiB）**。离线时原文件、备份、工作副本的长度和 SHA-256 全部一致：

```text
0922a94cf0ce23c5942c35e23529d30ad53cfcb0667e21753fa5a3020d6a1dc5
```

此摘要对应迁移前的静止数据。恢复使用后，工作副本会发生正常写入，不能再要求其摘要等于旧备份。备份保持原状，约占 D 盘 21 GB，本轮保留。

## 关闭、迁移与恢复

Docker 和 WSL 的正常退出均超时。经用户授权后，在 Windows 管理员权限下先尝试停止 WSL 服务；超时后核对专用 `wslservice.exe` 的安装路径并停止该服务进程。随后以独占读方式打开数据盘，确认已没有写入者，再复制和校验。没有重置 Docker、注销发行版或删除用户数据卷。

复制期间保留原盘独占句柄，分别验证两份副本。D 盘目标目录沿用原 Docker 数据目录的访问规则；保存原配置后，仅修改 WSL 数据位置，再释放并移除已核验的 C 盘原文件。

WSL 服务已恢复，Docker Desktop 已重新启动。第一次启动报错指向此前异常退出残留的零字节 `userAnalyticsOtlpHttp.sock`。关闭 Docker 进程并核对临时目录内只有此文件后，将 `C:\Users\zcn20\AppData\Local\Docker\run` 保留为 `run.before-migration-restart`，创建新的空 `run` 目录。第二次启动成功。

恢复后的引擎核对结果：原有 **15 个容器、25 个镜像、27 个数据卷**仍可见，包含本项目迁移前已构建的两份镜像；此前使用的 MySQL 容器已自动恢复运行。未打开或查询其他项目数据库。WSL 关闭前的临时 Linux 进程不会自动恢复，此记录不宣称恢复了这些进程。

私有的完整容器清单、迁移日志和配置备份留在本机，未纳入 Git。

## 回退依据

若以后需要回退，应先完全关闭 Docker 和 WSL，确认工作数据盘不再被占用，并为当前 D 盘数据另做备份。迁移时保留的 `docker_data.before.vhdx` 是 **2026-10-14 迁移前快照**，不能覆盖后来写入的数据而不核对差异。

回到旧位置时，需先保证 C 盘有足够空间，将选定的完整数据盘复制到旧 `wsl\disk\docker_data.vhdx`，核对长度和摘要，再恢复对应的原设置。数据必须先到位，随后才能启动 Docker；不能只改路径启动一个空数据盘。备份与配置文件都应在停止状态下操作。

本次依据 Docker 官方的[WSL 数据位置说明](https://docs.docker.com/desktop/features/wsl/)、[完全停止后备份数据盘的说明](https://docs.docker.com/desktop/settings-and-maintenance/backup-and-restore/)，并结合[维护者对 `customWslDistroDir` 与先复制数据盘的解释](https://github.com/docker/for-win/issues/14163)。实际设置键和目录结构通过本机 4.36.0 配置及启动后的状态再次确认。

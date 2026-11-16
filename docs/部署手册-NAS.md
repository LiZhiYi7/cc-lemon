# 柠檬账 · NAS 部署手册

> **读者**：客户方负责服务器的技术人员（NAS 管理员、IT）
> **目标**：把柠檬账部署到 NAS，成为局域网内多人使用的财务系统，并具备每日备份、可升级、可恢复的能力
> **前置要求**：会用 SSH 或 NAS 的 Docker 管理界面，看得懂 `docker compose` 命令

部署完成后，员工怎么用见同目录 **《使用手册》**。

---

## 目录

1. [部署形态](#1-部署形态)
2. [部署前确认](#2-部署前确认)
3. [选择部署方式](#3-选择部署方式)
4. [方式 A：离线镜像包（推荐）](#4-方式-a离线镜像包推荐)
5. [方式 B / C：自行构建镜像](#5-方式-b--c自行构建镜像)
6. [网络与访问](#6-网络与访问)
7. [备份与恢复](#7-备份与恢复)
8. [升级](#8-升级)
9. [日常运维命令](#9-日常运维命令)
10. [故障排查](#10-故障排查)
11. [交付验收清单](#11-交付验收清单)
12. [附录：端口、卷与环境变量](#12-附录端口卷与环境变量)

---

## 1. 部署形态

整套系统由三个容器组成，用一份 `docker-compose` 文件编排：

```
        局域网内的电脑 / 手机浏览器
                    │  HTTP :8080
                    ▼
        ┌───────────────────────┐
        │  app   柠檬账服务      │   Go 二进制 + 内嵌前端
        │  端口 8080             │   数据库迁移在启动时自动执行
        └───────────┬───────────┘
                    │  容器内部网络 5432
                    ▼
        ┌───────────────────────┐
        │  db    PostgreSQL 16   │   数据落在 ${DATA_DIR}/postgres
        └───────────┬───────────┘
                    │
        ┌───────────▼───────────┐
        │  backup  每日备份      │   每天 pg_dump → ${BACKUP_DIR}
        └───────────────────────┘
```

要点：

- **数据库端口不对局域网开放**，只有 `app` 能访问，这是刻意的。
- 只有 `app` 暴露端口（默认 8080）。
- `backup` 不参与业务，只负责每天导出一份数据库并清理过期备份。
- 首次启动会自动建表，不需要手工执行 SQL。

---

## 2. 部署前确认

逐项确认，任何一项不满足都会在部署中卡住。

| 检查项 | 要求 | 怎么确认 |
| --- | --- | --- |
| Docker | 支持 Compose（Container Manager / Container Station / Docker + compose 插件均可） | `docker --version`；`docker compose version`（老系统是 `docker-compose --version`） |
| CPU 架构 | x86_64（amd64）或 arm64 | `uname -m`：`x86_64` → amd64，`aarch64` → arm64 |
| 可用内存 | ≥ 2 GB | 群晖「资源监控」，或 `free -m` |
| 可用磁盘 | ≥ 10 GB（数据 + 30 天备份） | `df -h` |
| 网络 | NAS 有固定 IP（或路由器上做了 DHCP 保留） | 路由器后台 |
| 端口 | TCP 8080 未被占用（可改） | `netstat -tlnp \| grep 8080` |
| 时间 | 已开启 NTP 自动对时 | 群晖：控制面板 → 区域选项 → NTP 服务 |

### 架构最容易装错

镜像分 amd64 和 arm64 两套，**装错的表现是容器起来就退出，日志里写 `exec format error`**。

常见的 arm64 机型（举例，最终以 `uname -m` 为准）：群晖 DS223 / DS124、威联通 TS-233、部分极空间与绿联型号。
常见的 x86 机型：群晖 DS923+ / DS1522+、威联通 TS-464、绝大多数自建 NAS。

不确定就在 NAS 上执行 `uname -m`，不要凭型号猜。

---

## 3. 选择部署方式

| 方式 | 适用场景 | 需要 NAS 联网 | 大致耗时 | 建议 |
| --- | --- | --- | --- | --- |
| **A 离线镜像包** | 客户内网、NAS 不能访问互联网 | 否 | 10 分钟 | **推荐** |
| B 在 NAS 上构建 | NAS 能联网，且 CPU 较强 | 是 | 20–60 分钟 | 低配 NAS 不建议 |
| C 电脑构建后导入 | 有另一台能装 Docker 的电脑 | 构建机需要 | 20 分钟 | 备选 |

下面主要写方式 A；B、C 在 §5 概述。

---

## 4. 方式 A：离线镜像包（推荐）

离线包里已经包含 `app` 与 `postgres` 两个镜像的 tar 文件，NAS 全程不需要联网。

### 4.1 取得离线包

离线包由交付方提供，文件名形如：

```
lemon-nas-amd64-1.0.0.tar.gz      # x86 NAS
lemon-nas-arm64-1.0.0.tar.gz      # ARM NAS
```

如需自行生成：在源码仓库的 **Actions → Release → Run workflow**，填入版本号运行，结束后在该次运行的 **Artifacts** 里下载对应架构的压缩包（amd64、arm64 各一个）。

### 4.2 上传并解压

建议放在 Docker 专用目录下，例如群晖的 `/volume1/docker/lemon`。

```sh
mkdir -p /volume1/docker/lemon
cd /volume1/docker/lemon
tar -xzf lemon-nas-amd64-1.0.0.tar.gz
cd lemon-nas-amd64-1.0.0
ls
```

解压后应有：

```
docker-compose.offline.yml     # 编排文件（不构建、不联网）
backup.sh                      # 备份脚本，被 backup 容器挂载执行
.env.example                   # 配置文件模板
nas/load-images.sh             # 导入镜像的脚本
images/app-1.0.0-amd64.tar
images/postgres-16-alpine-amd64.tar
VERSION.txt                    # 版本与构建时间，报问题时提供此文件内容
```

> 用群晖「File Station」上传也可以，但**不要用 Windows 记事本保存 `.sh` 文件**（会变成 CRLF 换行导致脚本无法执行）。要改配置只改 `.env`。

### 4.3 导入镜像

```sh
sh nas/load-images.sh
```

脚本会自动判断当前架构，只导入匹配的 tar，跳过另一个架构的。成功时输出类似：

```
当前架构：x86_64（需要 amd64 镜像）
导入：images/app-1.0.0-amd64.tar
Loaded image: cc-lemon:1.0.0-amd64
导入：images/postgres-16-alpine-amd64.tar
Loaded image: postgres:16-alpine
已导入 2 个镜像
```

检查：

```sh
docker images | grep -E 'cc-lemon|postgres'
```

应当看到 `cc-lemon:1.0.0-amd64` 与 `postgres:16-alpine`。**记住 `cc-lemon` 的完整 tag**，下一步要填进配置。

脚本在导入后会核对一次镜像架构：如果这个包不是给当前机器用的，会直接报
`镜像 cc-lemon:1.0.0-arm64 的架构是 [arm64]，当前机器需要 [amd64]` 并中止。
看到这句话就是拿错包了，换另一个架构的包重新执行即可——不要继续往下走，否则容器起来就退出。

### 4.4 写配置文件

```sh
cp .env.example .env
vi .env          # 或用 NAS 的文本编辑器打开
```

至少要改两项：

| 变量 | 填什么 | 注意 |
| --- | --- | --- |
| `DB_PASSWORD` | 数据库密码，**16 位以上** | **只能用字母和数字**，见下方说明 |
| `APP_IMAGE` | 上一步导入的 tag，如 `cc-lemon:1.0.0-amd64` | 写错会报「镜像不存在」 |

其余可保持默认：

| 变量 | 默认 | 说明 |
| --- | --- | --- |
| `HTTP_PORT` | `8080` | 对外端口，冲突时改这里 |
| `TZ` | `Asia/Shanghai` | 时区，影响日志与备份文件名 |
| `DATA_DIR` | `./data` | 数据库文件的存放位置 |
| `BACKUP_DIR` | `./backups` | 备份文件存放位置 |
| `BACKUP_KEEP_DAYS` | `30` | 备份保留天数 |
| `TOKEN_TTL` | `12h` | 登录有效期，超过需重新登录 |

> **关于密码字符**：`DB_PASSWORD` 会被拼进 `postgres://用户:密码@db:5432/lemon` 这样的连接串。如果密码里含 `@ : / ? # &` 等字符，连接串会被解析错，表现为应用一直连不上数据库。所以这里限制为**字母 + 数字**，例如 `Lemon2026NasDb7x4Kq`。

> **首次启动后不要再改 `DB_PASSWORD`**。数据库是用这个密码初始化的，改了配置却保留旧数据卷，新的密码对不上，应用会一直认证失败。确需更换，见 §10 最后一节。

### 4.5 启动

```sh
docker compose -f docker-compose.offline.yml up -d
```

老版本 Docker 用 `docker-compose -f docker-compose.offline.yml up -d`（下同）。

首次启动过程：`db` 初始化 → 健康检查通过 → `app` 启动并自动建表 → `backup` 启动。约需 30–60 秒。

### 4.6 验证

```sh
docker compose -f docker-compose.offline.yml ps
```

三个服务都应是 `running`，`db` 与 `app` 显示 `healthy`。

```sh
curl http://127.0.0.1:8080/healthz
```

返回 `ok` 即服务正常（返回 503 表示服务起来了但数据库连不上，看 `docker compose logs app`）。NAS 上没有 `curl` 时，用 `wget -qO- http://127.0.0.1:8080/healthz`。

浏览器打开：

```
http://<NAS 的 IP>:8080
```

### 4.7 创建系统管理员

首次打开会提示「首次使用，请创建系统管理员」：

1. 用户名：建议 `admin` 或单位统一命名
2. 姓名：真实姓名，会显示在凭证的「制单人」上
3. 密码：至少 6 位，**建议 12 位以上**
4. 点击「创建管理员并登录」

这个账号是**系统管理员**：能看到所有账套、能管理所有用户。请把密码单独保管，日常记账不要用它。

接下来该做什么：把《使用手册》发给会计，让他们建账套；或者由管理员自己按《使用手册》第三部分先建好账套、分配成员。

---

## 5. 方式 B / C：自行构建镜像

> 只有拿不到离线包时才需要看这一节。

### 方式 B：在 NAS 上直接构建

```sh
cd /volume1/docker
git clone <源码仓库地址> cc-lemon
cd cc-lemon/deploy
cp .env.example .env
vi .env                      # 改 DB_PASSWORD
docker compose up -d --build
```

注意：

- 构建会执行 `npm ci`（下载前端依赖）和 Go 编译，**必须能访问外网**。
- 低配 NAS（2 GB 内存、ARM 四核）容易在 `npm ci` 阶段被 OOM kill，表现是构建中途退出。建议改用方式 A 或 C。
- 构建时间可能 20–60 分钟，期间 NAS 会很卡，建议在夜间进行。

### 方式 C：在电脑上构建后导入

在有 Docker 的电脑上，仓库根目录：

```sh
# 注意 --platform：在 Apple Silicon / ARM 电脑上构建 x86 NAS 用的镜像必须指定
docker build --platform linux/amd64 -f deploy/Dockerfile -t cc-lemon:1.0.0-amd64 .
docker save cc-lemon:1.0.0-amd64 -o app.tar

docker pull --platform linux/amd64 postgres:16-alpine
docker save postgres:16-alpine -o postgres.tar
```

把 `app.tar`、`postgres.tar`，以及仓库里的 `deploy/docker-compose.offline.yml`、`deploy/backup.sh`、`deploy/nas/.env.example` 一起拷到 NAS，然后按 §4.3 起执行。

---

## 6. 网络与访问

### 6.1 固定 IP

给员工一个不会变的地址，前提是 NAS 的 IP 固定：

- 路由器里做 DHCP 保留（推荐，改网络时不易冲突）
- 或在 NAS 上设静态 IP

### 6.2 改端口

`8080` 被占用（例如已被别的服务、或 NAS 自带的 Web Station 占用）时：

```sh
vi .env        # HTTP_PORT=8090
docker compose -f docker-compose.offline.yml up -d
```

### 6.3 防火墙

只放行局域网，不要把端口暴露到公网。

- **群晖**：控制面板 → 安全性 → 防火墙 → 新增规则：允许，端口 TCP 8080，来源限定 `192.168.0.0/16`
- **威联通**：安全中心 → 防火墙 → 新增规则
- **飞牛 / Unraid**：系统自带防火墙或路由器层控制

### 6.4 告诉员工访问地址

统一发给员工：

```
http://192.168.1.100:8080
```

（换成实际 IP。）介意记 IP 的，可以在内网 DNS 或路由器里加一条记录，例如 `http://lemon.lan:8080`。

### 6.5 公网访问（谨慎）

系统**默认是 HTTP 明文**，设计前提是「可信局域网」。如果确有外网访问需求：

- 必须加反向代理并启用 HTTPS，不要把 8080 直接映射到公网
- 建议只允许通过 VPN 接入后再访问
- 开启失败登录锁定等额外防护（由反向代理或网关负责）

群晖反向代理：控制面板 → 登录门户 → 高级 → 反向代理 → 新增：

| 项 | 值 |
| --- | --- |
| 来源协议 / 端口 | HTTPS / 443（或自定义域名） |
| 目的地主机名 | `127.0.0.1` |
| 目的地端口 | `8080` |

Nginx 参考配置：

```nginx
server {
    listen 443 ssl;
    server_name lemon.example.com;

    ssl_certificate     /etc/nginx/cert/lemon.pem;
    ssl_certificate_key /etc/nginx/cert/lemon.key;

    client_max_body_size 50m;          # Excel 导入建议放宽

    location / {
        proxy_pass http://127.0.0.1:8080;
        proxy_set_header Host              $host;
        proxy_set_header X-Real-IP         $remote_addr;
        proxy_set_header X-Forwarded-For   $proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto $scheme;
        proxy_read_timeout 120s;
    }
}
```

---

## 7. 备份与恢复

### 7.1 自动备份在做什么

`backup` 容器启动后立即执行一次，此后每 24 小时执行一次：

```sh
pg_dump -h db -U lemon -d lemon -Fc -f /backups/lemon-YYYYmmdd-HHMMSS.dump
```

- 先写 `.partial` 临时文件，成功后再改名，避免留下半截备份
- 自动删除超过 `BACKUP_KEEP_DAYS`（默认 30）天的备份
- 文件位置：NAS 上 `${BACKUP_DIR}` 目录（默认安装目录下的 `backups/`）

检查：

```sh
ls -lh backups | tail -5
docker compose -f docker-compose.offline.yml logs --tail=20 backup
```

### 7.2 只靠 NAS 本地备份是不够的

备份和数据库在同一台机器上，遇到磁盘损坏、勒索软件、误删目录会一起没。**至少要做到其中一条**：

- 备份目录放在支持快照的存储卷，并开启定时快照
- 每周把 `backups/` 同步一份到另一台机器、移动硬盘或异地（Hyper Backup、rsync、云盘均可）

### 7.3 手动立即备份

升级前、批量导入前、年底结账前，建议手动备一次：

```sh
mkdir -p /volume1/backup
docker compose -f docker-compose.offline.yml exec -T db \
  pg_dump -U lemon -d lemon -Fc > /volume1/backup/lemon-manual-$(date +%Y%m%d-%H%M).dump
```

### 7.4 恢复

**恢复会覆盖当前全部数据。先按 §7.3 备份现状，再执行恢复。**

```sh
# 1) 停应用，避免恢复过程中有人写入
docker compose -f docker-compose.offline.yml stop app

# 2) 导入备份（--clean 会先删除库内已有对象）
cat backups/lemon-20260101-020000.dump | \
  docker compose -f docker-compose.offline.yml exec -T db \
  pg_restore -U lemon -d lemon --clean --if-exists --no-owner

# 3) 启动应用
docker compose -f docker-compose.offline.yml start app

# 4) 验证
curl http://127.0.0.1:8080/healthz
```

说明：

- 恢复后**所有用户需要重新登录**（登录凭据存在数据库里，已被覆盖）。
- 恢复完成后核对：账套列表、最近一个月的凭证数量、科目余额表期末数。
- 如果 `pg_restore` 报错，先看具体表名再判断；多数是版本不一致（用旧版本备份恢复到新版本，通常兼容；反向不保证）。

---

## 8. 升级

升级只替换应用镜像，数据结构由程序启动时自动迁移。

```sh
# 1) 先备份（重要）
docker compose -f docker-compose.offline.yml exec -T db \
  pg_dump -U lemon -d lemon -Fc > /volume1/backup/lemon-before-upgrade-$(date +%Y%m%d).dump

# 2) 导入新镜像
docker load -i images/app-1.1.0-amd64.tar

# 3) 改 .env 里的镜像 tag
vi .env      # APP_IMAGE=cc-lemon:1.1.0-amd64

# 4) 只重建 app，数据库与备份容器不动
docker compose -f docker-compose.offline.yml up -d app

# 5) 看启动日志，确认迁移完成
docker compose -f docker-compose.offline.yml logs -f app
```

日志里出现 `服务已启动` 且 `curl /healthz` 返回 `ok` 即升级完成。浏览器按一次 `Ctrl+F5` 刷新。

**不能降级。** 数据库迁移是单向的，把 `APP_IMAGE` 改回旧 tag 很可能启动失败。要回退必须同时恢复升级前的备份（§7.4），两者版本要对应。

---

## 9. 日常运维命令

在安装目录（含 `docker-compose.offline.yml` 的目录）下执行：

| 目的 | 命令 |
| --- | --- |
| 看容器状态 | `docker compose -f docker-compose.offline.yml ps` |
| 看应用日志（实时） | `docker compose -f docker-compose.offline.yml logs -f --tail=100 app` |
| 看数据库日志 | `docker compose -f docker-compose.offline.yml logs --tail=100 db` |
| 看备份日志 | `docker compose -f docker-compose.offline.yml logs --tail=50 backup` |
| 重启应用（改配置后） | `docker compose -f docker-compose.offline.yml up -d app` |
| 停止全部 | `docker compose -f docker-compose.offline.yml stop` |
| 启动全部 | `docker compose -f docker-compose.offline.yml start` |
| 进数据库命令行 | `docker compose -f docker-compose.offline.yml exec db psql -U lemon -d lemon` |
| 看各容器资源占用 | `docker stats --no-stream` |
| 看磁盘占用 | `du -sh data backups` |
| 查应用版本 | `cat VERSION.txt`，或启动日志首行的 `version=` |

---

## 10. 故障排查

| 现象 | 可能原因 | 怎么查 / 怎么处理 |
| --- | --- | --- |
| 容器起来就退出，日志 `exec format error` | 镜像架构不对（x86 用了 arm64 包，反之） | `uname -m` 确认架构，换对应架构的离线包重新导入 |
| 报 `pull access denied` / 一直尝试联网拉取 | 用了 `docker-compose.yml` 而不是 `docker-compose.offline.yml` | 离线环境必须用 `.offline.yml`，它带 `pull_policy: never` |
| `app` 反复重启，日志 `connection refused` / `password authentication failed` | `DB_PASSWORD` 与数据卷里的密码不一致（改过配置），或密码含特殊字符 | 若从未成功启动：删掉 `data/postgres` 重新 `up -d`（空库无所谓）。若已有数据：把密码改回原值 |
| 页面能打开但**一片空白** | 前端静态资源缺失（镜像包损坏） | 按 `F12` 看控制台，若报 `MIME type of "text/html"` 即资源缺失；换一个完整的离线包重新导入 |
| 打开提示「无法访问此网站」 | 端口未放行 / 服务未起 / IP 不对 | `curl http://127.0.0.1:8080/healthz`；查防火墙；确认 IP |
| `curl /healthz` 返回 503 | 应用活着但连不上数据库 | `docker compose logs db` 看数据库是否正常；确认 `db` 是 `healthy` |
| 同事打不开、本机能打开 | NAS 防火墙或网络被识别为「公用」 | 放行 8080；确认访问的是 NAS 的局域网 IP |
| 备份目录一直是空的 | `backup` 容器没起来；`BACKUP_DIR` 路径无权限 | `docker compose logs backup`；检查目录权限 |
| 备份失败提示 `No space left on device` | 磁盘满 | `df -h`；清理旧备份或扩容，调小 `BACKUP_KEEP_DAYS` |
| 凭证日期、日志时间不对 | 容器时区不对 | `.env` 里 `TZ=Asia/Shanghai`，改完 `up -d` |
| 上传 Excel 报 413 | 反向代理限制了请求体大小 | Nginx 加 `client_max_body_size 50m;`，群晖反代同理 |
| 升级后打不开，日志报迁移错误 | 新版本迁移脚本与旧数据不兼容 | 恢复升级前的备份，联系交付方 |

### 确实要更换数据库密码时

已有数据的情况下不能只改 `.env`：

```sh
# 1) 用旧密码进数据库
docker compose -f docker-compose.offline.yml exec db psql -U lemon -d lemon

# 2) 在 psql 里执行（替换为新密码）
ALTER USER lemon WITH PASSWORD '新密码';

# 3) 退出 psql，再改 .env 里的 DB_PASSWORD 为新密码
docker compose -f docker-compose.offline.yml up -d
```

---

## 11. 交付验收清单

部署完成后逐项确认，全部通过再交给使用者：

- [ ] `docker compose ps` 三个容器均为 `running`，`db` 与 `app` 为 `healthy`
- [ ] `curl http://127.0.0.1:8080/healthz` 返回 `ok`
- [ ] 用**另一台电脑**（不是 NAS 自己）能打开 `http://<NAS-IP>:8080`
- [ ] 已创建系统管理员，并记录账号（密码单独保管）
- [ ] 建了一个测试账套，能正常创建、能进入
- [ ] `backups/` 下已产生至少一个 `.dump` 文件
- [ ] 备份目录已纳入快照或异地同步
- [ ] `VERSION.txt` 已留存（后续报问题时提供）
- [ ] 《使用手册》已发给会计与管理员
- [ ] 已告知员工访问地址与「用 Chrome / Edge / Firefox，不要用 IE」

---

## 12. 附录：端口、卷与环境变量

### 端口

| 端口 | 谁在用 | 是否对外开放 |
| --- | --- | --- |
| 8080（可改） | app，HTTP 服务 | 是，仅局域网 |
| 5432 | db，PostgreSQL | 否，只在容器网络内 |

### 卷映射

| 容器内路径 | 宿主机路径 | 内容 |
| --- | --- | --- |
| `/var/lib/postgresql/data` | `${DATA_DIR}/postgres` | 数据库文件，**不要手工改动** |
| `/backups` | `${BACKUP_DIR}` | 每日备份文件 |
| `/backup.sh` | `./backup.sh` | 备份脚本（只读挂载） |

### 环境变量

| 变量 | 默认 | 必填 | 说明 |
| --- | --- | --- | --- |
| `DB_PASSWORD` | 无 | **是** | 数据库密码，仅字母数字，16 位以上 |
| `APP_IMAGE` | 无 | **是** | 应用镜像 tag，须与导入的一致 |
| `HTTP_PORT` | 8080 | 否 | 对外端口 |
| `TZ` | Asia/Shanghai | 否 | 时区 |
| `DATA_DIR` | ./data | 否 | 数据库文件位置 |
| `BACKUP_DIR` | ./backups | 否 | 备份位置 |
| `BACKUP_KEEP_DAYS` | 30 | 否 | 备份保留天数 |
| `TOKEN_TTL` | 12h | 否 | 登录有效期 |

### 可选：开放更多会计准则

交付版本默认只开放《政府会计制度（行政事业单位）》，新建账套时看不到其他准则。需要开放时，在 `docker-compose.offline.yml` 的 `app.environment` 下增加一行，然后 `up -d app`：

```yaml
      LEMON_STANDARDS_ALLOW: gov_accounting,small_enterprise
```

留空或删除该行表示开放全部内置准则。

### 数据与导出

- 账套数据的权威副本只有一处：`${DATA_DIR}/postgres`。不要用文件复制的方式「备份数据库目录」——数据库运行中复制可能得到损坏的文件，必须用 `pg_dump`。
- 客户需要把账套数据交给审计或上级单位时，用系统内的「导出 Excel」，不要拷数据库文件。

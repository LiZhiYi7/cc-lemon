# 柠檬账 · NAS 部署手册

适用于群晖（Container Manager）、威联通（Container Station）、飞牛、Unraid、TrueNAS 等支持 Docker Compose 的设备。

**全程离线**：不需要 NAS 能上外网，镜像以 tar 文件形式随交付包提供。

---

## 一、先确认架构

在 NAS 的 SSH 终端里执行：

```bash
uname -m
```

| 输出 | 架构 | 用哪个包 |
| --- | --- | --- |
| `x86_64` | amd64 | `lemon-nas-amd64-<版本>.tar.gz` |
| `aarch64` / `arm64` | arm64 | `lemon-nas-arm64-<版本>.tar.gz` |

装错架构会报 `exec format error`。群晖 DS220+、DS923+ 等常见型号是 x86_64；部分入门型号（如 DS223）是 arm64。

---

## 二、部署（四步）

### 1. 上传并解压

把对应架构的包上传到 NAS 的共享目录（例如 `/volume1/docker/lemon`），解压：

```bash
cd /volume1/docker/lemon
tar -xzf lemon-nas-amd64-1.0.0.tar.gz
cd lemon
```

解压后目录结构：

```
lemon/
├─ docker-compose.offline.yml
├─ .env.example
├─ images/
│  ├─ app-1.0.0.tar        # 应用镜像
│  └─ postgres-16-alpine.tar
└─ nas/
   └─ load-images.sh
```

### 2. 导入镜像

```bash
sh nas/load-images.sh
```

脚本会依次 `docker load` 两个 tar，并校验架构是否与 `uname -m` 一致，最后打印已导入的镜像。

### 3. 配置

```bash
cp .env.example .env
vi .env
```

必填项：

| 变量 | 说明 |
| --- | --- |
| `DB_PASSWORD` | 数据库密码，请改成强密码，**只用字母和数字**（会拼进连接串） |
| `APP_IMAGE` | 应用镜像名，与导入的一致，如 `cc-lemon:1.0.0-amd64` |
| `HTTP_PORT` | 对外端口，默认 8080 |
| `DATA_DIR` | 数据库存放目录，建议放在有快照的存储卷 |
| `BACKUP_DIR` | 备份输出目录 |
| `BACKUP_KEEP_DAYS` | 备份保留天数，默认 30 |

### 4. 启动

```bash
docker compose -f docker-compose.offline.yml up -d
docker compose -f docker-compose.offline.yml ps
```

验证：

```bash
curl -s http://127.0.0.1:8080/healthz
# 期望输出：ok
```

浏览器打开 `http://<NAS 的 IP>:8080`，首次访问会引导创建系统管理员。

---

## 三、日常运维

### 升级

```bash
# 1. 导入新版本镜像
tar -xzf lemon-nas-amd64-1.0.1.tar.gz
sh nas/load-images.sh

# 2. 改 .env 里的 APP_IMAGE 为新版本
vi .env

# 3. 重建应用容器（数据库卷不动，表结构自动迁移）
docker compose -f docker-compose.offline.yml up -d
```

> 数据库结构的迁移在应用启动时自动执行；升级前建议先做一次备份。

### 备份

`backup` 容器每天执行一次 `pg_dump`，文件在 `BACKUP_DIR`，保留 `BACKUP_KEEP_DAYS` 天。

手动备份：

```bash
docker compose -f docker-compose.offline.yml exec -T db \
  pg_dump -U lemon -d lemon -Fc > backups/manual-$(date +%Y%m%d).dump
```

### 恢复

```bash
docker compose -f docker-compose.offline.yml stop app
docker compose -f docker-compose.offline.yml exec -T db \
  pg_restore -U lemon -d lemon --clean --if-exists < backups/lemon-20260101-020000.dump
docker compose -f docker-compose.offline.yml start app
```

### 查看日志

```bash
docker compose -f docker-compose.offline.yml logs -f app
docker compose -f docker-compose.offline.yml logs -f db
```

---

## 四、对外开放与 HTTPS

系统默认走 HTTP，只适合在可信局域网内使用。

**需要通过公网访问时**，请在 NAS 前面加反向代理并启用 HTTPS（群晖自带「反向代理服务器」，或部署 Nginx Proxy Manager），并注意：

- 反代目标填 `http://<NAS 内网 IP>:8080`
- 开启 WebSocket 透传（若后续版本加入实时推送）
- 不要把数据库端口映射到外网
- 建议加访问控制（IP 白名单或 VPN），财务数据不建议直接暴露公网

---

## 五、常见问题

| 现象 | 处理 |
| --- | --- |
| `exec format error` | 镜像架构与 NAS 不符，重新下载对应架构的包 |
| `docker compose up` 尝试去拉镜像 | 使用了错误的 compose 文件，必须用 `docker-compose.offline.yml`；确认 `pull_policy: never` 生效 |
| `no such image` | 镜像没导入成功，重新执行 `sh nas/load-images.sh` |
| `/healthz` 返回 503 | 数据库还没就绪，等 10–20 秒再试；仍失败看 `logs db` |
| 端口被占用 | 改 `.env` 里的 `HTTP_PORT`，重新 `up -d` |
| 忘记管理员密码 | 与 Windows 版一致：用其他管理员重置，或从备份恢复 |

# 柠檬账（cc-lemon）

部署在本地 NAS 上的局域网财务系统。功能与交互以柠檬云财务为基准，并支持**平行记账**（同一账套内财务会计与预算会计并行核算）。

## 已有功能

- **账套**：按会计准则模板建账；内置《小企业会计准则》与《政府会计制度（行政事业单位）》
- **科目与期初**：多级科目、编码方案校验、增加下级时数据自动下沉、期初试算平衡
- **凭证**：录入、修改、审核、整理凭证号；“=” 自动找平、Ctrl+S 保存、连续录入
- **平行记账**：一张凭证分区录入各账簿分录，按规则自动生成预算会计分录，规则可配置
- **账簿**：科目余额表、明细账、总账
- **期末**：按规则生成结转凭证、结账检查、结账与反结账
- **报表**：资产负债表、利润表、收入费用表、预算收入支出表（模板驱动）
- **权限**：用户、账套成员与角色、操作日志

架构、模块边界、扩展方式与迭代路线见 [docs/architecture.md](docs/architecture.md)。

## 部署到 NAS

要求 NAS 支持 Docker Compose（群晖 Container Manager、威联通 Container Station、飞牛、Unraid 等）。

1. 把本仓库放到 NAS 上，进入 `deploy/` 目录
2. 复制环境文件并设置数据库密码

   ```bash
   cp .env.example .env
   ```

3. 构建并启动

   ```bash
   docker compose up -d --build
   ```

4. 局域网内用浏览器访问 `http://<NAS 的 IP>:8080`，首次访问会引导创建系统管理员

数据库文件在 `DATA_DIR`，每日备份在 `BACKUP_DIR`（均在 `.env` 中配置）。升级时拉取新代码后重新执行第 3 步，数据库结构会自动迁移。

恢复备份：

```bash
docker compose exec -T db pg_restore -U lemon -d lemon --clean --if-exists < backups/lemon-<时间>.dump
```

## 本地开发

需要 Go 1.26+、Node 22+、PostgreSQL 16。

```bash
export LEMON_DATABASE_DSN='postgres://lemon:lemon@localhost:5432/lemon?sslmode=disable'
make dev-server
```

```bash
make dev-web
```

前端开发服务器默认把 `/api` 代理到 `http://localhost:8080`，可用 `VITE_API_TARGET` 修改。

测试（端到端测试会清空目标库，请指向专用测试库）：

```bash
LEMON_TEST_DSN='postgres://lemon:lemon@localhost:5432/lemon_test?sslmode=disable' make test
```

## 配置

运行配置见 [config.example.yaml](config.example.yaml)，每一项都可用 `LEMON_` 前缀的环境变量覆盖。

业务配置都是数据而非代码：

| 内容 | 位置 |
| --- | --- |
| 会计准则模板（科目表、报表、结转规则、平行记账规则） | `configs/standards/*.yaml` |
| 角色与权限 | `configs/roles.yaml` |
| 前端菜单 | `web/src/config/menu.ts` |

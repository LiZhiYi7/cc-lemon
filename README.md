# 柠檬账（cc-lemon）

[![CI](https://github.com/LiZhiYi7/cc-lemon/actions/workflows/ci.yml/badge.svg)](https://github.com/LiZhiYi7/cc-lemon/actions/workflows/ci.yml)
![Go](https://img.shields.io/badge/Go-1.26-00ADD8?logo=go&logoColor=white)
![Vue](https://img.shields.io/badge/Vue-3-42B883?logo=vue.js&logoColor=white)
![PostgreSQL](https://img.shields.io/badge/PostgreSQL-16-4169E1?logo=postgresql&logoColor=white)

部署在本地 NAS 上的局域网财务系统。功能与交互以柠檬云财务为基准，并原生支持**平行记账**——同一账套内财务会计与预算会计并行核算，这是《政府会计制度（行政事业单位）》的强制要求。

一个 Go 二进制 + 一个 PostgreSQL 容器即可运行，数据留在自己家里。

## 为什么值得一看

- **平行记账不是补丁，是一等模型**：一个账套、多个核算账簿、一张凭证。分录归属哪个账簿由科目决定，保存时每个账簿内**各自**借贷平衡；录入财务会计分录后按规则一键生成预算会计分录。这套模型同样能承载"多准则并行"（企业账 + 税务账）。
- **余额不落表**：余额 = 期初 + 凭证分录实时汇总，没有"过账"步骤，凭证保存即生效。因此不存在"余额表与凭证不一致"这类财务软件最典型的数据故障。
- **业务配置是数据不是代码**：会计准则模板、报表模板、期末结转规则、平行记账映射、角色权限、前端菜单全部是 YAML / TS 配置。新增一套准则（企业会计准则、民间非营利组织会计制度）只需加一个 YAML 文件，不改代码。
- **金额全程无浮点**：后端 `decimal`，JSON 传输用字符串，前端以"分"为单位做整数运算。

## 已有功能

- **账套**：按会计准则模板建账；内置《小企业会计准则》与《政府会计制度（行政事业单位）》
- **科目与期初**：多级科目、编码方案校验、增加下级时数据自动下沉、期初试算平衡
- **凭证**：录入、修改、审核、整理凭证号；"=" 自动找平、Ctrl+S 保存、连续录入
- **平行记账**：一张凭证分区录入各账簿分录，按规则自动生成预算会计分录，规则可配置
- **辅助核算**：客户、供应商、职员、部门、项目及自定义类别；科目按需启用，凭证与期初按项目记录
- **账簿**：科目余额表、明细账、总账、辅助核算余额表、辅助核算明细账
- **期末**：按规则生成结转凭证（含预算会计年末按资金性质结转）、一键结转、结账检查、结账与反结账
- **首页**：本期概况与快捷入口
- **报表**：资产负债表、利润表、收入费用表、预算收入支出表、净资产变动表、预算结转结余变动表（模板驱动）
- **Excel**：账簿、报表、凭证列表导出；科目、辅助核算项目、期初余额导入
- **权限**：用户、账套成员与角色、操作日志

各功能的操作位置与使用规则见 [docs/features.md](docs/features.md)。

## 技术栈

| 层 | 选型 | 说明 |
| --- | --- | --- |
| 后端 | Go 1.26、`chi`、`pgx` v5 | 手写 SQL，不用 ORM |
| 数据库 | PostgreSQL 16 | 金额 `numeric(18,2)`，迁移脚本内嵌在二进制中，启动自动执行 |
| 金额 | `shopspring/decimal` | 十进制类型，JSON 以字符串传输 |
| 前端 | Vue 3 + TypeScript + Vite + Element Plus + Pinia | Excel 导入导出用 `exceljs` |
| 部署 | Docker Compose | 数据库 + 应用 + 每日备份三个容器，镜像支持 amd64 / arm64 |

## 架构

模块化单体。业务模块只通过对方导出的 Service 交互，不直接读写对方的表；跨模块事务通过 context 传播。

```
cmd/lemon ──► internal/app（装配：创建模块、挂载路由）
               │
               ├── internal/modules/   业务模块
               │     iam        用户、登录
               │     standard   会计准则模板（读取 YAML）
               │     book       账套、成员权限、会计期间、账套级配置
               │     account    会计科目
               │     auxiliary  辅助核算（类别、项目、科目绑定）
               │     voucher    凭证、平行记账规则与分录生成
               │     ledger     余额引擎、期初、科目余额表/明细账/总账（读模型）
               │     closing    期末结转、结账检查、结账 / 反结账
               │     report     报表引擎
               │     dataio     批量导入（科目、辅助核算项目、期初）
               │
               ├── internal/platform/ config / db / httpx / auth / audit
               ├── internal/kernel/   共享的最小领域概念（期间、借贷方向）
               ├── configs/           内嵌默认配置（准则模板、角色）
               └── migrations/        内嵌数据库迁移
```

模块依赖只允许单向：`report → ledger → auxiliary → account → book → standard`，`closing → ledger, voucher`。反向依赖用接口或钩子倒置。

数据模型、关键设计决定、平行记账建模、报表与结转规则语法、扩展点，都在 [docs/architecture.md](docs/architecture.md)。

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

> 系统面向可信局域网，默认 HTTP。如需跨网访问，请在前面加反向代理并启用 HTTPS。

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

## 测试

端到端测试会清空目标库（`drop schema public`），请务必指向专用测试库：

```bash
LEMON_TEST_DSN='postgres://lemon:lemon@localhost:5432/lemon_test?sslmode=disable' make test
```

未设置 `LEMON_TEST_DSN` 时测试会跳过。覆盖六个场景：小企业账务全流程、政府会计平行记账与权限、辅助核算、预算会计年末结转、经营亏损特例、批量导入原子性；断言精确到分，并校验重复结转的幂等性。

CI 在 push 到 `main` 和 PR 时执行 `gofmt` 检查、`go vet`、`go build`、`go test`（起真实 PostgreSQL service）以及前端 `vue-tsc` 类型检查与构建。

## 演示数据

服务启动后执行下面的命令，会创建两套演示账（行政事业单位、小企业）并录入三个月业务，登录账号 `admin` / `lemon-demo`。只用于演示或培训环境。

```bash
make demo-data
```

## 配置

运行配置见 [config.example.yaml](config.example.yaml)，每一项都可用 `LEMON_` 前缀的环境变量覆盖。

业务配置都是数据而非代码：

| 内容 | 位置 |
| --- | --- |
| 会计准则模板（科目表、报表、结转规则、平行记账规则） | `configs/standards/*.yaml` |
| 角色与权限 | `configs/roles.yaml` |
| 前端菜单 | `web/src/config/menu.ts` |

## 状态与路线图

**第一阶段（已完成）总账核心**：账套与准则模板、科目与期初、凭证、平行记账、账簿、期末结转与结账、报表、用户权限、NAS 部署与备份。

**第二阶段（进行中）核算深度**：辅助核算与辅助账已完成；待做数量/外币核算、现金流量表、凭证模板与冲销、报表"上年数"列。

**第三阶段 业务模块**：出纳、固定资产、工资、往来核销、余额快照优化。

完整清单见 [docs/architecture.md](docs/architecture.md) 第 9 节。

## 文档

- [docs/使用手册.md](docs/使用手册.md) —— 给会计、审核与管理员：日常记账、期末结转、账套与权限
- [docs/部署手册-NAS.md](docs/部署手册-NAS.md) —— 给 IT：在 NAS 上用 Docker 部署、备份、升级、排障
- [docs/features.md](docs/features.md) —— 各功能的操作位置与使用规则
- [docs/architecture.md](docs/architecture.md) —— 架构、模块边界、数据模型、扩展方式与迭代路线

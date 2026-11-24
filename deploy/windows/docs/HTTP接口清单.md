# 阳和财务系统 · HTTP 接口清单

供对接、集成或排障使用。所有接口均为 JSON。

---

## 一、通用约定

| 项目 | 约定 |
| --- | --- |
| Base URL | `http://<主机>:8080/api/v1` |
| 认证 | `Authorization: Bearer <token>` |
| 令牌来源 | `POST /auth/login`，默认有效期 12 小时 |
| 内容类型 | `application/json; charset=utf-8` |
| 金额 | **字符串**形式的十进制数，如 `"1234.56"`，不接受浮点误差 |
| 期间 | `YYYY-MM`，如 `2026-01` |
| 日期 | `YYYY-MM-DD` |
| 分页 | `page`（默认 1）、`page_size`（默认 50，上限 500） |
| 健康检查 | `GET /healthz`（**注意不在 /api/v1 下**，无需认证，返回 `ok` 或 503） |

### 认证流程

```bash
# 1. 首次使用：创建系统管理员（已初始化会返回 409）
curl -s -X POST http://127.0.0.1:8080/api/v1/setup \
  -H 'Content-Type: application/json' \
  -d '{"username":"admin","password":"secret1","display_name":"管理员"}'

# 2. 登录取令牌
curl -s -X POST http://127.0.0.1:8080/api/v1/auth/login \
  -H 'Content-Type: application/json' \
  -d '{"username":"admin","password":"secret1"}'
# → {"token":"eyJ...","expires_at":"2026-11-17T08:00:00Z","user":{...}}

# 3. 带令牌调用
TOKEN='eyJ...'
curl -s http://127.0.0.1:8080/api/v1/books -H "Authorization: Bearer $TOKEN"
```

### 错误结构

```json
{
  "error": {
    "code": "bad_request",
    "message": "第 1 行：请选择科目"
  }
}
```

| HTTP | code | 含义 |
| --- | --- | --- |
| 400 | `bad_request` | 参数或业务校验不通过 |
| 401 | `unauthorized` | 未登录或令牌过期 |
| 403 | `forbidden` | 无该账套权限或该权限点 |
| 404 | `not_found` | 资源不存在 |
| 409 | `conflict` | 状态冲突（期间已结账、凭证已审核、编码重复等） |
| 500 | `internal` | 服务器内部错误（不返回堆栈） |

---

## 二、系统与认证

| 方法 | 路径 | 说明 | 权限 |
| --- | --- | --- | --- |
| GET | `/healthz` | 健康检查（根路径） | 无 |
| GET | `/setup` | 是否已初始化 → `{"initialized":true}` | 无 |
| POST | `/setup` | 创建首个系统管理员，已初始化返回 409 | 无 |
| POST | `/auth/login` | 登录，返回 token | 无 |
| GET | `/auth/me` | 当前用户 | 登录 |
| POST | `/auth/password` | 修改自己的密码 | 登录 |

## 三、用户与元数据

| 方法 | 路径 | 说明 | 权限 |
| --- | --- | --- | --- |
| GET | `/users` | 用户列表 | 登录 |
| POST | `/users` | 新建用户 | 系统管理员 |
| PATCH | `/users/{userID}` | 改显示名 / 密码 / 停用 / 管理员标记 | 系统管理员 |
| GET | `/standards` | 可用会计准则模板 | 登录 |
| GET | `/roles` | 账套角色与权限 | 登录 |

## 四、账套

| 方法 | 路径 | 说明 | 权限 |
| --- | --- | --- | --- |
| GET | `/books` | 我参与的账套 | 登录 |
| POST | `/books` | 建账套（按准则模板建账） | 登录 |
| GET | `/books/{bookID}` | 账套详情（含账簿、期间、设置） | 成员 |
| PATCH | `/books/{bookID}` | 改账套设置（凭证字、平行记账模式等） | `book:admin` |
| DELETE | `/books/{bookID}` | 删除账套 | `book:admin` |
| GET | `/books/{bookID}/members` | 成员列表 | 成员 |
| PUT | `/books/{bookID}/members/{userID}` | 添加成员 / 改角色 | `book:admin` |
| DELETE | `/books/{bookID}/members/{userID}` | 移除成员 | `book:admin` |

## 五、科目与辅助核算

| 方法 | 路径 | 说明 | 权限 |
| --- | --- | --- | --- |
| GET | `/books/{id}/accounts` | 科目表 | 成员 |
| POST | `/books/{id}/accounts` | 新增科目 | `account:write` |
| PATCH | `/books/{id}/accounts/{accountID}` | 改名 / 停用 | `account:write` |
| DELETE | `/books/{id}/accounts/{accountID}` | 删除科目 | `account:write` |
| GET | `/books/{id}/aux/types` | 辅助核算类别 | 成员 |
| GET | `/books/{id}/aux/items` | 辅助核算项目 | 成员 |
| GET | `/books/{id}/aux/bindings` | 科目启用的类别 | 成员 |
| POST | `/books/{id}/aux/types` | 新增类别 | `aux:write` |
| PATCH | `/books/{id}/aux/types/{id}` | 修改类别 | `aux:write` |
| DELETE | `/books/{id}/aux/types/{id}` | 删除类别 | `aux:write` |
| POST | `/books/{id}/aux/items` | 新增项目 | `aux:write` |
| PATCH | `/books/{id}/aux/items/{id}` | 修改 / 停用项目 | `aux:write` |
| DELETE | `/books/{id}/aux/items/{id}` | 删除项目 | `aux:write` |

## 六、期初与账簿

| 方法 | 路径 | 说明 | 权限 |
| --- | --- | --- | --- |
| GET | `/books/{id}/opening` | 期初余额 | 成员 |
| GET | `/books/{id}/opening/check` | 期初试算平衡检查 | 成员 |
| PUT | `/books/{id}/opening` | 保存期初 | `opening:write` |
| GET | `/books/{id}/opening/aux/{accountID}` | 按辅助核算项目取期初 | 成员 |
| PUT | `/books/{id}/opening/aux/{accountID}` | 按项目保存期初 | `opening:write` |
| GET | `/books/{id}/ledger/trial-balance` | 科目余额表（必带 `ledger_id`；`show_zero=1` 显示零余额科目） | 成员 |
| GET | `/books/{id}/ledger/detail` | 明细账（`account_id` 必填） | 成员 |
| GET | `/books/{id}/ledger/general` | 总账（必带 `ledger_id`） | 成员 |
| GET | `/books/{id}/ledger/aux-balance` | 辅助核算余额表 | 成员 |
| GET | `/books/{id}/ledger/aux-detail` | 辅助核算明细账 | 成员 |

## 七、凭证与平行记账

| 方法 | 路径 | 说明 | 权限 |
| --- | --- | --- | --- |
| GET | `/books/{id}/vouchers` | 凭证列表（`from`/`to`/`word`/`status`/`keyword`/`account_id`/`desc`） | 成员 |
| GET | `/books/{id}/vouchers/{voucherID}` | 凭证详情 | 成员 |
| GET | `/books/{id}/vouchers/next-number` | 下一个凭证号 | 成员 |
| POST | `/books/{id}/vouchers/parallel-suggest` | 生成平行记账建议分录 | 成员 |
| POST | `/books/{id}/vouchers` | 新增凭证 | `voucher:write` |
| PUT | `/books/{id}/vouchers/{voucherID}` | 修改凭证 | `voucher:write` |
| DELETE | `/books/{id}/vouchers/{voucherID}` | 删除凭证 | `voucher:write` |
| POST | `/books/{id}/vouchers/renumber` | 整理凭证号 | `voucher:write` |
| POST | `/books/{id}/vouchers/review` | 批量审核 / 取消审核 | `voucher:review` |
| GET | `/books/{id}/vouchers/{voucherID}/attachments` | 凭证图片列表（无图返回 `[]`） | 成员 |
| GET | `/books/{id}/vouchers/{voucherID}/attachments/{attachmentID}` | 取图片二进制；`?thumb=1` 取缩略图 | 成员 |
| POST | `/books/{id}/vouchers/{voucherID}/attachments` | 上传图片（`multipart/form-data`） | `voucher:write` |
| DELETE | `/books/{id}/vouchers/{voucherID}/attachments/{attachmentID}` | 删除图片（204） | `voucher:write` |
| GET | `/books/{id}/parallel-rules` | 平行记账规则 | 成员 |
| POST | `/books/{id}/parallel-rules` | 新增规则 | `parallel:write` |
| DELETE | `/books/{id}/parallel-rules/{ruleID}` | 删除规则 | `parallel:write` |

### 凭证图片（附件）

图片不存数据库，按 `附件目录/attachments/{账套}/{凭证}/` 存放，目录内 `index.json` 记录文件名、大小、上传人、时间。

- 上传字段：`file`（必填，JPG / PNG / WebP）、`thumb`（可选，前端生成的 JPEG 缩略图）。
- 服务端只按文件头判断类型，不认扩展名与客户端声明的 `Content-Type`；**不接受 SVG**。
- 上限：单张 10MB（`YANGHE_FINANCE_STORAGE_MAX_FILE_MB`）、单张凭证 20 张（`YANGHE_FINANCE_STORAGE_MAX_FILES`）、单次请求体 12MB。
- 已审核凭证或已结账期间返回 `409 conflict`。
- 取图片必须带 `Authorization` 头（浏览器里不能用裸 `<img src>`，前端用 fetch + blob）。
- 附件目录不可写时，上传返回 `503 attachment_unavailable`，读取接口仍返回空列表。

## 八、期末与报表

| 方法 | 路径 | 说明 | 权限 |
| --- | --- | --- | --- |
| GET | `/books/{id}/closing/{period}` | 本期可执行的结转规则与状态 | 成员 |
| POST | `/books/{id}/closing/{period}` | 一键执行本期全部结转 | `closing:run` |
| POST | `/books/{id}/closing/{period}/{code}` | 执行单条结转规则 | `closing:run` |
| PUT | `/books/{id}/closing-rules/{code}` | 修改结转规则定义 | `book:admin` |
| GET | `/books/{id}/periods/{period}/check` | 结账前检查 | 成员 |
| POST | `/books/{id}/periods/{period}/close` | 结账 | `period:close` |
| POST | `/books/{id}/periods/{period}/reopen` | 反结账 | `period:close` |
| GET | `/books/{id}/reports` | 报表清单 | 成员 |
| GET | `/books/{id}/reports/{code}` | 报表数据（如 `balance_sheet`、`income_statement`） | 成员 |
| PUT | `/books/{id}/reports/{code}` | 修改报表模板 | `book:admin` |

## 九、批量导入

| 方法 | 路径 | 说明 | 权限 |
| --- | --- | --- | --- |
| POST | `/books/{id}/import/accounts` | 导入科目 | `account:write` |
| POST | `/books/{id}/import/aux-items` | 导入辅助核算项目 | `aux:write` |
| POST | `/books/{id}/import/openings` | 导入期初余额 | `opening:write` |

导入特点：整批同一事务，任一行出错则全部回滚，并返回逐行错误。

```json
{
  "applied": false,
  "created": 0,
  "updated": 0,
  "unchanged": 0,
  "errors": [{ "row": 3, "message": "上级科目 1002 不存在" }]
}
```

---

## 十、权限点对照

| 权限点 | 说明 | 默认拥有者 |
| --- | --- | --- |
| `*` | 全部权限 | 账套管理员 owner |
| `book:admin` | 账套设置、成员、报表与结转规则模板 | owner |
| `account:write` | 科目增删改 | owner、会计 |
| `aux:write` | 辅助核算类别与项目 | owner、会计 |
| `opening:write` | 期初余额 | owner、会计 |
| `voucher:write` | 凭证录入、修改、删除、整理号 | owner、会计 |
| `voucher:review` | 凭证审核 / 取消审核 | owner、审核 |
| `parallel:write` | 平行记账规则 | owner、会计 |
| `closing:run` | 执行期末结转 | owner、会计 |
| `period:close` | 结账 / 反结账 | owner、会计 |

内置角色：`owner`（账套管理员）、`accountant`（会计）、`reviewer`（审核）、`viewer`（查看）。角色定义在 `configs/roles.yaml`，可通过 `YANGHE_FINANCE_AUTH_ROLES_FILE` 指向自定义文件覆盖。

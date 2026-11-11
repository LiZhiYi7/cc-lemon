-- 平台
create table system_kv (
  key   text primary key,
  value text not null
);

create table users (
  id            bigserial primary key,
  username      text not null unique,
  password_hash text not null,
  display_name  text not null default '',
  is_admin      boolean not null default false,
  enabled       boolean not null default true,
  created_at    timestamptz not null default now()
);

create table audit_logs (
  id         bigserial primary key,
  book_id    bigint,
  user_id    bigint,
  action     text not null,
  entity     text not null default '',
  entity_id  text not null default '',
  detail     jsonb not null default '{}',
  created_at timestamptz not null default now()
);
create index audit_logs_book_idx on audit_logs (book_id, created_at desc);

-- 账套
create table books (
  id            bigserial primary key,
  name          text not null,
  company       text not null default '',
  standard_code text not null,
  start_year    int  not null,
  start_month   int  not null check (start_month between 1 and 12),
  settings      jsonb not null default '{}',
  created_by    bigint references users(id),
  created_at    timestamptz not null default now()
);

create table book_members (
  book_id bigint not null references books(id) on delete cascade,
  user_id bigint not null references users(id) on delete cascade,
  role    text not null,
  primary key (book_id, user_id)
);

-- 核算账簿（会计体系）。单体系账套只有一个；平行记账账套有多个（如 财务会计 / 预算会计）。
create table ledgers (
  id         bigserial primary key,
  book_id    bigint not null references books(id) on delete cascade,
  code       text not null,
  name       text not null,
  sort       int  not null default 0,
  is_primary boolean not null default false,
  unique (book_id, code)
);

create table account_categories (
  book_id   bigint not null references books(id) on delete cascade,
  ledger_id bigint not null references ledgers(id) on delete cascade,
  code      text not null,
  name      text not null,
  sort      int  not null default 0,
  is_pl     boolean not null default false, -- 损益/收支类：期初只录累计发生额，期末需结转
  primary key (ledger_id, code)
);

create table accounts (
  id        bigserial primary key,
  book_id   bigint not null references books(id) on delete cascade,
  ledger_id bigint not null references ledgers(id) on delete cascade,
  code      text not null,
  name      text not null,
  parent_id bigint references accounts(id),
  level     int  not null,
  category  text not null,
  direction char(1) not null check (direction in ('D','C')),
  is_leaf   boolean not null default true,
  is_cash   boolean not null default false,
  enabled   boolean not null default true,
  is_system boolean not null default false,
  attrs     jsonb not null default '{}', -- 扩展点：辅助核算 / 数量 / 外币
  unique (ledger_id, code)
);
create index accounts_book_idx on accounts (book_id, ledger_id, code);

create table opening_balances (
  book_id    bigint not null references books(id) on delete cascade,
  account_id bigint primary key references accounts(id) on delete cascade,
  balance    numeric(18,2) not null default 0, -- 期初余额（按科目方向）
  ytd_debit  numeric(18,2) not null default 0, -- 启用年度年初至启用期的累计借方
  ytd_credit numeric(18,2) not null default 0
);

create table periods (
  book_id   bigint not null references books(id) on delete cascade,
  year      int not null,
  month     int not null check (month between 1 and 12),
  closed    boolean not null default false,
  closed_at timestamptz,
  closed_by bigint references users(id),
  primary key (book_id, year, month)
);

create table voucher_words (
  book_id    bigint not null references books(id) on delete cascade,
  word       text not null,
  title      text not null default '',
  is_default boolean not null default false,
  sort       int not null default 0,
  primary key (book_id, word)
);

create table vouchers (
  id          bigserial primary key,
  book_id     bigint not null references books(id) on delete cascade,
  year        int  not null,
  month       int  not null,
  word        text not null,
  number      int  not null,
  vdate       date not null,
  attachments int  not null default 0,
  status      text not null default 'saved' check (status in ('saved','reviewed')),
  source      text not null default 'manual', -- manual | closing:<规则编码> | 其他业务模块
  remark      text not null default '',
  created_by  bigint references users(id),
  created_at  timestamptz not null default now(),
  updated_at  timestamptz not null default now(),
  reviewed_by bigint references users(id),
  reviewed_at timestamptz,
  unique (book_id, year, month, word, number) deferrable initially deferred
);
create index vouchers_period_idx on vouchers (book_id, year, month);

create table voucher_lines (
  id         bigserial primary key,
  voucher_id bigint not null references vouchers(id) on delete cascade,
  book_id    bigint not null references books(id) on delete cascade,
  ledger_id  bigint not null references ledgers(id),
  line_no    int  not null,
  summary    text not null default '',
  account_id bigint not null references accounts(id),
  debit      numeric(18,2) not null default 0,
  credit     numeric(18,2) not null default 0,
  attrs      jsonb not null default '{}',
  check ((debit <> 0 and credit = 0) or (debit = 0 and credit <> 0))
);
create index voucher_lines_voucher_idx on voucher_lines (voucher_id);
create index voucher_lines_account_idx on voucher_lines (book_id, account_id);

-- 账套级可配置定义（建账时从会计准则模板复制，之后可按账套调整）
--   kind = report        报表模板
--   kind = closing_rule  期末结转规则
create table book_configs (
  book_id    bigint not null references books(id) on delete cascade,
  kind       text not null,
  code       text not null,
  name       text not null,
  sort       int  not null default 0,
  definition jsonb not null,
  primary key (book_id, kind, code)
);

-- 平行记账规则：来源账簿科目 → 目标账簿科目
create table parallel_rules (
  id               bigserial primary key,
  book_id          bigint not null references books(id) on delete cascade,
  source_ledger_id bigint not null references ledgers(id) on delete cascade,
  source_code      text not null,
  target_ledger_id bigint not null references ledgers(id) on delete cascade,
  target_code      text not null,
  side             text not null default '' check (side in ('', 'D', 'C')), -- 仅在借方/贷方发生时适用
  note             text not null default '',
  unique (source_ledger_id, source_code, target_ledger_id, side)
);

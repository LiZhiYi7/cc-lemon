-- 辅助核算
create table aux_types (
  id        bigserial primary key,
  book_id   bigint not null references books(id) on delete cascade,
  code      text not null,
  name      text not null,
  sort      int  not null default 0,
  is_system boolean not null default false,
  unique (book_id, code)
);

create table aux_items (
  id      bigserial primary key,
  book_id bigint not null references books(id) on delete cascade,
  type_id bigint not null references aux_types(id) on delete cascade,
  code    text not null,
  name    text not null,
  enabled boolean not null default true,
  remark  text not null default '',
  unique (type_id, code)
);

-- 科目启用了哪些辅助核算类别（仅末级科目）
create table account_aux_types (
  account_id bigint not null references accounts(id) on delete cascade,
  type_id    bigint not null references aux_types(id),
  primary key (account_id, type_id)
);

-- 分录的辅助核算项目：科目启用的每个类别各一条
create table voucher_line_aux (
  line_id bigint not null references voucher_lines(id) on delete cascade,
  type_id bigint not null references aux_types(id),
  item_id bigint not null references aux_items(id),
  primary key (line_id, type_id)
);
create index voucher_line_aux_item_idx on voucher_line_aux (item_id);

-- 启用辅助核算的科目按“项目组合”录入期初；opening_balances 中该科目的行为各组合之和
create table opening_aux (
  id         bigserial primary key,
  book_id    bigint not null references books(id) on delete cascade,
  account_id bigint not null references accounts(id) on delete cascade,
  balance    numeric(18,2) not null default 0,
  ytd_debit  numeric(18,2) not null default 0,
  ytd_credit numeric(18,2) not null default 0
);
create index opening_aux_account_idx on opening_aux (account_id);

create table opening_aux_items (
  opening_id bigint not null references opening_aux(id) on delete cascade,
  type_id    bigint not null references aux_types(id),
  item_id    bigint not null references aux_items(id),
  primary key (opening_id, type_id)
);

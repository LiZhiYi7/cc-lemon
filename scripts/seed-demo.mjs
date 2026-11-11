#!/usr/bin/env node
// 生成演示数据：通过 HTTP 接口建两套账并录入几个月的业务，用于预览与培训。
//
//   node scripts/seed-demo.mjs
//
// 环境变量：
//   LEMON_URL       服务地址，默认 http://localhost:8080
//   LEMON_ADMIN     管理员用户名，默认 admin
//   LEMON_PASSWORD  管理员密码，默认 lemon-demo（系统未初始化时会用它创建管理员）
//
// 可重复执行：同名账套已存在时跳过。只应对演示/测试环境使用。

const base = (process.env.LEMON_URL || 'http://localhost:8080').replace(/\/$/, '') + '/api/v1'
const username = process.env.LEMON_ADMIN || 'admin'
const password = process.env.LEMON_PASSWORD || 'lemon-demo'
let token = ''

async function api(method, path, body) {
  const res = await fetch(base + path, {
    method,
    headers: { 'Content-Type': 'application/json', ...(token ? { Authorization: 'Bearer ' + token } : {}) },
    body: body === undefined ? undefined : JSON.stringify(body),
  })
  if (res.status === 204) return null
  const data = await res.json().catch(() => null)
  if (!res.ok) throw new Error(`${method} ${path} → ${res.status}: ${data?.error?.message ?? ''}`)
  return data
}

/** 金额转两位小数字符串，避免浮点误差带出多余小数。 */
const money = (n) => (Math.round((n || 0) * 100) / 100).toFixed(2)

/** 打开一个账套，返回按编码查科目、按“类别/编码”查辅助核算项目等便捷方法。 */
async function openBook(id) {
  const b = `/books/${id}`
  const ctx = { base: b, accounts: {}, auxTypes: {}, auxItems: {} }
  ctx.reload = async () => {
    for (const a of await api('GET', b + '/accounts')) ctx.accounts[a.code] = a.id
    const types = await api('GET', b + '/aux/types')
    for (const t of types) ctx.auxTypes[t.code] = t.id
    for (const it of await api('GET', b + '/aux/items')) {
      const type = types.find((t) => t.id === it.type_id)
      ctx.auxItems[`${type.code}/${it.code}`] = it.id
    }
  }
  /** 分录：line('1002', 100, 0, { fund_nature: '01' }) */
  ctx.line = (summary, code, debit, credit, aux = {}) => ({
    summary,
    account_id: ctx.accounts[code],
    debit: money(debit),
    credit: money(credit),
    aux: Object.entries(aux).map(([type, item]) => ({ type_id: ctx.auxTypes[type], item_id: ctx.auxItems[`${type}/${item}`] })),
  })
  ctx.post = (date, lines) => api('POST', b + '/vouchers', { date, lines })
  ctx.addItems = async (type, items) => {
    for (const [code, name] of items) await api('POST', b + '/aux/items', { type_id: ctx.auxTypes[type], code, name })
    await ctx.reload()
  }
  ctx.bind = (code, ...types) => api('PUT', `${b}/accounts/${ctx.accounts[code]}/aux`, { type_ids: types.map((t) => ctx.auxTypes[t]) })
  /** 结转、审核并结账一个期间。 */
  ctx.closePeriod = async (period) => {
    await api('POST', `${b}/closing/${period}`)
    const page = await api('GET', `${b}/vouchers?from=${period}&to=${period}&page_size=500`)
    await api('POST', b + '/vouchers/review', { ids: page.items.map((v) => v.id), reviewed: true })
    await api('POST', `${b}/periods/${period}/close`)
  }
  await ctx.reload()
  return ctx
}

async function createBook(existing, input) {
  if (existing.some((b) => b.name === input.name)) {
    console.log(`· 账套“${input.name}”已存在，跳过`)
    return null
  }
  const b = await api('POST', '/books', input)
  console.log(`· 已创建账套“${input.name}”`)
  return openBook(b.id)
}

// ---------- 行政事业单位：平行记账 ----------
async function seedGov(existing, year) {
  const k = await createBook(existing, {
    name: '示范事业单位（演示）',
    company: '某市科技服务中心',
    standard_code: 'gov_accounting',
    start_year: year,
    start_month: 1,
  })
  if (!k) return

  await k.addItems('department', [['01', '办公室'], ['02', '业务一部'], ['03', '业务二部']])
  await k.addItems('supplier', [['001', '市政府采购中心'], ['002', '宏图办公用品公司']])
  await k.bind('5101', 'department') // 单位管理费用按部门核算
  await k.bind('2302', 'supplier') // 应付账款按供应商核算

  // 期初：财务会计与预算会计各自平衡
  await api('PUT', k.base + '/opening', {
    items: [
      { account_id: k.accounts['1001'], balance: '5000' },
      { account_id: k.accounts['1002'], balance: '495000' },
      { account_id: k.accounts['1601'], balance: '800000' },
      { account_id: k.accounts['1602'], balance: '200000' },
      { account_id: k.accounts['3001'], balance: '1100000' },
      { account_id: k.accounts['800102'], balance: '500000' },
      { account_id: k.accounts['8101'], balance: '300000' },
      { account_id: k.accounts['8202'], balance: '200000' },
    ],
  })

  // 财务会计分录 + 平行生成的预算会计分录。fund 为预算分录的资金性质。
  const parallel = async (date, fin, fund) => {
    const res = await api('POST', k.base + '/vouchers/parallel-suggest', { lines: fin })
    const bud = res.lines.map((l) => ({
      summary: l.summary,
      account_id: l.account_id,
      debit: l.debit,
      credit: l.credit,
      // 目标科目启用了资金性质时补上
      aux: needsFund.has(l.account_id) ? [{ type_id: k.auxTypes.fund_nature, item_id: k.auxItems[`fund_nature/${fund}`] }] : [],
    }))
    return k.post(date, [...fin, ...bud])
  }
  const bindings = await api('GET', k.base + '/aux/bindings')
  const needsFund = new Set(Object.keys(bindings).filter((id) => bindings[id].includes(k.auxTypes.fund_nature)).map(Number))

  const months = [1, 2, 3]
  for (const m of months) {
    const d = (day) => `${year}-${String(m).padStart(2, '0')}-${String(day).padStart(2, '0')}`
    await parallel(d(5), [k.line('收到财政授权支付额度', '1002', 200000, 0), k.line('收到财政授权支付额度', '4001', 0, 200000)], '01')
    await parallel(d(8), [k.line('收到技术服务收入', '1002', 80000 + m * 5000, 0), k.line('收到技术服务收入', '4101', 0, 80000 + m * 5000)], '03')
    await parallel(d(10), [k.line('发放本月职工薪酬', '5001', 120000, 0), k.line('发放本月职工薪酬', '1002', 0, 120000)], '01')
    await parallel(
      d(15),
      [
        k.line('支付办公费', '5101', 9000, 0, { department: '01' }),
        k.line('支付差旅费', '5101', 6000 + m * 800, 0, { department: '02' }),
        k.line('支付办公费、差旅费', '1002', 0, 15000 + m * 800),
      ],
      '03',
    )
    await k.post(d(28), [k.line('计提本月固定资产折旧', '5001', 10000, 0), k.line('计提本月固定资产折旧', '1602', 0, 10000)])
    if (m === 2) {
      await k.post(d(20), [
        k.line('赊购办公用品', '5101', 4200, 0, { department: '03' }),
        k.line('赊购办公用品', '2302', 0, 4200, { supplier: '002' }),
      ])
    }
    if (m === 3) {
      await parallel(d(12), [k.line('收到专项课题经费', '1002', 30000, 0), k.line('收到专项课题经费', '4101', 0, 30000)], '02')
      await parallel(d(18), [k.line('购置专用设备', '1601', 50000, 0), k.line('购置专用设备', '1002', 0, 50000)], '01')
      await parallel(
        d(22),
        [k.line('支付上月办公用品款', '2302', 4200, 0, { supplier: '002' }), k.line('支付上月办公用品款', '1002', 0, 4200)],
        '03',
      )
    }
    // 前两个月结账，三月保持未结账，便于体验录入、审核与结转
    if (m < 3) await k.closePeriod(`${year}-${String(m).padStart(2, '0')}`)
  }
  console.log('  已录入 1–3 月业务，1、2 月已结账')
}

// ---------- 小企业：辅助核算 ----------
async function seedSmall(existing, year) {
  const k = await createBook(existing, {
    name: '示范商贸公司（演示）',
    company: '示范商贸有限公司',
    standard_code: 'small_enterprise',
    start_year: year,
    start_month: 1,
  })
  if (!k) return

  await k.addItems('customer', [['001', '华东百货'], ['002', '江南超市'], ['003', '城北便利店']])
  await k.addItems('supplier', [['001', '恒昌食品厂'], ['002', '远大物流']])
  await k.addItems('department', [['01', '行政部'], ['02', '销售部']])
  await k.bind('1122', 'customer')
  await k.bind('2202', 'supplier')
  await k.bind('5601', 'department')
  await k.bind('5602', 'department')

  await api('PUT', `${k.base}/opening/aux/${k.accounts['1122']}`, {
    items: [
      { aux: [{ type_id: k.auxTypes.customer, item_id: k.auxItems['customer/001'] }], balance: '60000' },
      { aux: [{ type_id: k.auxTypes.customer, item_id: k.auxItems['customer/002'] }], balance: '25000' },
    ],
  })
  await api('PUT', `${k.base}/opening/aux/${k.accounts['2202']}`, {
    items: [{ aux: [{ type_id: k.auxTypes.supplier, item_id: k.auxItems['supplier/001'] }], balance: '40000' }],
  })
  await api('PUT', k.base + '/opening', {
    items: [
      { account_id: k.accounts['1001'], balance: '8000' },
      { account_id: k.accounts['1002'], balance: '300000' },
      { account_id: k.accounts['1405'], balance: '150000' },
      { account_id: k.accounts['1601'], balance: '120000' },
      { account_id: k.accounts['1602'], balance: '23000' },
      { account_id: k.accounts['3001'], balance: '500000' },
      { account_id: k.accounts['310415'], balance: '100000' },
    ],
  })

  for (const m of [1, 2, 3]) {
    const d = (day) => `${year}-${String(m).padStart(2, '0')}-${String(day).padStart(2, '0')}`
    const sale = 90000 + m * 12000
    await k.post(d(6), [
      k.line('销售商品', '1122', sale, 0, { customer: m === 2 ? '002' : '001' }),
      k.line('销售商品', '5001', 0, sale),
    ])
    await k.post(d(9), [k.line('结转销售成本', '5401', sale * 0.7, 0), k.line('结转销售成本', '1405', 0, sale * 0.7)])
    await k.post(d(12), [
      k.line('采购商品', '1405', 70000, 0),
      k.line('采购商品', '2202', 0, 70000, { supplier: '001' }),
    ])
    await k.post(d(16), [
      k.line('收回货款', '1002', 60000, 0),
      k.line('收回货款', '1122', 0, 60000, { customer: '001' }),
    ])
    await k.post(d(20), [
      k.line('支付货款', '2202', 50000, 0, { supplier: '001' }),
      k.line('支付货款', '1002', 0, 50000),
    ])
    await k.post(d(25), [
      k.line('支付办公费', '5602', 6500, 0, { department: '01' }),
      k.line('支付业务宣传费', '5601', 8800 + m * 500, 0, { department: '02' }),
      k.line('支付费用', '1002', 0, 15300 + m * 500),
    ])
    await k.post(d(28), [
      k.line('计提折旧', '5602', 2000, 0, { department: '01' }),
      k.line('计提折旧', '1602', 0, 2000),
    ])
    if (m < 3) await k.closePeriod(`${year}-${String(m).padStart(2, '0')}`)
  }
  console.log('  已录入 1–3 月业务，1、2 月已结账')
}

async function main() {
  const setup = await api('GET', '/setup')
  if (!setup.initialized) {
    await api('POST', '/setup', { username, password, display_name: '管理员' })
    console.log(`· 已创建管理员 ${username}`)
  }
  token = (await api('POST', '/auth/login', { username, password })).token
  const existing = await api('GET', '/books')
  const year = new Date().getFullYear()
  await seedGov(existing, year)
  await seedSmall(existing, year)
  console.log(`完成。登录：${username} / ${password}`)
}

main().catch((e) => {
  console.error('生成演示数据失败：' + e.message)
  process.exit(1)
})

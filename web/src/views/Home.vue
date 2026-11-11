<script setup lang="ts">
// 首页：本期概况 + 快捷入口。快捷入口来自 config/menu.ts 中带 quick 的菜单项与账套的报表模板。
import { computed, onMounted, ref } from 'vue'
import { useRouter } from 'vue-router'
import { http } from '@/api/http'
import type { CheckItem, ConfigItem, TrialRow, Voucher } from '@/api/types'
import { menu } from '@/config/menu'
import { useBook } from '@/stores/book'
import { fmt, fmtCents, toCents } from '@/utils/money'
import { periodStr } from '@/utils/period'

const book = useBook()
const router = useRouter()
const detail = book.detail!
const period = periodStr(detail.current_period)
const go = (path: string) => router.push(`/b/${detail.id}/${path}`)

const total = ref(0)
const unreviewed = ref(0)
const recent = ref<Voucher[]>([])
const cash = ref(0)
const checks = ref<CheckItem[]>([])
const reports = ref<ConfigItem[]>([])
const loading = ref(true)

const quick = computed(() =>
  menu.flatMap((g) =>
    g.items
      .filter((i) => i.quick && (!i.perm || book.can(i.perm)) && (!i.multiLedgerOnly || book.multiLedger))
      .map((i) => ({ ...i, icon: g.icon })),
  ),
)
const blocking = computed(() => checks.value.filter((c) => !c.ok && c.blocking))
const hints = computed(() => checks.value.filter((c) => !c.ok && !c.blocking))

onMounted(async () => {
  try {
    const range = { from: period, to: period }
    const [page, pending, trial, ck, rp] = await Promise.all([
      http.get<{ items: Voucher[]; total: number }>(`${book.base}/vouchers`, { ...range, page_size: 6, order: 'desc' }),
      http.get<{ total: number }>(`${book.base}/vouchers`, { ...range, status: 'saved', page_size: 1 }),
      http.get<{ rows: TrialRow[] }>(`${book.base}/ledger/trial-balance`, { ...range, ledger_id: book.primaryLedger!.id }),
      http.get<CheckItem[]>(`${book.base}/periods/${period}/check`),
      http.get<ConfigItem[]>(`${book.base}/reports`),
    ])
    total.value = page.total
    recent.value = page.items
    unreviewed.value = pending.total
    checks.value = ck
    reports.value = rp
    // 货币资金 = 现金类末级科目的期末余额合计
    cash.value = trial.rows.reduce((sum, r) => {
      const a = book.accountById.get(r.account_id)
      return a?.is_cash && a.is_leaf ? sum + toCents(r.end_debit) - toCents(r.end_credit) : sum
    }, 0)
  } finally {
    loading.value = false
  }
})
</script>

<template>
  <div class="page" v-loading="loading">
    <div class="stats">
      <div class="card stat">
        <div class="label">当前期间</div>
        <div class="value">{{ detail.current_period.year }} 年 {{ detail.current_period.month }} 月</div>
        <div class="muted">{{ detail.standard_name }}</div>
      </div>
      <div class="card stat link-card" @click="go('vouchers')">
        <div class="label">本期凭证</div>
        <div class="value">{{ total }} <small>张</small></div>
        <div :class="unreviewed ? 'warn' : 'muted'">{{ unreviewed ? `${unreviewed} 张未审核` : '全部已审核' }}</div>
      </div>
      <div class="card stat link-card" @click="go('ledger/trial')">
        <div class="label">货币资金余额</div>
        <div class="value num">{{ fmtCents(cash, '0.00') }}</div>
        <div class="muted">现金类科目期末余额合计</div>
      </div>
      <div class="card stat link-card" @click="go('closing')">
        <div class="label">期末进度</div>
        <div class="value">
          <el-tag v-if="!blocking.length" type="success" size="large" disable-transitions>可以结账</el-tag>
          <el-tag v-else type="warning" size="large" disable-transitions>{{ blocking.length }} 项待处理</el-tag>
        </div>
        <div class="muted ellipsis">{{ blocking[0]?.name ?? hints[0]?.message ?? '结账检查全部通过' }}</div>
      </div>
    </div>

    <h3 class="section">快捷入口</h3>
    <div class="quick">
      <div v-for="q in quick" :key="q.path" class="card entry" @click="go(q.path)">
        <el-icon class="icon"><component :is="q.icon" /></el-icon>
        <div>
          <div class="title">{{ q.title }}</div>
          <div class="muted">{{ q.quick }}</div>
        </div>
      </div>
    </div>

    <div class="columns">
      <div>
        <h3 class="section">
          最近凭证
          <a class="link more" @click="go('vouchers')">全部 ›</a>
        </h3>
        <table class="sheet">
          <thead>
            <tr>
              <th style="width: 100px">日期</th>
              <th style="width: 76px">凭证字号</th>
              <th>摘要</th>
              <th style="width: 130px">金额</th>
              <th style="width: 80px">状态</th>
            </tr>
          </thead>
          <tbody>
            <tr v-for="v in recent" :key="v.id" class="row" @click="go(`vouchers/${v.id}`)">
              <td class="center">{{ v.date }}</td>
              <td class="center link">{{ v.word }}-{{ v.number }}</td>
              <td>{{ v.lines[0]?.summary }}</td>
              <td class="num">{{ fmt(v.total, '0.00') }}</td>
              <td class="center">
                <el-tag :type="v.status === 'reviewed' ? 'success' : 'info'" size="small" disable-transitions>{{ v.status === 'reviewed' ? '已审核' : '未审核' }}</el-tag>
              </td>
            </tr>
            <tr v-if="!recent.length">
              <td colspan="5" class="center muted" style="padding: 32px">
                本期还没有凭证
                <el-button v-if="book.can('voucher:write')" type="primary" link @click="go('vouchers/new')">去录入</el-button>
              </td>
            </tr>
          </tbody>
        </table>
      </div>
      <div>
        <h3 class="section">报表</h3>
        <div class="card reports">
          <a v-for="r in reports" :key="r.code" class="report" @click="go(`reports/${r.code}`)">
            <span>{{ r.name }}</span>
            <span class="muted">›</span>
          </a>
        </div>
      </div>
    </div>
  </div>
</template>

<style scoped>
.stats { display: grid; grid-template-columns: repeat(4, minmax(0, 1fr)); gap: 14px; }
.stat .label { color: #8a94a6; font-size: 13px; }
.stat .value { font-size: 24px; font-weight: 600; margin: 8px 0 6px; min-height: 34px; text-align: left; }
.stat .value small { font-size: 14px; font-weight: normal; color: #8a94a6; }
.link-card, .entry { cursor: pointer; transition: box-shadow 0.15s, border-color 0.15s; }
.link-card:hover, .entry:hover { border-color: var(--el-color-primary); box-shadow: 0 2px 10px rgba(30, 60, 120, 0.1); }
.warn { color: #e6a23c; }
.ellipsis { overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.section { font-size: 15px; margin: 22px 0 10px; display: flex; align-items: center; }
.more { margin-left: auto; font-size: 13px; font-weight: normal; }
.quick { display: grid; grid-template-columns: repeat(auto-fill, minmax(230px, 1fr)); gap: 12px; }
.entry { display: flex; align-items: center; gap: 14px; padding: 14px 16px; }
.entry .icon { font-size: 26px; color: var(--el-color-primary); background: #eef3ff; border-radius: 8px; padding: 9px; box-sizing: content-box; }
.entry .title { font-weight: 600; margin-bottom: 2px; }
.entry .muted { font-size: 12px; }
.columns { display: grid; grid-template-columns: minmax(0, 2fr) minmax(220px, 1fr); gap: 20px; }
.row { cursor: pointer; }
.reports { padding: 4px 16px; }
.report { display: flex; justify-content: space-between; padding: 11px 0; border-bottom: 1px dashed var(--lemon-border); cursor: pointer; color: inherit; }
.report:last-child { border-bottom: none; }
.report:hover span:first-child { color: var(--el-color-primary); }
@media (max-width: 1100px) {
  .stats { grid-template-columns: repeat(2, minmax(0, 1fr)); }
  .columns { grid-template-columns: 1fr; }
}
</style>

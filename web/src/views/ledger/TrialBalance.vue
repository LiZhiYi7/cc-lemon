<script setup lang="ts">
import { onMounted, reactive, ref } from 'vue'
import { useRouter } from 'vue-router'
import { http } from '@/api/http'
import type { TrialRow } from '@/api/types'
import { useBook } from '@/stores/book'
import { fmt } from '@/utils/money'
import { periodStr } from '@/utils/period'
import PeriodRange from '@/components/PeriodRange.vue'
import LedgerSwitch from '@/components/LedgerSwitch.vue'

const book = useBook()
const router = useRouter()
const cur = periodStr(book.detail!.current_period)
const q = reactive({ from: cur, to: cur, ledger_id: book.primaryLedger!.id, show_zero: false })
const rows = ref<TrialRow[]>([])
const total = ref<TrialRow | null>(null)
const loading = ref(false)
const cols = ['open_debit', 'open_credit', 'debit', 'credit', 'ytd_debit', 'ytd_credit', 'end_debit', 'end_credit'] as const

async function load() {
  loading.value = true
  try {
    const res = await http.get<{ rows: TrialRow[]; total: TrialRow }>(`${book.base}/ledger/trial-balance`, { ...q, show_zero: q.show_zero ? 1 : 0 })
    rows.value = res.rows
    total.value = res.total
  } finally {
    loading.value = false
  }
}
onMounted(load)
const detail = (r: TrialRow) =>
  router.push({ path: `/b/${book.detail!.id}/ledger/detail`, query: { account_id: r.account_id, from: q.from, to: q.to } })
const print = () => window.print()
</script>

<template>
  <div class="page">
    <div class="toolbar no-print">
      <h2 class="page-title">科目余额表</h2>
      <LedgerSwitch v-model="q.ledger_id" @change="load" />
      <PeriodRange v-model:from="q.from" v-model:to="q.to" />
      <el-checkbox v-model="q.show_zero">显示无数据科目</el-checkbox>
      <el-button type="primary" @click="load">查询</el-button>
      <span class="spacer" />
      <el-button @click="print">打印</el-button>
    </div>
    <div v-loading="loading" class="sheet-wrap">
      <table class="sheet">
        <thead>
          <tr>
            <th rowspan="2">科目编码</th>
            <th rowspan="2">科目名称</th>
            <th colspan="2">期初余额</th>
            <th colspan="2">本期发生额</th>
            <th colspan="2">本年累计发生额</th>
            <th colspan="2">期末余额</th>
          </tr>
          <tr><template v-for="i in 4" :key="i"><th>借方</th><th>贷方</th></template></tr>
        </thead>
        <tbody>
          <tr v-for="r in rows" :key="r.account_id" :class="{ bold: r.level === 1 }">
            <td><a class="link" @click="detail(r)">{{ r.code }}</a></td>
            <td :style="{ paddingLeft: 10 + (r.level - 1) * 16 + 'px' }">{{ r.name }}</td>
            <td v-for="c in cols" :key="c" class="num">{{ fmt(r[c]) }}</td>
          </tr>
          <tr v-if="!rows.length"><td colspan="10" class="center muted" style="padding: 40px">暂无数据</td></tr>
        </tbody>
        <tfoot v-if="total">
          <tr>
            <td colspan="2" class="center">合计</td>
            <td v-for="c in cols" :key="c" class="num">{{ fmt(total[c]) }}</td>
          </tr>
        </tfoot>
      </table>
    </div>
  </div>
</template>

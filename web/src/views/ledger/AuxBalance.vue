<script setup lang="ts">
import { computed, onMounted, reactive, ref } from 'vue'
import { useRouter } from 'vue-router'
import { http } from '@/api/http'
import type { AuxBalanceRow } from '@/api/types'
import { useBook } from '@/stores/book'
import { fmt } from '@/utils/money'
import { periodStr } from '@/utils/period'
import PeriodRange from '@/components/PeriodRange.vue'
import AccountSelect from '@/components/AccountSelect.vue'

const book = useBook()
const router = useRouter()
const cur = periodStr(book.detail!.current_period)
const q = reactive({ type_id: book.auxTypes[0]?.id ?? 0, account_id: undefined as number | undefined, from: cur, to: cur })
const rows = ref<AuxBalanceRow[]>([])
const total = ref<AuxBalanceRow | null>(null)
const loading = ref(false)
const cols = ['open_debit', 'open_credit', 'debit', 'credit', 'end_debit', 'end_credit'] as const
const typeName = computed(() => book.auxTypes.find((t) => t.id === q.type_id)?.name ?? '项目')

async function load() {
  if (!q.type_id) return
  loading.value = true
  try {
    const res = await http.get<{ rows: AuxBalanceRow[]; total: AuxBalanceRow }>(`${book.base}/ledger/aux-balance`, q)
    rows.value = res.rows
    total.value = res.total
  } finally {
    loading.value = false
  }
}
onMounted(load)
// 同一项目跨多个科目时，项目列合并显示
const span = (i: number) => {
  const r = rows.value[i]
  if (i > 0 && rows.value[i - 1].item_id === r.item_id) return 0
  let n = 1
  while (rows.value[i + n]?.item_id === r.item_id) n++
  return n
}
const detail = (r: AuxBalanceRow) =>
  router.push({
    path: `/b/${book.detail!.id}/ledger/aux-detail`,
    query: { type_id: q.type_id, item_id: r.item_id, account_id: r.account_id, from: q.from, to: q.to },
  })
const print = () => window.print()
</script>

<template>
  <div class="page">
    <div class="toolbar no-print">
      <h2 class="page-title">辅助核算余额表</h2>
      <el-select v-model="q.type_id" style="width: 130px" @change="load">
        <el-option v-for="t in book.auxTypes" :key="t.id" :value="t.id" :label="t.name" />
      </el-select>
      <span style="width: 240px"><AccountSelect v-model="q.account_id" clearable placeholder="全部科目" @change="load" /></span>
      <PeriodRange v-model:from="q.from" v-model:to="q.to" />
      <el-button type="primary" @click="load">查询</el-button>
      <span class="spacer" />
      <el-button @click="print">打印</el-button>
    </div>
    <div v-loading="loading" class="sheet-wrap">
      <table class="sheet">
        <thead>
          <tr>
            <th rowspan="2">{{ typeName }}</th>
            <th rowspan="2">科目</th>
            <th colspan="2">期初余额</th>
            <th colspan="2">本期发生额</th>
            <th colspan="2">期末余额</th>
          </tr>
          <tr><template v-for="i in 3" :key="i"><th>借方</th><th>贷方</th></template></tr>
        </thead>
        <tbody>
          <tr v-for="(r, i) in rows" :key="`${r.item_id}-${r.account_id}`">
            <td v-if="span(i)" :rowspan="span(i)">{{ r.item_code }} {{ r.item_name }}</td>
            <td><a class="link" @click="detail(r)">{{ r.account_code }} {{ r.account_name }}</a></td>
            <td v-for="c in cols" :key="c" class="num">{{ fmt(r[c]) }}</td>
          </tr>
          <tr v-if="!rows.length"><td colspan="8" class="center muted" style="padding: 40px">暂无数据</td></tr>
        </tbody>
        <tfoot v-if="total && rows.length">
          <tr>
            <td colspan="2" class="center">合计</td>
            <td v-for="c in cols" :key="c" class="num">{{ fmt(total[c]) }}</td>
          </tr>
        </tfoot>
      </table>
    </div>
  </div>
</template>

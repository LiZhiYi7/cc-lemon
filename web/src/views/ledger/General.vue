<script setup lang="ts">
import { onMounted, reactive, ref } from 'vue'
import { http } from '@/api/http'
import type { GeneralAccount } from '@/api/types'
import { useBook } from '@/stores/book'
import { fmt } from '@/utils/money'
import { periodStr } from '@/utils/period'
import PeriodRange from '@/components/PeriodRange.vue'
import LedgerSwitch from '@/components/LedgerSwitch.vue'

const book = useBook()
const cur = periodStr(book.detail!.current_period)
const q = reactive({ from: cur, to: cur, ledger_id: book.primaryLedger!.id })
const list = ref<GeneralAccount[]>([])
const loading = ref(false)

async function load() {
  loading.value = true
  try {
    list.value = await http.get<GeneralAccount[]>(`${book.base}/ledger/general`, q)
  } finally {
    loading.value = false
  }
}
onMounted(load)
const print = () => window.print()
</script>

<template>
  <div class="page">
    <div class="toolbar no-print">
      <h2 class="page-title">总账</h2>
      <LedgerSwitch v-model="q.ledger_id" @change="load" />
      <PeriodRange v-model:from="q.from" v-model:to="q.to" />
      <el-button type="primary" @click="load">查询</el-button>
      <span class="spacer" />
      <el-button @click="print">打印</el-button>
    </div>
    <div v-loading="loading" class="sheet-wrap">
      <table class="sheet">
        <thead>
          <tr>
            <th style="width: 90px">科目编码</th>
            <th>科目名称</th>
            <th style="width: 90px">期间</th>
            <th style="width: 100px">摘要</th>
            <th style="width: 140px">借方</th>
            <th style="width: 140px">贷方</th>
            <th style="width: 50px">方向</th>
            <th style="width: 140px">余额</th>
          </tr>
        </thead>
        <tbody v-for="a in list" :key="a.account_id">
          <tr v-for="(r, i) in a.rows" :key="i">
            <template v-if="i === 0">
              <td :rowspan="a.rows.length" class="center">{{ a.code }}</td>
              <td :rowspan="a.rows.length">{{ a.name }}</td>
            </template>
            <td class="center">{{ r.period }}</td>
            <td>{{ r.summary }}</td>
            <td class="num">{{ fmt(r.debit) }}</td>
            <td class="num">{{ fmt(r.credit) }}</td>
            <td class="center">{{ r.dir }}</td>
            <td class="num">{{ fmt(r.balance, '0.00') }}</td>
          </tr>
        </tbody>
        <tbody v-if="!list.length">
          <tr><td colspan="8" class="center muted" style="padding: 40px">暂无数据</td></tr>
        </tbody>
      </table>
    </div>
  </div>
</template>

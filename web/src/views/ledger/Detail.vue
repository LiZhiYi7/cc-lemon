<script setup lang="ts">
import { onMounted, reactive, ref } from 'vue'
import { useRoute } from 'vue-router'
import { http } from '@/api/http'
import type { DetailRow } from '@/api/types'
import { useBook } from '@/stores/book'
import { fmt } from '@/utils/money'
import { periodStr } from '@/utils/period'
import PeriodRange from '@/components/PeriodRange.vue'
import AccountSelect from '@/components/AccountSelect.vue'

const book = useBook()
const route = useRoute()
const cur = periodStr(book.detail!.current_period)
const q = reactive({
  account_id: Number(route.query.account_id) || undefined,
  from: (route.query.from as string) || cur,
  to: (route.query.to as string) || cur,
})
const rows = ref<DetailRow[]>([])
const loading = ref(false)

async function load() {
  if (!q.account_id) return
  loading.value = true
  try {
    rows.value = await http.get<DetailRow[]>(`${book.base}/ledger/detail`, q)
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
      <h2 class="page-title">明细账</h2>
      <span style="width: 300px"><AccountSelect v-model="q.account_id" :leaf-only="false" placeholder="选择科目" @change="load" /></span>
      <PeriodRange v-model:from="q.from" v-model:to="q.to" />
      <el-button type="primary" :disabled="!q.account_id" @click="load">查询</el-button>
      <span class="spacer" />
      <el-button @click="print">打印</el-button>
    </div>
    <div v-loading="loading" class="sheet-wrap">
      <table class="sheet">
        <thead>
          <tr>
            <th style="width: 100px">日期</th>
            <th style="width: 80px">凭证字号</th>
            <th>摘要</th>
            <th style="width: 140px">借方</th>
            <th style="width: 140px">贷方</th>
            <th style="width: 50px">方向</th>
            <th style="width: 140px">余额</th>
          </tr>
        </thead>
        <tbody>
          <tr v-for="(r, i) in rows" :key="i" :class="{ sub: r.kind !== 'line' }">
            <td class="center">{{ r.date }}</td>
            <td class="center">
              <router-link v-if="r.voucher_id" class="link" :to="`/b/${book.detail!.id}/vouchers/${r.voucher_id}`">{{ r.voucher_no }}</router-link>
            </td>
            <td>
              {{ r.summary }}
              <span v-if="r.account" class="muted">（{{ r.account }}）</span>
              <span v-if="r.aux" class="muted">· {{ r.aux }}</span>
            </td>
            <td class="num">{{ fmt(r.debit) }}</td>
            <td class="num">{{ fmt(r.credit) }}</td>
            <td class="center">{{ r.dir }}</td>
            <td class="num">{{ fmt(r.balance, '0.00') }}</td>
          </tr>
          <tr v-if="!rows.length"><td colspan="7" class="center muted" style="padding: 40px">请选择科目后查询</td></tr>
        </tbody>
      </table>
    </div>
  </div>
</template>

<script setup lang="ts">
import { onMounted, reactive, ref } from 'vue'
import { useRoute } from 'vue-router'
import { http } from '@/api/http'
import type { DetailRow } from '@/api/types'
import ExportButton from '@/components/ExportButton.vue'
import { useBook } from '@/stores/book'
import { fmt } from '@/utils/money'
import { periodStr } from '@/utils/period'
import PeriodRange from '@/components/PeriodRange.vue'
import AccountSelect from '@/components/AccountSelect.vue'
import AuxSelect from '@/components/AuxSelect.vue'

const book = useBook()
const route = useRoute()
const cur = periodStr(book.detail!.current_period)
const q = reactive({
  type_id: Number(route.query.type_id) || (book.auxTypes[0]?.id ?? 0),
  item_id: Number(route.query.item_id) || undefined,
  account_id: Number(route.query.account_id) || undefined,
  from: (route.query.from as string) || cur,
  to: (route.query.to as string) || cur,
})
const rows = ref<DetailRow[]>([])
const loading = ref(false)

async function load() {
  if (!q.item_id) return
  loading.value = true
  try {
    rows.value = await http.get<DetailRow[]>(`${book.base}/ledger/aux-detail`, q)
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
      <h2 class="page-title">辅助核算明细账</h2>
      <el-select v-model="q.type_id" style="width: 120px" @change="q.item_id = undefined">
        <el-option v-for="t in book.auxTypes" :key="t.id" :value="t.id" :label="t.name" />
      </el-select>
      <span style="width: 200px"><AuxSelect v-model="q.item_id" :type-id="q.type_id" placeholder="选择项目" @change="load" /></span>
      <span style="width: 220px"><AccountSelect v-model="q.account_id" clearable placeholder="全部科目" @change="load" /></span>
      <PeriodRange v-model:from="q.from" v-model:to="q.to" />
      <el-button type="primary" :disabled="!q.item_id" @click="load">查询</el-button>
      <span class="spacer" />
      <ExportButton name="辅助核算明细账" :suffix="q.from === q.to ? q.from : `${q.from}至${q.to}`" />
      <el-button @click="print">打印</el-button>
    </div>
    <div v-loading="loading" class="sheet-wrap">
      <table class="sheet">
        <thead>
          <tr>
            <th style="width: 100px">日期</th>
            <th style="width: 80px">凭证字号</th>
            <th>科目</th>
            <th>摘要</th>
            <th style="width: 130px">借方</th>
            <th style="width: 130px">贷方</th>
            <th style="width: 50px">方向</th>
            <th style="width: 130px">余额</th>
          </tr>
        </thead>
        <tbody>
          <tr v-for="(r, i) in rows" :key="i" :class="{ sub: r.kind !== 'line' }">
            <td class="center">{{ r.date }}</td>
            <td class="center">
              <router-link v-if="r.voucher_id" class="link" :to="`/b/${book.detail!.id}/vouchers/${r.voucher_id}`">{{ r.voucher_no }}</router-link>
            </td>
            <td>{{ r.account }}</td>
            <td>{{ r.summary }}</td>
            <td class="num">{{ fmt(r.debit) }}</td>
            <td class="num">{{ fmt(r.credit) }}</td>
            <td class="center">{{ r.dir }}</td>
            <td class="num">{{ fmt(r.balance, '0.00') }}</td>
          </tr>
          <tr v-if="!rows.length"><td colspan="8" class="center muted" style="padding: 40px">请选择辅助核算项目后查询</td></tr>
        </tbody>
      </table>
    </div>
  </div>
</template>

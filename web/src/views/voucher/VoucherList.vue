<script setup lang="ts">
import { onMounted, reactive, ref } from 'vue'
import { useRouter } from 'vue-router'
import { ElMessage, ElMessageBox } from 'element-plus'
import { http } from '@/api/http'
import type { Voucher } from '@/api/types'
import { useBook } from '@/stores/book'
import { fmt } from '@/utils/money'
import { parsePeriod, periodStr } from '@/utils/period'
import PeriodRange from '@/components/PeriodRange.vue'

const book = useBook()
const router = useRouter()
const detail = book.detail!
const cur = periodStr(detail.current_period)
const filter = reactive({ from: cur, to: cur, word: '', status: '', keyword: '', page: 1, page_size: 50 })
const items = ref<Voucher[]>([])
const total = ref(0)
const loading = ref(false)
const selected = ref(new Set<number>())
const ledgerName = (id: number) => (book.multiLedger ? book.ledgers.find((l) => l.id === id) : undefined)

async function load() {
  loading.value = true
  try {
    const res = await http.get<{ items: Voucher[]; total: number }>(`${book.base}/vouchers`, filter)
    items.value = res.items
    total.value = res.total
    selected.value = new Set()
  } finally {
    loading.value = false
  }
}
onMounted(load)
const search = () => ((filter.page = 1), load())

function toggle(id: number, on: boolean) {
  on ? selected.value.add(id) : selected.value.delete(id)
  selected.value = new Set(selected.value)
}
function toggleAll(on: boolean) {
  selected.value = new Set(on ? items.value.map((v) => v.id) : [])
}
async function review(reviewed: boolean) {
  await http.post(`${book.base}/vouchers/review`, { ids: [...selected.value], reviewed })
  ElMessage.success(reviewed ? '已审核' : '已取消审核')
  load()
}
async function remove(v: Voucher) {
  await ElMessageBox.confirm(`确定删除凭证 ${v.word}-${v.number}？`, '删除凭证', { type: 'warning' })
  await http.del(`${book.base}/vouchers/${v.id}`)
  load()
}
async function renumber() {
  await ElMessageBox.confirm(`将 ${filter.from} 的凭证按日期重排为连续凭证号，是否继续？`, '整理凭证号', { type: 'warning' })
  await http.post(`${book.base}/vouchers/renumber`, parsePeriod(filter.from))
  ElMessage.success('凭证号已整理')
  load()
}
const open = (v: Voucher) => router.push(`/b/${detail.id}/vouchers/${v.id}`)
</script>

<template>
  <div class="page">
    <div class="toolbar">
      <h2 class="page-title">凭证列表</h2>
      <PeriodRange v-model:from="filter.from" v-model:to="filter.to" />
      <el-select v-model="filter.status" placeholder="审核状态" clearable style="width: 120px">
        <el-option value="saved" label="未审核" />
        <el-option value="reviewed" label="已审核" />
      </el-select>
      <el-input v-model="filter.keyword" placeholder="摘要关键字" clearable style="width: 160px" @keyup.enter="search" />
      <el-button type="primary" @click="search">查询</el-button>
      <span class="spacer" />
      <template v-if="book.can('voucher:review')">
        <el-button :disabled="!selected.size" @click="review(true)">审核</el-button>
        <el-button :disabled="!selected.size" @click="review(false)">取消审核</el-button>
      </template>
      <template v-if="book.can('voucher:write')">
        <el-button :disabled="filter.from !== filter.to" title="仅可对单个期间整理" @click="renumber">整理凭证号</el-button>
        <el-button type="primary" @click="router.push(`/b/${detail.id}/vouchers/new`)">新增凭证</el-button>
      </template>
    </div>

    <div v-loading="loading" class="sheet-wrap">
      <table class="sheet">
        <thead>
          <tr>
            <th style="width: 36px"><el-checkbox :model-value="!!items.length && selected.size === items.length" @change="(v: any) => toggleAll(v)" /></th>
            <th style="width: 96px">日期</th>
            <th style="width: 76px">凭证字号</th>
            <th>摘要</th>
            <th>科目</th>
            <th style="width: 130px">借方金额</th>
            <th style="width: 130px">贷方金额</th>
            <th style="width: 80px">制单人</th>
            <th style="width: 80px">审核人</th>
            <th style="width: 100px">操作</th>
          </tr>
        </thead>
        <tbody v-for="v in items" :key="v.id">
          <tr v-for="(l, i) in v.lines" :key="i">
            <template v-if="i === 0">
              <td :rowspan="v.lines.length" class="center"><el-checkbox :model-value="selected.has(v.id)" @change="(on: any) => toggle(v.id, on)" /></td>
              <td :rowspan="v.lines.length" class="center">{{ v.date }}</td>
              <td :rowspan="v.lines.length" class="center"><a class="link" @click="open(v)">{{ v.word }}-{{ v.number }}</a></td>
            </template>
            <td>{{ l.summary }}</td>
            <td>
              <el-tag v-if="ledgerName(l.ledger_id) && !ledgerName(l.ledger_id)!.is_primary" size="small" type="warning" style="margin-right: 6px">{{ ledgerName(l.ledger_id)!.name }}</el-tag>
              {{ l.account_code }} {{ l.account_name }}
              <span v-if="l.aux?.length" class="muted">· {{ l.aux.map((a) => a.item_name).join(' / ') }}</span>
            </td>
            <td class="num">{{ fmt(l.debit) }}</td>
            <td class="num">{{ fmt(l.credit) }}</td>
            <template v-if="i === 0">
              <td :rowspan="v.lines.length" class="center">{{ v.created_by_name }}</td>
              <td :rowspan="v.lines.length" class="center">{{ v.reviewed_by_name }}</td>
              <td :rowspan="v.lines.length" class="center">
                <el-button link type="primary" @click="open(v)">{{ v.status === 'reviewed' ? '查看' : '修改' }}</el-button>
                <el-button v-if="v.status === 'saved' && book.can('voucher:write')" link type="danger" @click="remove(v)">删除</el-button>
              </td>
            </template>
          </tr>
        </tbody>
        <tbody v-if="!items.length">
          <tr><td colspan="10" class="center muted" style="padding: 40px">本期间暂无凭证</td></tr>
        </tbody>
      </table>
    </div>
    <el-pagination
      v-if="total > filter.page_size"
      v-model:current-page="filter.page"
      :page-size="filter.page_size"
      :total="total"
      layout="total, prev, pager, next"
      style="margin-top: 12px; justify-content: flex-end"
      @current-change="load"
    />
  </div>
</template>

<style scoped>
tbody + tbody tr:first-child td { border-top: 2px solid var(--lemon-border); }
</style>

<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import { ApiError, http } from '@/api/http'
import type { CheckItem, ClosingStatus, Voucher } from '@/api/types'
import { useBook } from '@/stores/book'
import { fmt } from '@/utils/money'
import { periodStr } from '@/utils/period'

const book = useBook()
const detail = computed(() => book.detail!)
const period = ref(periodStr(detail.value.current_period))
const rules = ref<ClosingStatus[]>([])
const checks = ref<CheckItem[]>([])
const loading = ref(false)

const isCurrent = computed(() => period.value === periodStr(detail.value.current_period))
const closed = computed(() => period.value < periodStr(detail.value.current_period))
const ledgerName = (code: string) => book.ledgers.find((l) => l.code === code)?.name

async function load() {
  loading.value = true
  try {
    ;[rules.value, checks.value] = await Promise.all([
      http.get<ClosingStatus[]>(`${book.base}/closing/${period.value}`),
      http.get<CheckItem[]>(`${book.base}/periods/${period.value}/check`),
    ])
  } finally {
    loading.value = false
  }
}
onMounted(load)

async function generate(r: ClosingStatus) {
  const res = await http.post<{ voucher: Voucher | null }>(`${book.base}/closing/${period.value}/${r.code}`)
  if (res.voucher) ElMessage.success(`已生成凭证 ${res.voucher.word}-${res.voucher.number}`)
  else ElMessage.info('来源科目没有余额，无需结转')
  load()
}
async function close() {
  try {
    await http.post(`${book.base}/periods/${period.value}/close`)
  } catch (e) {
    if (e instanceof ApiError) ElMessage.error(e.message)
    return load()
  }
  ElMessage.success(`${period.value} 已结账`)
  await book.reloadDetail()
  period.value = periodStr(detail.value.current_period)
  load()
}
async function reopen() {
  await ElMessageBox.confirm(`反结账后 ${period.value} 的凭证可以再次修改，是否继续？`, '反结账', { type: 'warning' })
  await http.post(`${book.base}/periods/${period.value}/reopen`)
  ElMessage.success('已反结账')
  await book.reloadDetail()
  load()
}
</script>

<template>
  <div class="page" v-loading="loading">
    <div class="toolbar">
      <h2 class="page-title">结转与结账</h2>
      <el-date-picker v-model="period" type="month" value-format="YYYY-MM" format="YYYY年M月" :clearable="false" style="width: 140px" @change="load" />
      <el-tag v-if="closed" type="success">已结账</el-tag>
      <el-tag v-else-if="isCurrent" type="primary">当前期间</el-tag>
      <el-tag v-else type="info">未到期间</el-tag>
    </div>

    <h3 class="step">第一步：期末结转</h3>
    <div class="rules">
      <div v-for="r in rules" :key="r.code" class="card rule">
        <div class="rule-name">
          {{ r.name }}
          <el-tag v-if="book.multiLedger" size="small" type="info">{{ ledgerName(r.ledger) }}</el-tag>
        </div>
        <div class="muted">{{ r.frequency === 'yearly' ? '年末执行' : '每月执行' }} · 转入 {{ r.target }}</div>
        <div class="amount">
          <template v-if="!r.applicable"><span class="muted">本期无需执行</span></template>
          <template v-else-if="r.pending">待结转 <b>{{ fmt(r.amount, '0.00') }}</b></template>
          <template v-else-if="r.voucher_ids.length"><el-tag type="success">已结转</el-tag></template>
          <template v-else><span class="muted">无余额</span></template>
        </div>
        <div>
          <el-button v-if="book.can('closing:run')" type="primary" plain size="small" :disabled="!r.applicable || closed" @click="generate(r)">
            {{ r.voucher_ids.length ? '重新生成凭证' : '生成凭证' }}
          </el-button>
          <router-link v-for="id in r.voucher_ids" :key="id" class="link" style="margin-left: 10px" :to="`/b/${detail.id}/vouchers/${id}`">查看凭证</router-link>
        </div>
      </div>
    </div>

    <h3 class="step">第二步：结账检查</h3>
    <div class="card" style="max-width: 720px">
      <div v-for="c in checks" :key="c.name" class="check">
        <el-tag :type="c.ok ? 'success' : c.blocking ? 'danger' : 'warning'" size="small">{{ c.ok ? '通过' : c.blocking ? '未通过' : '提示' }}</el-tag>
        <b>{{ c.name }}</b>
        <span v-if="!c.ok" class="muted">{{ c.message }}</span>
      </div>
      <div v-if="book.can('period:close')" style="margin-top: 14px">
        <el-button v-if="!closed" type="primary" :disabled="!isCurrent" @click="close">结账</el-button>
        <el-button v-else @click="reopen">反结账</el-button>
        <span class="muted" style="margin-left: 10px">结账后本期凭证不可增删改；反结账须从最近一期开始</span>
      </div>
    </div>
  </div>
</template>

<style scoped>
.step { font-size: 15px; margin: 18px 0 10px; }
.rules { display: grid; grid-template-columns: repeat(auto-fill, minmax(260px, 1fr)); gap: 12px; }
.rule { display: flex; flex-direction: column; gap: 8px; }
.rule-name { font-weight: 600; font-size: 15px; display: flex; gap: 8px; align-items: center; }
.amount { min-height: 24px; }
.check { display: flex; gap: 10px; align-items: center; padding: 7px 0; border-bottom: 1px dashed var(--lemon-border); }
</style>

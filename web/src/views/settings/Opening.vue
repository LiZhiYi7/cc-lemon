<script setup lang="ts">
import { computed, onMounted, reactive, ref } from 'vue'
import { ElMessage } from 'element-plus'
import { http } from '@/api/http'
import type { Opening, OpeningCheck } from '@/api/types'
import { useBook } from '@/stores/book'
import { fmt, fmtCents, fromCents, toCents } from '@/utils/money'
import { periodStr } from '@/utils/period'
import LedgerSwitch from '@/components/LedgerSwitch.vue'

type Field = 'balance' | 'ytd_debit' | 'ytd_credit'
const book = useBook()
const detail = book.detail!
const ledgerId = ref(book.primaryLedger!.id)
const values = reactive(new Map<number, Record<Field, string>>())
const checks = ref<OpeningCheck[]>([])
const dirty = ref(false)
const saving = ref(false)

const showYtd = detail.start_month !== 1
const locked = computed(
  () => !book.can('opening:write') || periodStr(detail.current_period) !== periodStr({ year: detail.start_year, month: detail.start_month }),
)
const plCats = new Set(detail.categories.filter((c) => c.is_pl).map((c) => c.code))
const rows = computed(() => book.ledgerAccounts(ledgerId.value))
const fields = computed<Field[]>(() => (showYtd ? ['balance', 'ytd_debit', 'ytd_credit'] : ['balance']))

function cell(id: number) {
  let v = values.get(id)
  if (!v) {
    v = reactive({ balance: '', ytd_debit: '', ytd_credit: '' })
    values.set(id, v)
  }
  return v
}
// 上级科目显示下级合计（余额按上级方向折算）
const sums = computed(() => {
  const m = new Map<number, Record<Field, number>>()
  const list = book.accounts
  for (let i = list.length - 1; i >= 0; i--) {
    const a = list[i]
    const own = m.get(a.id) ?? { balance: 0, ytd_debit: 0, ytd_credit: 0 }
    if (a.is_leaf) {
      const v = values.get(a.id)
      own.balance = toCents(v?.balance)
      own.ytd_debit = toCents(v?.ytd_debit)
      own.ytd_credit = toCents(v?.ytd_credit)
    }
    m.set(a.id, own)
    if (a.parent_id) {
      const p = m.get(a.parent_id) ?? { balance: 0, ytd_debit: 0, ytd_credit: 0 }
      const parent = book.accountById.get(a.parent_id)
      p.balance += parent && parent.direction !== a.direction ? -own.balance : own.balance
      p.ytd_debit += own.ytd_debit
      p.ytd_credit += own.ytd_credit
      m.set(a.parent_id, p)
    }
  }
  return m
})

async function load() {
  const [list, ck] = await Promise.all([
    http.get<Opening[]>(`${book.base}/opening`),
    http.get<OpeningCheck[]>(`${book.base}/opening/check`),
  ])
  values.clear()
  for (const o of list) {
    const v = cell(o.account_id)
    for (const f of ['balance', 'ytd_debit', 'ytd_credit'] as Field[]) v[f] = toCents(o[f]) ? fromCents(toCents(o[f])) : ''
  }
  checks.value = ck
  dirty.value = false
}
onMounted(load)

function normalize(id: number, f: Field) {
  const v = cell(id)
  v[f] = toCents(v[f]) ? fromCents(toCents(v[f])) : ''
}
async function save() {
  saving.value = true
  try {
    const items = book.accounts
      .filter((a) => a.is_leaf && values.has(a.id))
      .map((a) => {
        const v = values.get(a.id)!
        return { account_id: a.id, balance: v.balance || '0', ytd_debit: v.ytd_debit || '0', ytd_credit: v.ytd_credit || '0' }
      })
    await http.put(`${book.base}/opening`, { items })
    ElMessage.success('期初数据已保存')
    await load()
  } finally {
    saving.value = false
  }
}
const check = computed(() => checks.value.find((c) => c.ledger_id === ledgerId.value))
</script>

<template>
  <div class="page">
    <div class="toolbar">
      <h2 class="page-title">期初余额</h2>
      <LedgerSwitch v-model="ledgerId" />
      <span class="muted">启用期间 {{ detail.start_year }} 年 {{ detail.start_month }} 月</span>
      <template v-if="check && !dirty">
        <el-tag v-if="check.balanced" type="success">试算平衡</el-tag>
        <el-tag v-else type="danger">试算不平衡：借方 {{ fmt(check.debit, '0.00') }} / 贷方 {{ fmt(check.credit, '0.00') }}</el-tag>
      </template>
      <span class="spacer" />
      <el-button v-if="!locked" type="primary" :loading="saving" @click="save">保存</el-button>
    </div>
    <el-alert v-if="locked && book.can('opening:write')" type="info" :closable="false" show-icon style="margin-bottom: 10px"
      title="启用期间已结账，期初数据不可修改。如需调整请先反结账。" />
    <table class="sheet">
      <thead>
        <tr>
          <th style="width: 130px">科目编码</th>
          <th>科目名称</th>
          <th style="width: 60px">方向</th>
          <th style="width: 170px">期初余额</th>
          <template v-if="showYtd">
            <th style="width: 170px">本年累计借方</th>
            <th style="width: 170px">本年累计贷方</th>
          </template>
        </tr>
      </thead>
      <tbody>
        <tr v-for="a in rows" :key="a.id" :class="{ bold: a.level === 1 }">
          <td>{{ a.code }}</td>
          <td :style="{ paddingLeft: 10 + (a.level - 1) * 20 + 'px' }">{{ a.name }}</td>
          <td class="center">{{ a.direction === 'D' ? '借' : '贷' }}</td>
          <td v-for="f in fields" :key="f" class="num" :class="{ edit: a.is_leaf && !locked }">
            <template v-if="a.is_leaf && !locked && !(f === 'balance' && plCats.has(a.category))">
              <el-input v-model="cell(a.id)[f]" class="amount" @input="dirty = true" @blur="normalize(a.id, f)" />
            </template>
            <template v-else-if="f === 'balance' && plCats.has(a.category)"><span class="muted">—</span></template>
            <template v-else>{{ fmtCents(sums.get(a.id)?.[f] ?? 0) }}</template>
          </td>
        </tr>
      </tbody>
    </table>
    <p class="muted">
      只需在末级科目录入；余额按科目方向填写，反方向余额填负数。损益（收支）类科目没有期初余额{{ showYtd ? '，年中启用时在“本年累计”中录入实际发生额' : '' }}。
    </p>
  </div>
</template>

<style scoped>
td.edit { padding: 2px; }
td.edit :deep(.el-input__wrapper) { box-shadow: none; background: #fffdf3; }
td.edit :deep(.el-input__wrapper.is-focus) { box-shadow: 0 0 0 1px var(--el-color-primary) inset; }
:deep(.amount input) { text-align: right; font-variant-numeric: tabular-nums; }
</style>

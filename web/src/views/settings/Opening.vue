<script setup lang="ts">
import { computed, onMounted, reactive, ref } from 'vue'
import { ElMessage } from 'element-plus'
import { http } from '@/api/http'
import type { Account, Opening, OpeningAuxRow, OpeningCheck } from '@/api/types'
import { useBook } from '@/stores/book'
import { fmt, fmtCents, fromCents, toCents } from '@/utils/money'
import { periodStr } from '@/utils/period'
import LedgerSwitch from '@/components/LedgerSwitch.vue'
import AuxSelect from '@/components/AuxSelect.vue'
import ImportDialog, { type ImportResult } from '@/components/ImportDialog.vue'
import { writeSheet, type SheetRow } from '@/utils/excel'

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
      .filter((a) => a.is_leaf && values.has(a.id) && !hasAux(a))
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

// ---- Excel 导出 / 导入（当前账簿）----
const importVisible = ref(false)
const ledgerName = computed(() => book.ledgers.find((l) => l.id === ledgerId.value)?.name ?? '')
/** 当前账簿中被科目启用的辅助核算类别，作为 Excel 中的列。 */
const auxColumns = computed(() => {
  const ids = new Set(rows.value.flatMap((a) => book.auxBindings[a.id] ?? []))
  return book.auxTypes.filter((t) => ids.has(t.id))
})
const amountHeaders = computed(() => (showYtd ? ['期初余额', '本年累计借方', '本年累计贷方'] : ['期初余额']))
async function exportOpening() {
  if (dirty.value) await save()
  const out: (string | number)[][] = []
  const amounts = (v: Record<Field, number | string>) => fields.value.map((f) => toCents(v[f] as string) / 100)
  for (const a of rows.value) {
    const head = [a.code, a.full_name, a.direction === 'D' ? '借' : '贷']
    const blank = auxColumns.value.map(() => '')
    if (!a.is_leaf) {
      // 上级科目：仅供对照，导入时忽略
      const s = sums.value.get(a.id)
      out.push([...head, ...blank, ...fields.value.map((f) => (s?.[f] ?? 0) / 100)])
    } else if (hasAux(a)) {
      const list = await http.get<OpeningAuxRow[]>(`${book.base}/opening/aux/${a.id}`)
      const label = (r: OpeningAuxRow, typeId: number) => {
        const x = r.aux.find((y) => y.type_id === typeId)
        return x ? `${x.item_code} ${x.item_name}` : ''
      }
      if (!list.length) out.push([...head, ...blank, ...fields.value.map(() => 0)])
      for (const r of list) out.push([...head, ...auxColumns.value.map((t) => label(r, t.id)), ...amounts(r)])
    } else {
      out.push([...head, ...blank, ...amounts(values.get(a.id) ?? { balance: '', ytd_debit: '', ytd_credit: '' })])
    }
  }
  await writeSheet(
    `${book.detail!.name}-期初余额${book.multiLedger ? '-' + ledgerName.value : ''}`,
    ['科目编码', '科目名称', '方向', ...auxColumns.value.map((t) => t.name), ...amountHeaders.value],
    out,
    [
      '只需填写末级科目；上级科目的行仅供对照，导入时忽略。',
      '余额按科目方向填写，反方向余额填负数。损益（收支）类科目没有期初余额。',
      '启用了辅助核算的科目：每个项目（组合）占一行，在对应的辅助核算列填项目编码（可带名称，如“001 甲公司”）。同一科目可以有多行。',
      '文件中出现的科目以文件为准；未出现的科目保持不变。',
      '导入时只要有一行有问题，整个文件都不会导入。',
    ].join('\n'),
  )
}
async function importOpening(sheet: SheetRow[]): Promise<ImportResult> {
  const res = await http.post<ImportResult>(`${book.base}/import/openings`, {
    ledger_id: ledgerId.value,
    rows: sheet.map((r) => ({
      row: r.row,
      code: r.values['科目编码'] ?? '',
      balance: r.values['期初余额'] ?? '',
      ytd_debit: r.values['本年累计借方'] ?? '',
      ytd_credit: r.values['本年累计贷方'] ?? '',
      aux: Object.fromEntries(book.auxTypes.filter((t) => r.values[t.name]).map((t) => [t.name, r.values[t.name]])),
    })),
  })
  if (res.applied) await load()
  return res
}

// ---- 启用辅助核算的科目：按项目录入 ----
interface AuxRow { aux: Record<number, number | undefined>; balance: string; ytd_debit: string; ytd_credit: string }
const hasAux = (a: Account) => (book.auxBindings[a.id]?.length ?? 0) > 0
const auxAccount = ref<Account | null>(null)
const auxRows = ref<AuxRow[]>([])
const auxVisible = ref(false)
const newAuxRow = (): AuxRow => ({ aux: {}, balance: '', ytd_debit: '', ytd_credit: '' })
const amount = (s: string) => (toCents(s) ? fromCents(toCents(s)) : '')

async function openAux(a: Account) {
  if (dirty.value) await save()
  const list = await http.get<OpeningAuxRow[]>(`${book.base}/opening/aux/${a.id}`)
  auxRows.value = list.map((r) => ({
    aux: Object.fromEntries(r.aux.map((x) => [x.type_id, x.item_id])),
    balance: amount(r.balance),
    ytd_debit: amount(r.ytd_debit),
    ytd_credit: amount(r.ytd_credit),
  }))
  if (!locked.value) auxRows.value.push(newAuxRow())
  auxAccount.value = a
  auxVisible.value = true
}
async function saveAux() {
  const a = auxAccount.value!
  const items = auxRows.value
    .filter((r) => toCents(r.balance) || toCents(r.ytd_debit) || toCents(r.ytd_credit))
    .map((r) => ({
      aux: book.auxTypesOf(a.id).map((t) => ({ type_id: t.id, item_id: r.aux[t.id] ?? 0 })),
      balance: r.balance || '0',
      ytd_debit: r.ytd_debit || '0',
      ytd_credit: r.ytd_credit || '0',
    }))
  await http.put(`${book.base}/opening/aux/${a.id}`, { items })
  auxVisible.value = false
  ElMessage.success('期初数据已保存')
  load()
}
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
      <el-button @click="exportOpening">导出 Excel</el-button>
      <el-button v-if="!locked" @click="importVisible = true">导入</el-button>
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
          <td v-for="f in fields" :key="f" class="num" :class="{ edit: a.is_leaf && !locked && !hasAux(a) }">
            <template v-if="hasAux(a)">
              <a class="link" title="按辅助核算项目录入" @click="openAux(a)">{{ fmtCents(sums.get(a.id)?.[f] ?? 0, '按项目录入') }}</a>
            </template>
            <template v-else-if="a.is_leaf && !locked && !(f === 'balance' && plCats.has(a.category))">
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

  <ImportDialog
    v-model="importVisible"
    :title="`导入期初余额${book.multiLedger ? '（' + ledgerName + '）' : ''}`"
    :required="['科目编码', '期初余额']"
    :tips="['只导入末级科目；文件中出现的科目以文件为准，未出现的不变', '启用辅助核算的科目每个项目占一行，在对应列填项目编码', '任何一行有问题，整个文件都不会导入']"
    :template="exportOpening"
    :submit="importOpening"
  />

  <el-dialog v-model="auxVisible" :title="`期初余额 — ${auxAccount?.code} ${auxAccount?.name}`" width="860px">
    <table v-if="auxAccount" class="sheet">
      <thead>
        <tr>
          <th v-for="t in book.auxTypesOf(auxAccount.id)" :key="t.id">{{ t.name }}</th>
          <th v-if="!plCats.has(auxAccount.category)" style="width: 150px">期初余额</th>
          <template v-if="showYtd">
            <th style="width: 150px">本年累计借方</th>
            <th style="width: 150px">本年累计贷方</th>
          </template>
          <th v-if="!locked" style="width: 44px"></th>
        </tr>
      </thead>
      <tbody>
        <tr v-for="(r, i) in auxRows" :key="i">
          <td v-for="t in book.auxTypesOf(auxAccount.id)" :key="t.id" class="edit">
            <AuxSelect v-model="r.aux[t.id]" :type-id="t.id" :disabled="locked" />
          </td>
          <td v-if="!plCats.has(auxAccount.category)" class="edit">
            <el-input v-model="r.balance" class="amount" :disabled="locked" @blur="r.balance = amount(r.balance)" />
          </td>
          <template v-if="showYtd">
            <td class="edit"><el-input v-model="r.ytd_debit" class="amount" :disabled="locked" @blur="r.ytd_debit = amount(r.ytd_debit)" /></td>
            <td class="edit"><el-input v-model="r.ytd_credit" class="amount" :disabled="locked" @blur="r.ytd_credit = amount(r.ytd_credit)" /></td>
          </template>
          <td v-if="!locked" class="center"><el-button link type="danger" @click="auxRows.splice(i, 1)">✕</el-button></td>
        </tr>
      </tbody>
    </table>
    <el-button v-if="!locked" link type="primary" style="margin-top: 8px" @click="auxRows.push(newAuxRow())">+ 增加一行</el-button>
    <template #footer>
      <el-button @click="auxVisible = false">{{ locked ? '关闭' : '取消' }}</el-button>
      <el-button v-if="!locked" type="primary" @click="saveAux">保存</el-button>
    </template>
  </el-dialog>
</template>

<style scoped>
td.edit { padding: 2px; }
td.edit :deep(.el-input__wrapper) { box-shadow: none; background: #fffdf3; }
td.edit :deep(.el-input__wrapper.is-focus) { box-shadow: 0 0 0 1px var(--el-color-primary) inset; }
:deep(.amount input) { text-align: right; font-variant-numeric: tabular-nums; }
</style>

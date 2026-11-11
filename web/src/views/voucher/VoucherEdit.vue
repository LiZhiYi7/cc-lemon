<script setup lang="ts">
// 凭证录入 / 修改。平行记账账套按核算账簿分区录入，各区借贷各自平衡。
import { computed, onBeforeUnmount, onMounted, reactive, ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { ElMessage, ElMessageBox } from 'element-plus'
import { http } from '@/api/http'
import type { Ledger, Voucher, VoucherLine } from '@/api/types'
import { useBook } from '@/stores/book'
import { fmtCents, fromCents, toCents, toChinese } from '@/utils/money'
import { defaultDate, periodStr } from '@/utils/period'
import AccountSelect from '@/components/AccountSelect.vue'
import AuxSelect from '@/components/AuxSelect.vue'

interface Row { summary: string; account_id?: number; debit: string; credit: string; aux: Record<number, number | undefined> }
interface Section { ledger: Ledger; rows: Row[] }

const MIN_ROWS = 4
const route = useRoute()
const router = useRouter()
const book = useBook()
const detail = book.detail!

const id = ref(Number(route.params.voucherId) || 0)
const loaded = ref<Voucher | null>(null)
const saving = ref(false)
const head = reactive({
  word: detail.voucher_words.find((w) => w.is_default)?.word ?? detail.voucher_words[0]?.word ?? '',
  number: 0,
  numberTouched: false,
  date: defaultDate(detail.current_period),
  attachments: 0,
})
const emptyRow = (): Row => ({ summary: '', account_id: undefined, debit: '', credit: '', aux: {} })
const amount = (s: string) => (toCents(s) ? fromCents(toCents(s)) : '')
const toRow = (l: VoucherLine): Row => ({
  summary: l.summary,
  account_id: l.account_id,
  debit: amount(l.debit),
  credit: amount(l.credit),
  aux: Object.fromEntries((l.aux ?? []).map((a) => [a.type_id, a.item_id])),
})
const sections = reactive<Section[]>(
  book.ledgers.map((l) => ({ ledger: l, rows: Array.from({ length: l.is_primary ? MIN_ROWS : 2 }, emptyRow) })),
)

const readonly = computed(() => {
  if (!book.can('voucher:write')) return true
  const v = loaded.value
  if (!v) return false
  return v.status === 'reviewed' || `${v.year}-${String(v.month).padStart(2, '0')}` < periodStr(detail.current_period)
})

function fill(v: Voucher) {
  loaded.value = v
  Object.assign(head, { word: v.word, number: v.number, numberTouched: false, date: v.date, attachments: v.attachments })
  for (const s of sections) {
    const rows = v.lines.filter((l) => l.ledger_id === s.ledger.id).map(toRow)
    while (rows.length < 2) rows.push(emptyRow())
    s.rows = rows
  }
}

async function refreshNumber() {
  if (id.value || head.numberTouched || !head.date) return
  const res = await http.get<{ number: number }>(`${book.base}/vouchers/next-number`, { date: head.date, word: head.word })
  head.number = res.number
}
watch(() => [head.date, head.word], refreshNumber)

onMounted(async () => {
  if (id.value) fill(await http.get<Voucher>(`${book.base}/vouchers/${id.value}`))
  else refreshNumber()
  window.addEventListener('keydown', hotkey)
})
onBeforeUnmount(() => window.removeEventListener('keydown', hotkey))

function hotkey(e: KeyboardEvent) {
  if ((e.ctrlKey || e.metaKey) && e.key.toLowerCase() === 's') {
    e.preventDefault()
    if (!readonly.value) save(false)
  }
}

const totals = computed(() =>
  sections.map((s) => {
    let debit = 0
    let credit = 0
    for (const r of s.rows) {
      debit += toCents(r.debit)
      credit += toCents(r.credit)
    }
    return { debit, credit }
  }),
)

// ---- 行内编辑 ----
function onAmountInput(r: Row, field: 'debit' | 'credit') {
  if (r[field] !== '') r[field === 'debit' ? 'credit' : 'debit'] = ''
}
function normalize(r: Row, field: 'debit' | 'credit') {
  const c = toCents(r[field])
  r[field] = c ? fromCents(c) : ''
}
/** 在金额格按 “=” ：自动填入使本区借贷平衡的金额。 */
function autoBalance(e: KeyboardEvent, s: Section, r: Row, field: 'debit' | 'credit') {
  if (e.key !== '=') return
  e.preventDefault()
  let diff = 0
  for (const x of s.rows) if (x !== r) diff += toCents(x.debit) - toCents(x.credit)
  const c = field === 'debit' ? -diff : diff
  r.debit = r.credit = ''
  r[field] = c ? fromCents(c) : ''
}
/** 选定科目后，启用的辅助核算类别若本凭证其他分录已选过项目，则自动带入（可再改）。 */
function onAccount(r: Row) {
  for (const t of book.auxTypesOf(r.account_id)) {
    if (r.aux[t.id]) continue
    for (const s of sections) {
      const other = s.rows.find((x) => x !== r && x.aux[t.id])
      if (other) {
        r.aux[t.id] = other.aux[t.id]
        break
      }
    }
  }
}
function focusSummary(s: Section, i: number) {
  const r = s.rows[i]
  if (!r.summary && i > 0 && !readonly.value) r.summary = s.rows[i - 1].summary
  if (i === s.rows.length - 1 && !readonly.value) s.rows.push(emptyRow())
}
function removeRow(s: Section, i: number) {
  s.rows.splice(i, 1)
  while (s.rows.length < 2) s.rows.push(emptyRow())
}

function collect() {
  const lines: Partial<VoucherLine>[] = []
  for (const s of sections)
    for (const r of s.rows) {
      if (!r.account_id && !toCents(r.debit) && !toCents(r.credit)) continue
      lines.push({
        summary: r.summary,
        account_id: r.account_id ?? 0,
        debit: r.debit || '0',
        credit: r.credit || '0',
        aux: book.auxTypesOf(r.account_id).map((t) => ({ type_id: t.id, item_id: r.aux[t.id] ?? 0 })),
      })
    }
  return lines
}

// ---- 平行记账：由主账簿分录生成其余账簿分录 ----
async function generateParallel() {
  const res = await http.post<{ lines: VoucherLine[]; warnings: string[] }>(`${book.base}/vouchers/parallel-suggest`, { lines: collect() })
  if (res.lines.length) {
    for (const s of sections) {
      const rows = res.lines.filter((l) => l.ledger_id === s.ledger.id).map(toRow)
      if (!rows.length) continue
      while (rows.length < 2) rows.push(emptyRow())
      s.rows = rows
    }
    ElMessage.success('已生成平行记账分录，请核对')
  }
  res.warnings.forEach((w) => ElMessage.warning({ message: w, duration: 6000 }))
}

async function save(andNew: boolean) {
  saving.value = true
  try {
    const body = {
      word: head.word,
      number: id.value || head.numberTouched ? head.number : 0,
      date: head.date,
      attachments: head.attachments,
      lines: collect(),
    }
    const v = id.value
      ? await http.put<Voucher>(`${book.base}/vouchers/${id.value}`, body)
      : await http.post<Voucher>(`${book.base}/vouchers`, body)
    ElMessage.success(`凭证 ${v.word}-${v.number} 已保存`)
    v.warnings?.forEach((w) => ElMessage.warning({ message: w, duration: 6000 }))
    if (andNew) {
      // 连续录入：保留日期与凭证字
      if (id.value) return router.push(`/b/${detail.id}/vouchers/new`)
      loaded.value = null
      head.numberTouched = false
      head.attachments = 0
      sections.forEach((s) => (s.rows = Array.from({ length: s.ledger.is_primary ? MIN_ROWS : 2 }, emptyRow)))
      await refreshNumber()
    } else if (!id.value) {
      router.replace(`/b/${detail.id}/vouchers/${v.id}`)
    } else fill(v)
  } finally {
    saving.value = false
  }
}

async function review(reviewed: boolean) {
  await http.post(`${book.base}/vouchers/review`, { ids: [id.value], reviewed })
  fill(await http.get<Voucher>(`${book.base}/vouchers/${id.value}`))
}
async function remove() {
  await ElMessageBox.confirm('确定删除这张凭证？', '删除凭证', { type: 'warning' })
  await http.del(`${book.base}/vouchers/${id.value}`)
  ElMessage.success('已删除')
  router.replace(`/b/${detail.id}/vouchers`)
}
const print = () => window.print()
</script>

<template>
  <div class="page">
    <div class="toolbar no-print">
      <h2 class="page-title">{{ id ? '凭证' : '新增凭证' }}</h2>
      <el-tag v-if="loaded?.status === 'reviewed'" type="success">已审核</el-tag>
      <el-tag v-if="loaded && loaded.source !== 'manual'" type="info">系统生成</el-tag>
      <span class="spacer" />
      <template v-if="!readonly">
        <el-button v-if="book.multiLedger" @click="generateParallel">生成平行记账分录</el-button>
        <el-button type="primary" :loading="saving" @click="save(false)">保存 (Ctrl+S)</el-button>
        <el-button :loading="saving" @click="save(true)">保存并新增</el-button>
      </template>
      <template v-if="id">
        <template v-if="book.can('voucher:review')">
          <el-button v-if="loaded?.status === 'saved'" @click="review(true)">审核</el-button>
          <el-button v-else @click="review(false)">取消审核</el-button>
        </template>
        <el-button v-if="!readonly" type="danger" plain @click="remove">删除</el-button>
        <el-button @click="print">打印</el-button>
      </template>
    </div>

    <div class="card voucher">
      <h3 class="title">{{ detail.voucher_words.find((w) => w.word === head.word)?.title || '记账凭证' }}</h3>
      <div class="head">
        <span>
          <el-select v-model="head.word" :disabled="readonly" style="width: 70px">
            <el-option v-for="w in detail.voucher_words" :key="w.word" :value="w.word" :label="w.word" />
          </el-select>
          <el-input-number v-model="head.number" :min="1" :controls="false" :disabled="readonly" style="width: 70px; margin: 0 6px" @change="head.numberTouched = true" />
          号
        </span>
        <span>
          日期
          <el-date-picker v-model="head.date" type="date" value-format="YYYY-MM-DD" :clearable="false" :disabled="readonly" style="width: 150px" />
        </span>
        <span class="unit muted">{{ detail.company || detail.name }}</span>
        <span>
          附单据
          <el-input-number v-model="head.attachments" :min="0" :controls="false" :disabled="readonly" style="width: 60px" />
          张
        </span>
      </div>

      <div v-for="(s, si) in sections" :key="s.ledger.id" class="section">
        <div v-if="book.multiLedger" class="section-title">{{ s.ledger.name }}</div>
        <table class="sheet entry">
          <thead>
            <tr>
              <th style="width: 36px"></th>
              <th style="width: 26%">摘要</th>
              <th>会计科目</th>
              <th style="width: 150px">借方金额</th>
              <th style="width: 150px">贷方金额</th>
              <th class="no-print" style="width: 44px"></th>
            </tr>
          </thead>
          <tbody>
            <tr v-for="(r, i) in s.rows" :key="i">
              <td class="center muted">{{ i + 1 }}</td>
              <td><el-input v-model="r.summary" :disabled="readonly" @focus="focusSummary(s, i)" /></td>
              <td>
                <AccountSelect v-model="r.account_id" :ledger-id="s.ledger.id" :disabled="readonly" placeholder="" @change="onAccount(r)" />
                <div v-if="book.auxTypesOf(r.account_id).length" class="aux">
                  <AuxSelect v-for="t in book.auxTypesOf(r.account_id)" :key="t.id" v-model="r.aux[t.id]" :type-id="t.id" :placeholder="'选择' + t.name" :disabled="readonly" size="small" />
                </div>
              </td>
              <td>
                <el-input v-model="r.debit" class="amount" :disabled="readonly" @input="onAmountInput(r, 'debit')" @blur="normalize(r, 'debit')" @keydown="(e: any) => autoBalance(e, s, r, 'debit')" />
              </td>
              <td>
                <el-input v-model="r.credit" class="amount" :disabled="readonly" @input="onAmountInput(r, 'credit')" @blur="normalize(r, 'credit')" @keydown="(e: any) => autoBalance(e, s, r, 'credit')" />
              </td>
              <td class="center no-print">
                <el-button v-if="!readonly" link type="danger" tabindex="-1" @click="removeRow(s, i)">✕</el-button>
              </td>
            </tr>
          </tbody>
          <tfoot>
            <tr>
              <td colspan="3">
                合计：{{ toChinese(totals[si].debit) }}
                <el-tag v-if="totals[si].debit !== totals[si].credit" type="danger" size="small" disable-transitions style="margin-left: 8px">
                  借贷差额 {{ fmtCents(totals[si].debit - totals[si].credit) }}
                </el-tag>
              </td>
              <td class="num">{{ fmtCents(totals[si].debit) }}</td>
              <td class="num">{{ fmtCents(totals[si].credit) }}</td>
              <td class="no-print"></td>
            </tr>
          </tfoot>
        </table>
      </div>

      <div class="foot muted">
        <span>制单人：{{ loaded?.created_by_name || '' }}</span>
        <span>审核人：{{ loaded?.reviewed_by_name || '' }}</span>
        <span class="no-print">提示：金额格按 “=” 自动找平；光标进入最后一行自动加行</span>
      </div>
    </div>
  </div>
</template>

<style scoped>
.voucher { max-width: 1100px; margin: 0 auto; }
.title { text-align: center; font-size: 20px; letter-spacing: 6px; margin: 4px 0 14px; }
.head { display: flex; align-items: center; justify-content: space-between; gap: 12px; margin-bottom: 12px; }
.unit { flex: 1; text-align: center; }
.section + .section { margin-top: 18px; }
.section-title { font-weight: 600; margin-bottom: 6px; padding-left: 8px; border-left: 3px solid var(--el-color-primary); }
.entry td { padding: 2px; }
.entry tfoot td { padding: 8px 10px; }
.aux { display: flex; gap: 4px; padding: 0 4px 4px; }
.aux :deep(.el-select__wrapper) { background: #f6f8fb !important; }
.aux :deep(.el-select__placeholder.is-transparent) { color: #e6a23c; }
.entry :deep(.el-input__wrapper), .entry :deep(.el-select__wrapper) { box-shadow: none !important; background: transparent; }
.entry :deep(.el-input__wrapper.is-focus), .entry :deep(.el-select__wrapper.is-focused) { box-shadow: 0 0 0 1px var(--el-color-primary) inset !important; }
.entry :deep(.amount input) { text-align: right; font-variant-numeric: tabular-nums; }
.foot { display: flex; gap: 40px; margin-top: 12px; font-size: 13px; }
</style>

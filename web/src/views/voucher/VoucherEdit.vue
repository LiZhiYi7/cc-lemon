<script setup lang="ts">
// 凭证录入 / 修改。
// 平行记账账套用**一张表**呈现：摘要列共享，左边财务会计三列、右边预算会计三列，
// 同一行左右对齐（与客户提供的记账凭证版式一致）。两个账簿各自借贷平衡。
import { computed, onBeforeUnmount, onMounted, reactive, ref, watch } from 'vue'
import { onBeforeRouteLeave, useRoute, useRouter } from 'vue-router'
import { ElMessage, ElMessageBox } from 'element-plus'
import { http } from '@/api/http'
import type { Ledger, Voucher, VoucherLine } from '@/api/types'
import { useBook } from '@/stores/book'
import { fmtCents, fromCents, toCents, toChinese } from '@/utils/money'
import { defaultDate, periodStr } from '@/utils/period'
import AccountSelect from '@/components/AccountSelect.vue'
import AuxSelect from '@/components/AuxSelect.vue'
import AttachBar from '@/components/AttachBar.vue'
import { useAttachQueue } from '@/stores/attachQueue'

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
const attachRef = ref<InstanceType<typeof AttachBar> | null>(null)
// 待上传的图片放在模块级队列里：保存后路由会 replace 一次，本组件会被重建。
const attachQueue = useAttachQueue()
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
// 所有账簿的分录行数必须始终一致：表格是左右对齐的一张表，行数不一致就会错位。
const sections = reactive<Section[]>(book.ledgers.map((l) => ({ ledger: l, rows: Array.from({ length: MIN_ROWS }, emptyRow) })))

const readonly = computed(() => {
  if (!book.can('voucher:write')) return true
  const v = loaded.value
  if (!v) return false
  return v.status === 'reviewed' || `${v.year}-${String(v.month).padStart(2, '0')}` < periodStr(detail.current_period)
})

// 主账簿（财务会计）与次账簿（预算会计）。单账簿账套里两者指向同一组数据，
// 模板上右侧三列由 book.multiLedger 控制不渲染。
const finSec = computed<Section>(() => sections[0])
const budSec = computed<Section>(() => sections[1] ?? sections[0])
const rowCount = computed(() => finSec.value.rows.length)
const rowIndexes = computed(() => Array.from({ length: rowCount.value }, (_, i) => i))
const totals = computed(() =>
  [finSec.value, budSec.value].map((s) => {
    let debit = 0
    let credit = 0
    for (const r of s.rows) {
      debit += toCents(r.debit)
      credit += toCents(r.credit)
    }
    return { debit, credit }
  }),
)
const totalFin = computed(() => totals.value[0])
const totalBud = computed(() => totals.value[1])

/** 把各账簿的行数对齐到最多的那一边（只增不减，用于加载与生成分录之后）。 */
function alignRows() {
  const n = Math.max(...sections.map((s) => s.rows.length))
  for (const s of sections) while (s.rows.length < n) s.rows.push(emptyRow())
}

function fill(v: Voucher) {
  loaded.value = v
  Object.assign(head, { word: v.word, number: v.number, numberTouched: false, date: v.date, attachments: v.attachments })
  for (const s of sections) {
    const rows = v.lines.filter((l) => l.ledger_id === s.ledger.id).map(toRow)
    while (rows.length < 2) rows.push(emptyRow())
    s.rows = rows
  }
  alignRows()
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

// 有图还没传上去就离开页面时提示一次：这些图只存在浏览器里，走了就没了。
onBeforeRouteLeave(async () => {
  if (!attachQueue.value.length) return true
  try {
    await ElMessageBox.confirm(`还有 ${attachQueue.value.length} 张图片没上传，确定离开？`, '图片未上传', {
      type: 'warning',
      confirmButtonText: '仍然离开',
      cancelButtonText: '留下',
    })
    return true
  } catch {
    return false
  }
})

function hotkey(e: KeyboardEvent) {
  if ((e.ctrlKey || e.metaKey) && e.key.toLowerCase() === 's') {
    e.preventDefault()
    if (!readonly.value) save(false)
  }
}

// ---- 行内编辑 ----
function onAmountInput(r: Row, field: 'debit' | 'credit') {
  if (r[field] !== '') r[field === 'debit' ? 'credit' : 'debit'] = ''
}
function normalize(r: Row, field: 'debit' | 'credit') {
  const c = toCents(r[field])
  r[field] = c ? fromCents(c) : ''
}
/** 在金额格按 “=” ：自动填入使本账簿借贷平衡的金额。 */
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

// ---- 摘要：一列共享，写一份同步到各账簿同序号行 ----
function summaryOf(i: number) {
  return finSec.value.rows[i]?.summary ?? ''
}
function setSummary(i: number, val: string) {
  for (const s of sections) if (s.rows[i]) s.rows[i].summary = val
}

/** 光标进入某行：补摘要、末行自动加行。 */
function focusRow(i: number) {
  if (readonly.value) return
  const prev = i > 0 ? finSec.value.rows[i - 1]?.summary : ''
  if (!summaryOf(i) && prev) setSummary(i, prev)
  if (i === rowCount.value - 1) addRow()
}
function addRow() {
  if (readonly.value) return
  for (const s of sections) s.rows.push(emptyRow())
}
function removeRow(i: number) {
  if (readonly.value) return
  for (const s of sections) s.rows.splice(i, 1)
  // 删完对齐两边行数，但至少保留 2 行（与手工录入的最低要求一致）。
  const n = Math.max(2, ...sections.map((s) => s.rows.length))
  for (const s of sections) while (s.rows.length < n) s.rows.push(emptyRow())
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
    alignRows()
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
    // 凭证已落库，再把暂存的图片逐张上传；单张失败不影响凭证本身。
    let failed = 0
    if (attachRef.value) failed = (await attachRef.value.flush(v.id)).failed
    ElMessage.success(`凭证 ${v.word}-${v.number} 已保存`)
    if (failed) {
      ElMessage.warning({
        message: `凭证已保存，${failed} 张图片未上传。请在附件区点「重试上传」，否则它们只在这台电脑上。`,
        duration: 10000,
      })
    }
    v.warnings?.forEach((w) => ElMessage.warning({ message: w, duration: 6000 }))
    if (andNew) {
      // 连续录入：保留日期与凭证字
      if (id.value) return router.push(`/b/${detail.id}/vouchers/new`)
      loaded.value = null
      head.numberTouched = false
      head.attachments = 0
      sections.forEach((s) => (s.rows = Array.from({ length: MIN_ROWS }, emptyRow)))
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

      <!-- 一张表：摘要 | 财务会计(科目/借/贷) | 预算会计(科目/借/贷) -->
      <table class="sheet entry">
        <colgroup>
          <col style="width: 96px" />
          <col />
          <col style="width: 104px" />
          <col style="width: 104px" />
          <template v-if="book.multiLedger">
            <col />
            <col style="width: 104px" />
            <col style="width: 104px" />
          </template>
        </colgroup>

        <thead v-if="book.multiLedger">
          <tr>
            <th rowspan="2" class="c-summary">摘要</th>
            <th colspan="3">{{ finSec.ledger.name }}</th>
            <th colspan="3">{{ budSec.ledger.name }}</th>
          </tr>
          <tr>
            <th>会计科目</th>
            <th>借方金额</th>
            <th>贷方金额</th>
            <th>会计科目</th>
            <th>借方金额</th>
            <th>贷方金额</th>
          </tr>
        </thead>
        <thead v-else>
          <tr>
            <th class="c-summary">摘要</th>
            <th>会计科目</th>
            <th>借方金额</th>
            <th>贷方金额</th>
          </tr>
        </thead>

        <tbody>
          <tr v-for="i in rowIndexes" :key="i">
            <td class="c-summary">
              <el-input
                :model-value="summaryOf(i)"
                :disabled="readonly"
                @update:model-value="(v: string) => setSummary(i, v)"
                @focus="focusRow(i)"
              />
              <button v-if="!readonly" class="row-del no-print" title="删除本行" @click="removeRow(i)">✕</button>
            </td>

            <td>
              <AccountSelect
                v-model="finSec.rows[i].account_id"
                :ledger-id="finSec.ledger.id"
                :disabled="readonly"
                placeholder=""
                @change="onAccount(finSec.rows[i])"
              />
              <div v-if="book.auxTypesOf(finSec.rows[i].account_id).length" class="aux">
                <AuxSelect
                  v-for="t in book.auxTypesOf(finSec.rows[i].account_id)"
                  :key="t.id"
                  v-model="finSec.rows[i].aux[t.id]"
                  :type-id="t.id"
                  :placeholder="'选择' + t.name"
                  :disabled="readonly"
                  size="small"
                />
              </div>
            </td>
            <td>
              <el-input
                v-model="finSec.rows[i].debit"
                class="amount"
                :disabled="readonly"
                @input="onAmountInput(finSec.rows[i], 'debit')"
                @blur="normalize(finSec.rows[i], 'debit')"
                @keydown="(e: any) => autoBalance(e, finSec, finSec.rows[i], 'debit')"
              />
            </td>
            <td>
              <el-input
                v-model="finSec.rows[i].credit"
                class="amount"
                :disabled="readonly"
                @input="onAmountInput(finSec.rows[i], 'credit')"
                @blur="normalize(finSec.rows[i], 'credit')"
                @keydown="(e: any) => autoBalance(e, finSec, finSec.rows[i], 'credit')"
              />
            </td>

            <template v-if="book.multiLedger">
              <td>
                <AccountSelect
                  v-model="budSec.rows[i].account_id"
                  :ledger-id="budSec.ledger.id"
                  :disabled="readonly"
                  placeholder=""
                  @change="onAccount(budSec.rows[i])"
                />
                <div v-if="book.auxTypesOf(budSec.rows[i].account_id).length" class="aux">
                  <AuxSelect
                    v-for="t in book.auxTypesOf(budSec.rows[i].account_id)"
                    :key="t.id"
                    v-model="budSec.rows[i].aux[t.id]"
                    :type-id="t.id"
                    :placeholder="'选择' + t.name"
                    :disabled="readonly"
                    size="small"
                  />
                </div>
              </td>
              <td>
                <el-input
                  v-model="budSec.rows[i].debit"
                  class="amount"
                  :disabled="readonly"
                  @input="onAmountInput(budSec.rows[i], 'debit')"
                  @blur="normalize(budSec.rows[i], 'debit')"
                  @keydown="(e: any) => autoBalance(e, budSec, budSec.rows[i], 'debit')"
                />
              </td>
              <td>
                <el-input
                  v-model="budSec.rows[i].credit"
                  class="amount"
                  :disabled="readonly"
                  @input="onAmountInput(budSec.rows[i], 'credit')"
                  @blur="normalize(budSec.rows[i], 'credit')"
                  @keydown="(e: any) => autoBalance(e, budSec, budSec.rows[i], 'credit')"
                />
              </td>
            </template>
          </tr>
        </tbody>

        <tfoot>
          <tr>
            <td colspan="2" class="upper">
              {{ toChinese(totalFin.debit) }}
              <el-tag v-if="totalFin.debit !== totalFin.credit" type="danger" size="small" disable-transitions style="margin-left: 8px">
                借贷差额 {{ fmtCents(totalFin.debit - totalFin.credit) }}
              </el-tag>
            </td>
            <td class="num">{{ fmtCents(totalFin.debit) }}</td>
            <td class="num">{{ fmtCents(totalFin.credit) }}</td>
            <template v-if="book.multiLedger">
              <td class="upper">
                {{ toChinese(totalBud.debit) }}
                <el-tag v-if="totalBud.debit !== totalBud.credit" type="danger" size="small" disable-transitions style="margin-left: 8px">
                  差额 {{ fmtCents(totalBud.debit - totalBud.credit) }}
                </el-tag>
              </td>
              <td class="num">{{ fmtCents(totalBud.debit) }}</td>
              <td class="num">{{ fmtCents(totalBud.credit) }}</td>
            </template>
          </tr>
        </tfoot>
      </table>

      <AttachBar ref="attachRef" :base="book.base" :voucher-id="id" :readonly="readonly" :max="20" />

      <div class="foot muted">
        <span>制单人：{{ loaded?.created_by_name || '' }}</span>
        <span>审核人：{{ loaded?.reviewed_by_name || '' }}</span>
        <span class="no-print">提示：金额格按 “=” 自动找平；光标进入最后一行自动加行；行悬停可删除</span>
      </div>
    </div>
  </div>
</template>

<style scoped>
/* 单表版式：财务会计与预算会计共用一行，摘要列共享。
   每列宽度可在 96 / auto / 104 三档间伸缩，窄屏时整表横向滚动而不是挤压科目列。 */
.voucher { max-width: 1680px; min-width: 960px; margin: 0 auto; }
.title { text-align: center; font-size: 20px; letter-spacing: 6px; margin: 4px 0 14px; }
.head { display: flex; align-items: center; justify-content: space-between; gap: 12px; margin-bottom: 12px; }
.unit { flex: 1; text-align: center; }

.entry { table-layout: fixed; }
.entry th, .entry td { box-sizing: border-box; }
.entry th { padding: 7px 6px; font-weight: 600; }
.entry td { padding: 2px; position: relative; }
.entry tfoot td { padding: 8px 10px; }
.entry .c-summary { background: #fafbfc; }
.entry tfoot .upper { font-weight: 600; }
.entry tfoot .num { font-weight: 600; }

.entry :deep(.el-input), .entry :deep(.el-select) { width: 100%; min-width: 0; }
.entry :deep(.el-input__wrapper), .entry :deep(.el-select__wrapper) { box-shadow: none !important; background: transparent; }
.entry :deep(.el-input__wrapper.is-focus), .entry :deep(.el-select__wrapper.is-focused) {
  box-shadow: 0 0 0 1px var(--el-color-primary) inset !important;
}
.entry :deep(.amount input) { text-align: right; font-variant-numeric: tabular-nums; }

.aux { display: flex; gap: 4px; padding: 0 4px 4px; }
.aux :deep(.el-select) { min-width: 0; }
.aux :deep(.el-select__wrapper) { background: #f6f8fb !important; }
.aux :deep(.el-select__placeholder.is-transparent) { color: #e6a23c; }

/* 删除按钮：不占列宽，鼠标移到行上才出现 */
.row-del {
  position: absolute;
  top: 2px;
  right: 2px;
  width: 16px;
  height: 16px;
  padding: 0;
  line-height: 14px;
  font-size: 11px;
  color: #fff;
  background: var(--el-color-danger);
  border: none;
  border-radius: 3px;
  cursor: pointer;
  opacity: 0;
  transition: opacity 0.12s;
}
.entry tbody tr:hover .row-del { opacity: 1; }

.foot { display: flex; gap: 40px; margin-top: 12px; font-size: 13px; }

/* 打印：7 列在 A4 纵向放不下，固定横向；两本账簿各占半张纸。 */
@media print {
  @page {
    size: A4 landscape;
    margin: 8mm;
  }
  .voucher {
    max-width: none;
    min-width: 0;
  }
  .entry th, .entry td {
    padding: 2px 3px;
    font-size: 11px;
  }
  .entry tr {
    break-inside: avoid;
  }
  tfoot {
    break-inside: avoid;
  }
}
</style>

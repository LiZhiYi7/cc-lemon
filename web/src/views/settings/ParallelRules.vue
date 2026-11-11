<script setup lang="ts">
import { onMounted, reactive, ref } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import { http } from '@/api/http'
import type { ParallelRule } from '@/api/types'
import { useBook } from '@/stores/book'
import AccountSelect from '@/components/AccountSelect.vue'

const book = useBook()
const rules = ref<ParallelRule[]>([])
const canWrite = book.can('parallel:write')
const visible = ref(false)
const primary = book.primaryLedger!
const others = book.ledgers.filter((l) => l.id !== primary.id)
const form = reactive({
  id: 0,
  source_ledger_id: primary.id,
  target_ledger_id: others[0]?.id ?? 0,
  source_id: undefined as number | undefined,
  target_id: undefined as number | undefined,
  side: '' as '' | 'D' | 'C',
  note: '',
})

const load = async () => (rules.value = await http.get<ParallelRule[]>(`${book.base}/parallel-rules`))
onMounted(load)

const find = (ledgerId: number, code: string) => book.accounts.find((a) => a.ledger_id === ledgerId && a.code === code)
const label = (ledgerId: number, code: string) => `${code} ${find(ledgerId, code)?.full_name ?? '（科目不存在）'}`
const ledgerName = (id: number) => book.ledgers.find((l) => l.id === id)?.name

function open(r?: ParallelRule) {
  Object.assign(form, {
    id: r?.id ?? 0,
    source_ledger_id: r?.source_ledger_id ?? primary.id,
    target_ledger_id: r?.target_ledger_id ?? others[0]?.id ?? 0,
    source_id: r ? find(r.source_ledger_id, r.source_code)?.id : undefined,
    target_id: r ? find(r.target_ledger_id, r.target_code)?.id : undefined,
    side: r?.side ?? '',
    note: r?.note ?? '',
  })
  visible.value = true
}
async function save() {
  await http.post(`${book.base}/parallel-rules`, {
    id: form.id,
    source_ledger_id: form.source_ledger_id,
    target_ledger_id: form.target_ledger_id,
    source_code: book.accountById.get(form.source_id!)?.code,
    target_code: book.accountById.get(form.target_id!)?.code,
    side: form.side,
    note: form.note,
  })
  visible.value = false
  ElMessage.success('已保存')
  load()
}
async function remove(r: ParallelRule) {
  await ElMessageBox.confirm('确定删除这条规则？', '删除规则', { type: 'warning' })
  await http.del(`${book.base}/parallel-rules/${r.id}`)
  load()
}
</script>

<template>
  <div class="page">
    <div class="toolbar">
      <h2 class="page-title">平行记账规则</h2>
      <span class="spacer" />
      <el-button v-if="canWrite" type="primary" @click="open()">新增规则</el-button>
    </div>
    <el-alert type="info" :closable="false" show-icon style="margin-bottom: 12px">
      <template #title>
        录入凭证时点击“生成平行记账分录”：若{{ primary.name }}分录涉及现金类科目，每条分录按下表映射为同方向、同金额的{{ others.map((l) => l.name).join('、') }}分录。
        规则对下级科目同样生效，下级科目可另设规则覆盖；限定方向的规则优先。
      </template>
    </el-alert>
    <table class="sheet">
      <thead>
        <tr>
          <th>来源科目（{{ primary.name }}）</th>
          <th style="width: 110px">适用方向</th>
          <th>目标科目</th>
          <th>说明</th>
          <th v-if="canWrite" style="width: 120px">操作</th>
        </tr>
      </thead>
      <tbody>
        <tr v-for="r in rules" :key="r.id">
          <td>{{ label(r.source_ledger_id, r.source_code) }}</td>
          <td class="center">{{ r.side === 'D' ? '仅借方' : r.side === 'C' ? '仅贷方' : '借贷均可' }}</td>
          <td>
            <el-tag v-if="others.length > 1" size="small" type="info">{{ ledgerName(r.target_ledger_id) }}</el-tag>
            {{ label(r.target_ledger_id, r.target_code) }}
          </td>
          <td class="muted">{{ r.note }}</td>
          <td v-if="canWrite" class="center">
            <el-button link type="primary" @click="open(r)">编辑</el-button>
            <el-button link type="danger" @click="remove(r)">删除</el-button>
          </td>
        </tr>
      </tbody>
    </table>
  </div>

  <el-dialog v-model="visible" :title="form.id ? '编辑规则' : '新增规则'" width="520px">
    <el-form label-width="90px">
      <el-form-item label="来源科目" required>
        <AccountSelect v-model="form.source_id" :ledger-id="form.source_ledger_id" :leaf-only="false" />
      </el-form-item>
      <el-form-item label="适用方向">
        <el-radio-group v-model="form.side">
          <el-radio value="">借贷均可</el-radio>
          <el-radio value="D">仅借方发生</el-radio>
          <el-radio value="C">仅贷方发生</el-radio>
        </el-radio-group>
      </el-form-item>
      <el-form-item v-if="others.length > 1" label="目标账簿">
        <el-select v-model="form.target_ledger_id" @change="form.target_id = undefined">
          <el-option v-for="l in others" :key="l.id" :value="l.id" :label="l.name" />
        </el-select>
      </el-form-item>
      <el-form-item label="目标科目" required>
        <AccountSelect v-model="form.target_id" :ledger-id="form.target_ledger_id" :leaf-only="false" />
      </el-form-item>
      <el-form-item label="说明"><el-input v-model="form.note" /></el-form-item>
    </el-form>
    <template #footer>
      <el-button @click="visible = false">取消</el-button>
      <el-button type="primary" :disabled="!form.source_id || !form.target_id" @click="save">保存</el-button>
    </template>
  </el-dialog>
</template>

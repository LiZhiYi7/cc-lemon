<script setup lang="ts">
import { computed, reactive, ref } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import { http } from '@/api/http'
import type { Account } from '@/api/types'
import { useBook } from '@/stores/book'
import LedgerSwitch from '@/components/LedgerSwitch.vue'

const book = useBook()
const ledgerId = ref(book.primaryLedger!.id)
const categories = computed(() => book.detail!.categories.filter((c) => c.ledger_id === ledgerId.value))
const category = ref(categories.value[0]?.code ?? '')
const canWrite = computed(() => book.can('account:write'))
const scheme = computed(() => book.detail!.settings.account_code_scheme)

const rows = computed(() => book.accounts.filter((a) => a.ledger_id === ledgerId.value && a.category === category.value))
function onLedger() {
  category.value = categories.value[0]?.code ?? ''
}

const visible = ref(false)
const editing = ref<Account | null>(null)
const parent = ref<Account | null>(null)
const form = reactive({ code: '', name: '', direction: 'D' as 'D' | 'C', is_cash: false, aux: [] as number[] })
const auxNames = (a: Account) => book.auxTypesOf(a.id).map((t) => t.name).join('、')

/** 建议编码：同级最大编码 + 1。 */
function suggestCode(p: Account | null): string {
  const level = p ? p.level + 1 : 1
  const seg = scheme.value[level - 1]
  if (!seg) return ''
  const siblings = book.accounts.filter((a) => a.ledger_id === ledgerId.value && (p ? a.parent_id === p.id : a.level === 1 && a.category === category.value))
  const prefix = p?.code ?? ''
  const max = siblings.reduce((m, a) => Math.max(m, Number(a.code.slice(prefix.length)) || 0), 0)
  if (!p && !max) return ''
  return prefix + String(max + 1).padStart(seg, '0')
}
function openCreate(p: Account | null) {
  editing.value = null
  parent.value = p
  Object.assign(form, { code: suggestCode(p), name: '', direction: p?.direction ?? 'D', is_cash: p?.is_cash ?? false, aux: [] })
  visible.value = true
}
function openEdit(a: Account) {
  editing.value = a
  parent.value = null
  Object.assign(form, { code: a.code, name: a.name, direction: a.direction, is_cash: a.is_cash, aux: [...(book.auxBindings[a.id] ?? [])] })
  visible.value = true
}
async function save() {
  let target = editing.value
  try {
    if (target) {
      await http.patch(`${book.base}/accounts/${target.id}`, { name: form.name, direction: form.direction, is_cash: form.is_cash })
    } else {
      target = await http.post<Account>(`${book.base}/accounts`, {
        code: form.code,
        name: form.name,
        direction: form.direction,
        is_cash: form.is_cash,
        parent_id: parent.value?.id ?? null,
        ledger_id: ledgerId.value,
        category: category.value,
      })
    }
    // 辅助核算绑定单独保存；未变化时后端直接忽略
    if (target.is_leaf) await http.put(`${book.base}/accounts/${target.id}/aux`, { type_ids: form.aux })
    visible.value = false
    ElMessage.success('已保存')
  } finally {
    await Promise.all([book.reloadAccounts(), book.reloadAux()])
  }
}
async function toggle(a: Account) {
  await http.patch(`${book.base}/accounts/${a.id}`, { enabled: !a.enabled })
  book.reloadAccounts()
}
async function remove(a: Account) {
  await ElMessageBox.confirm(`确定删除科目 ${a.code} ${a.name}？`, '删除科目', { type: 'warning' })
  await http.del(`${book.base}/accounts/${a.id}`)
  book.reloadAccounts()
}
</script>

<template>
  <div class="page">
    <div class="toolbar">
      <h2 class="page-title">会计科目</h2>
      <LedgerSwitch v-model="ledgerId" @change="onLedger" />
      <span class="muted">编码方案 {{ scheme.join('-') }}</span>
      <span class="spacer" />
      <el-button v-if="canWrite" type="primary" @click="openCreate(null)">新增一级科目</el-button>
    </div>
    <el-tabs v-model="category" type="card">
      <el-tab-pane v-for="c in categories" :key="c.code" :name="c.code" :label="c.name" />
    </el-tabs>
    <table class="sheet">
      <thead>
        <tr>
          <th style="width: 140px">科目编码</th>
          <th>科目名称</th>
          <th style="width: 80px">余额方向</th>
          <th style="width: 80px">现金类</th>
          <th style="width: 160px">辅助核算</th>
          <th style="width: 80px">状态</th>
          <th v-if="canWrite" style="width: 220px">操作</th>
        </tr>
      </thead>
      <tbody>
        <tr v-for="a in rows" :key="a.id" :class="{ bold: a.level === 1 }">
          <td>{{ a.code }}</td>
          <td :style="{ paddingLeft: 10 + (a.level - 1) * 20 + 'px' }">{{ a.name }}</td>
          <td class="center">{{ a.direction === 'D' ? '借' : '贷' }}</td>
          <td class="center">{{ a.is_cash ? '是' : '' }}</td>
          <td>{{ auxNames(a) }}</td>
          <td class="center"><el-tag :type="a.enabled ? 'success' : 'info'" size="small">{{ a.enabled ? '启用' : '停用' }}</el-tag></td>
          <td v-if="canWrite" class="center">
            <el-button v-if="a.level < scheme.length" link type="primary" @click="openCreate(a)">新增下级</el-button>
            <el-button link type="primary" @click="openEdit(a)">编辑</el-button>
            <el-button link @click="toggle(a)">{{ a.enabled ? '停用' : '启用' }}</el-button>
            <el-button v-if="a.is_leaf" link type="danger" @click="remove(a)">删除</el-button>
          </td>
        </tr>
      </tbody>
    </table>
  </div>

  <el-dialog v-model="visible" :title="editing ? '编辑科目' : parent ? `新增下级科目（上级：${parent.code} ${parent.name}）` : '新增一级科目'" width="520px">
    <el-alert v-if="parent?.is_leaf" type="info" :closable="false" show-icon style="margin-bottom: 12px"
      title="上级科目若已有期初或凭证数据，将自动转移到这个新增的下级科目。" />
    <el-form label-width="90px">
      <el-form-item label="科目编码" required><el-input v-model="form.code" :disabled="!!editing" /></el-form-item>
      <el-form-item label="科目名称" required><el-input v-model="form.name" /></el-form-item>
      <el-form-item label="余额方向">
        <el-radio-group v-model="form.direction">
          <el-radio value="D">借</el-radio>
          <el-radio value="C">贷</el-radio>
        </el-radio-group>
      </el-form-item>
      <el-form-item label="现金类科目">
        <el-switch v-model="form.is_cash" />
        <span class="muted" style="margin-left: 8px">库存现金、银行存款等{{ book.multiLedger ? '；平行记账以此判断是否需要预算会计分录' : '' }}</span>
      </el-form-item>
      <el-form-item v-if="book.auxTypes.length && (!editing || editing.is_leaf)" label="辅助核算">
        <el-checkbox-group v-model="form.aux">
          <el-checkbox v-for="t in book.auxTypes" :key="t.id" :value="t.id">{{ t.name }}</el-checkbox>
        </el-checkbox-group>
        <div class="muted">启用后该科目每笔分录都要选择对应项目；科目已有数据后不能再调整，也不能增加下级科目</div>
      </el-form-item>
    </el-form>
    <template #footer>
      <el-button @click="visible = false">取消</el-button>
      <el-button type="primary" :disabled="!form.code || !form.name" @click="save">保存</el-button>
    </template>
  </el-dialog>
</template>

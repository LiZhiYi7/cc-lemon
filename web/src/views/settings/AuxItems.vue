<script setup lang="ts">
import { computed, reactive, ref } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import { http } from '@/api/http'
import type { AuxItem, AuxType } from '@/api/types'
import { useBook } from '@/stores/book'
import ImportDialog, { type ImportResult } from '@/components/ImportDialog.vue'
import { writeSheet, type SheetRow } from '@/utils/excel'

const book = useBook()
const canWrite = computed(() => book.can('aux:write'))
const typeId = ref(book.auxTypes[0]?.id ?? 0)
const type = computed(() => book.auxTypes.find((t) => t.id === typeId.value))
const items = computed(() => book.auxItemsOf(typeId.value))
const usedBy = computed(() =>
  book.accounts.filter((a) => book.auxBindings[a.id]?.includes(typeId.value)).map((a) => `${a.code} ${a.name}`),
)

const importVisible = ref(false)
async function exportItems() {
  await writeSheet(
    `${book.detail!.name}-${type.value!.name}`,
    ['编码', '名称', '备注'],
    items.value.map((i) => [i.code, i.name, i.remark]),
    '编码、名称必填。编码已存在的项目会更新名称和备注，不存在的会新增。\n导入时只要有一行有问题，整个文件都不会导入。',
  )
}
async function importItems(rows: SheetRow[]): Promise<ImportResult> {
  const res = await http.post<ImportResult>(`${book.base}/import/aux-items`, {
    type_id: typeId.value,
    rows: rows.map((r) => ({ row: r.row, code: r.values['编码'] ?? '', name: r.values['名称'] ?? '', remark: r.values['备注'] ?? '' })),
  })
  if (res.applied) await book.reloadAux()
  return res
}

const visible = ref(false)
const form = reactive({ id: 0, code: '', name: '', remark: '' })

function open(it?: AuxItem) {
  // 新增时建议编码：现有最大数字编码 + 1，保持位数
  let code = ''
  if (!it) {
    const nums = items.value.map((i) => i.code).filter((c) => /^\d+$/.test(c))
    const width = nums.reduce((w, c) => Math.max(w, c.length), 3)
    code = String(nums.reduce((m, c) => Math.max(m, Number(c)), 0) + 1).padStart(width, '0')
  }
  Object.assign(form, { id: it?.id ?? 0, code: it?.code ?? code, name: it?.name ?? '', remark: it?.remark ?? '' })
  visible.value = true
}
async function save(again: boolean) {
  const body = { type_id: typeId.value, code: form.code, name: form.name, remark: form.remark }
  if (form.id) await http.patch(`${book.base}/aux/items/${form.id}`, body)
  else await http.post(`${book.base}/aux/items`, body)
  await book.reloadAux()
  ElMessage.success('已保存')
  if (again) open()
  else visible.value = false
}
async function toggle(it: AuxItem) {
  await http.patch(`${book.base}/aux/items/${it.id}`, { enabled: !it.enabled })
  book.reloadAux()
}
async function remove(it: AuxItem) {
  await ElMessageBox.confirm(`确定删除 ${it.code} ${it.name}？`, '删除项目', { type: 'warning' })
  await http.del(`${book.base}/aux/items/${it.id}`)
  book.reloadAux()
}
async function addType() {
  const { value } = await ElMessageBox.prompt('类别名称（如：合同、车辆、资金来源）', '新增辅助核算类别', { inputPattern: /\S/, inputErrorMessage: '请输入名称' })
  const t = await http.post<AuxType>(`${book.base}/aux/types`, { name: value })
  await book.reloadAux()
  typeId.value = t.id
}
async function renameType() {
  const { value } = await ElMessageBox.prompt('类别名称', '重命名', { inputValue: type.value!.name, inputPattern: /\S/, inputErrorMessage: '请输入名称' })
  await http.patch(`${book.base}/aux/types/${typeId.value}`, { name: value })
  book.reloadAux()
}
async function removeType() {
  await ElMessageBox.confirm(`确定删除类别“${type.value!.name}”？`, '删除类别', { type: 'warning' })
  await http.del(`${book.base}/aux/types/${typeId.value}`)
  await book.reloadAux()
  typeId.value = book.auxTypes[0]?.id ?? 0
}
</script>

<template>
  <div class="page">
    <div class="toolbar">
      <h2 class="page-title">辅助核算</h2>
      <span class="spacer" />
      <template v-if="canWrite">
        <el-button @click="addType">新增类别</el-button>
        <el-button v-if="type" @click="importVisible = true">导入</el-button>
        <el-button v-if="type" type="primary" @click="open()">新增{{ type.name }}</el-button>
      </template>
      <el-button v-if="type" @click="exportItems">导出 Excel</el-button>
    </div>
    <el-tabs v-model="typeId" type="card">
      <el-tab-pane v-for="t in book.auxTypes" :key="t.id" :name="t.id" :label="t.name" />
    </el-tabs>
    <template v-if="type">
      <div class="toolbar">
        <span class="muted">
          {{ usedBy.length ? `已在科目启用：${usedBy.join('、')}` : '尚未在任何科目启用。请到“会计科目”中编辑科目，勾选要启用的辅助核算类别。' }}
        </span>
        <span class="spacer" />
        <template v-if="canWrite">
          <el-button link type="primary" @click="renameType">重命名类别</el-button>
          <el-button v-if="!type.is_system" link type="danger" @click="removeType">删除类别</el-button>
        </template>
      </div>
      <table class="sheet">
        <thead>
          <tr>
            <th style="width: 140px">编码</th>
            <th>名称</th>
            <th>备注</th>
            <th style="width: 80px">状态</th>
            <th v-if="canWrite" style="width: 170px">操作</th>
          </tr>
        </thead>
        <tbody>
          <tr v-for="it in items" :key="it.id">
            <td>{{ it.code }}</td>
            <td>{{ it.name }}</td>
            <td class="muted">{{ it.remark }}</td>
            <td class="center"><el-tag :type="it.enabled ? 'success' : 'info'" size="small">{{ it.enabled ? '启用' : '停用' }}</el-tag></td>
            <td v-if="canWrite" class="center">
              <el-button link type="primary" @click="open(it)">编辑</el-button>
              <el-button link @click="toggle(it)">{{ it.enabled ? '停用' : '启用' }}</el-button>
              <el-button link type="danger" @click="remove(it)">删除</el-button>
            </td>
          </tr>
          <tr v-if="!items.length"><td colspan="5" class="center muted" style="padding: 40px">暂无{{ type.name }}</td></tr>
        </tbody>
      </table>
    </template>
  </div>

  <ImportDialog
    v-if="type"
    v-model="importVisible"
    :title="`导入${type.name}`"
    :required="['编码', '名称']"
    :tips="['编码不存在的会新增；已存在的更新名称和备注', '任何一行有问题，整个文件都不会导入']"
    :template="exportItems"
    :submit="importItems"
  />

  <el-dialog v-model="visible" :title="(form.id ? '编辑' : '新增') + (type?.name ?? '')" width="440px">
    <el-form label-width="70px" @submit.prevent>
      <el-form-item label="编码" required><el-input v-model="form.code" /></el-form-item>
      <el-form-item label="名称" required><el-input v-model="form.name" @keyup.enter="form.code && form.name && save(!form.id)" /></el-form-item>
      <el-form-item label="备注"><el-input v-model="form.remark" /></el-form-item>
    </el-form>
    <template #footer>
      <el-button @click="visible = false">取消</el-button>
      <el-button v-if="!form.id" :disabled="!form.code || !form.name" @click="save(true)">保存并继续新增</el-button>
      <el-button type="primary" :disabled="!form.code || !form.name" @click="save(false)">保存</el-button>
    </template>
  </el-dialog>
</template>

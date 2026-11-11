<script setup lang="ts">
// 通用 Excel 导入对话框：下载模板 → 选择文件 → 校验表头 → 提交 → 显示逐行错误。
import { ref, watch } from 'vue'
import { ElMessage } from 'element-plus'
import { readSheet, type SheetRow } from '@/utils/excel'

export interface ImportResult {
  applied: boolean
  created: number
  updated: number
  unchanged: number
  errors: { row: number; message: string }[]
}

const props = defineProps<{
  title: string
  required: string[] // 文件必须包含的列名
  tips?: string[]
  template: () => Promise<void> // 下载模板
  submit: (rows: SheetRow[]) => Promise<ImportResult>
}>()
const visible = defineModel<boolean>({ required: true })
const emit = defineEmits<{ done: [] }>()

const fileName = ref('')
const rows = ref<SheetRow[]>([])
const problem = ref('')
const result = ref<ImportResult | null>(null)
const busy = ref(false)
const input = ref<HTMLInputElement>()

watch(visible, (v) => {
  if (v) {
    fileName.value = problem.value = ''
    rows.value = []
    result.value = null
  }
})

async function pick(e: Event) {
  const file = (e.target as HTMLInputElement).files?.[0]
  ;(e.target as HTMLInputElement).value = ''
  if (!file) return
  result.value = null
  rows.value = []
  fileName.value = file.name
  try {
    const sheet = await readSheet(file)
    const missing = props.required.filter((h) => !sheet.headers.includes(h))
    if (missing.length) problem.value = `文件缺少列：${missing.join('、')}。请使用下载的模板填写。`
    else if (!sheet.rows.length) problem.value = '文件中没有数据行'
    else {
      problem.value = ''
      rows.value = sheet.rows
    }
  } catch {
    problem.value = '无法读取文件，请确认是 .xlsx 格式'
  }
}

async function run() {
  busy.value = true
  try {
    const res = await props.submit(rows.value)
    result.value = res
    if (res.applied) {
      ElMessage.success('导入成功')
      emit('done')
    }
  } finally {
    busy.value = false
  }
}
</script>

<template>
  <el-dialog v-model="visible" :title="title" width="620px">
    <ol class="steps">
      <li>
        <el-button link type="primary" @click="template()">下载模板</el-button>
        <span class="muted">模板里已包含当前数据，可直接在上面修改或追加</span>
      </li>
      <li>
        <el-button @click="input?.click()">选择 Excel 文件</el-button>
        <input ref="input" type="file" accept=".xlsx" hidden @change="pick" />
        <span v-if="fileName" class="file">{{ fileName }}<template v-if="rows.length">，共 {{ rows.length }} 行数据</template></span>
      </li>
    </ol>
    <ul v-if="tips?.length" class="tips muted">
      <li v-for="t in tips" :key="t">{{ t }}</li>
    </ul>
    <el-alert v-if="problem" type="error" :closable="false" show-icon :title="problem" />
    <template v-if="result">
      <el-alert v-if="result.applied" type="success" :closable="false" show-icon
        :title="`导入完成：新增 ${result.created}，更新 ${result.updated}，未变化 ${result.unchanged}`" />
      <template v-else>
        <el-alert type="error" :closable="false" show-icon :title="`有 ${result.errors.length} 处问题，本次未导入任何数据。请修改后重新选择文件。`" />
        <div class="errors">
          <table class="sheet">
            <thead><tr><th style="width: 80px">Excel 行号</th><th>问题</th></tr></thead>
            <tbody>
              <tr v-for="(e, i) in result.errors" :key="i">
                <td class="center">{{ e.row || '—' }}</td>
                <td>{{ e.message }}</td>
              </tr>
            </tbody>
          </table>
        </div>
      </template>
    </template>
    <template #footer>
      <el-button @click="visible = false">{{ result?.applied ? '关闭' : '取消' }}</el-button>
      <el-button v-if="!result?.applied" type="primary" :disabled="!rows.length" :loading="busy" @click="run">开始导入</el-button>
    </template>
  </el-dialog>
</template>

<style scoped>
.steps { margin: 0 0 12px; padding-left: 20px; line-height: 40px; }
.steps .muted, .file { margin-left: 10px; }
.tips { margin: 0 0 12px; padding-left: 20px; font-size: 13px; line-height: 22px; }
.errors { max-height: 260px; overflow: auto; margin-top: 10px; }
</style>

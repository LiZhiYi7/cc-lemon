<script setup lang="ts">
// 导出当前页面的账表到 Excel。自动取所在页面（.page）中的第一张 table.sheet。
import { ref } from 'vue'
import { ElMessage } from 'element-plus'
import { useBook } from '@/stores/book'
import { exportTable } from '@/utils/excel'

const props = defineProps<{ name: string; suffix?: string }>()
const book = useBook()
const root = ref<HTMLElement>()
const loading = ref(false)

async function run() {
  const table = root.value?.closest('.page')?.querySelector<HTMLTableElement>('table.sheet')
  if (!table) return ElMessage.warning('当前页面没有可导出的表格')
  loading.value = true
  try {
    const unit = book.detail?.company || book.detail?.name || ''
    const period = props.suffix ? `（${props.suffix}）` : ''
    await exportTable(table, [book.detail?.name, props.name, props.suffix].filter(Boolean).join('-'), props.name, `${unit}${period}`)
  } finally {
    loading.value = false
  }
}
</script>

<template>
  <span ref="root"><el-button :loading="loading" @click="run">导出 Excel</el-button></span>
</template>

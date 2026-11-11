<script setup lang="ts">
// 科目选择器：输入编码或名称过滤，回车选中第一项。leafOnly 时只列出可记账的末级、启用科目。
import { computed } from 'vue'
import { useBook } from '@/stores/book'

const props = withDefaults(defineProps<{ ledgerId?: number; leafOnly?: boolean; placeholder?: string; clearable?: boolean }>(), {
  leafOnly: true,
  placeholder: '编码或名称',
  clearable: false,
})
const model = defineModel<number | undefined>()
const book = useBook()

const options = computed(() =>
  book.accounts.filter(
    (a) =>
      (!props.ledgerId || a.ledger_id === props.ledgerId) &&
      (!props.leafOnly || (a.is_leaf && (a.enabled || a.id === model.value))),
  ),
)
</script>

<template>
  <el-select
    v-model="model"
    filterable
    default-first-option
    :placeholder="placeholder"
    :clearable="clearable"
    style="width: 100%"
  >
    <el-option v-for="a in options" :key="a.id" :value="a.id" :label="`${a.code} ${a.full_name}`" />
  </el-select>
</template>

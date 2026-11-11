<script setup lang="ts">
// 辅助核算项目选择器：某一类别下的项目，输入编码或名称过滤，回车选中第一项。
import { computed } from 'vue'
import { useBook } from '@/stores/book'

const props = defineProps<{ typeId: number; placeholder?: string; clearable?: boolean }>()
const model = defineModel<number | undefined>()
const book = useBook()
const options = computed(() => book.auxItemsOf(props.typeId).filter((i) => i.enabled || i.id === model.value))
</script>

<template>
  <el-select v-model="model" filterable default-first-option :placeholder="placeholder" :clearable="clearable" style="width: 100%">
    <el-option v-for="i in options" :key="i.id" :value="i.id" :label="`${i.code} ${i.name}`" />
  </el-select>
</template>

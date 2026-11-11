<script setup lang="ts">
// 核算账簿切换：单账簿账套不显示。
import { useBook } from '@/stores/book'
const model = defineModel<number>({ required: true })
const book = useBook()
if (!model.value && book.primaryLedger) model.value = book.primaryLedger.id
</script>

<template>
  <el-radio-group v-if="book.multiLedger" v-model="model">
    <el-radio-button v-for="l in book.ledgers" :key="l.id" :value="l.id">{{ l.name }}</el-radio-button>
  </el-radio-group>
</template>

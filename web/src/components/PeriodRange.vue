<script setup lang="ts">
// 会计期间区间选择（YYYY-MM ~ YYYY-MM）。
const from = defineModel<string>('from', { required: true })
const to = defineModel<string>('to', { required: true })
function fix() {
  if (to.value < from.value) to.value = from.value
}
</script>

<template>
  <span class="period-range">
    <el-date-picker v-model="from" type="month" value-format="YYYY-MM" format="YYYY年M月" :clearable="false" style="width: 130px" @change="fix" />
    <span class="muted">至</span>
    <el-date-picker v-model="to" type="month" value-format="YYYY-MM" format="YYYY年M月" :clearable="false" style="width: 130px" @change="fix" />
  </span>
</template>

<style scoped>
.period-range { display: inline-flex; align-items: center; gap: 6px; }
</style>

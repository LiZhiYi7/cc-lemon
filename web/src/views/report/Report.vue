<script setup lang="ts">
// 通用报表页：按后端返回的模板结果渲染，新增报表模板无需改前端。
import { computed, onMounted, ref } from 'vue'
import { useRoute } from 'vue-router'
import { http } from '@/api/http'
import type { ReportResult, ReportRow } from '@/api/types'
import ExportButton from '@/components/ExportButton.vue'
import { useBook } from '@/stores/book'
import { fmt } from '@/utils/money'
import { periodStr } from '@/utils/period'

const book = useBook()
const route = useRoute()
const period = ref(periodStr(book.detail!.current_period))
const data = ref<ReportResult | null>(null)
const loading = ref(false)

async function load() {
  loading.value = true
  try {
    data.value = await http.get<ReportResult>(`${book.base}/reports/${route.params.code}`, { period: period.value })
  } finally {
    loading.value = false
  }
}
onMounted(load)

// 左右对照布局：左、右两列各自按顺序排列，行数不足的一侧留空
const pairs = computed(() => {
  const rows = data.value?.rows ?? []
  const left = rows.filter((r) => r.side !== 'right')
  const right = rows.filter((r) => r.side === 'right')
  return Array.from({ length: Math.max(left.length, right.length) }, (_, i) => [left[i], right[i]] as (ReportRow | undefined)[])
})
const failed = computed(() => data.value?.checks.filter((c) => !c.ok) ?? [])
const print = () => window.print()
</script>

<template>
  <div class="page">
    <div class="toolbar no-print">
      <h2 class="page-title">{{ data?.name }}</h2>
      <el-date-picker v-model="period" type="month" value-format="YYYY-MM" format="YYYY年M月" :clearable="false" style="width: 140px" @change="load" />
      <span class="spacer" />
      <ExportButton :name="data?.name ?? '报表'" :suffix="period" />
      <el-button @click="print">打印</el-button>
    </div>
    <el-alert v-for="c in failed" :key="c.name" type="warning" show-icon :closable="false" style="margin-bottom: 10px"
      :title="`校验未通过：${c.name}`" description="通常是本期损益尚未结转，或期初余额不平衡。" />
    <div v-if="data" v-loading="loading" class="card">
      <h3 class="report-title">{{ data.name }}</h3>
      <div class="report-meta muted">
        <span>编制单位：{{ book.detail!.company || book.detail!.name }}</span>
        <span>{{ period.replace('-', ' 年 ') }} 月</span>
        <span>单位：元</span>
      </div>
      <table v-if="data.layout === 'two_side'" class="sheet">
        <thead>
          <tr>
            <template v-for="side in ['资产', '负债和权益']" :key="side">
              <th>{{ side }}</th>
              <th style="width: 44px">行次</th>
              <th v-for="c in data.columns" :key="c.key" style="width: 130px">{{ c.name }}</th>
            </template>
          </tr>
        </thead>
        <tbody>
          <tr v-for="(p, i) in pairs" :key="i">
            <template v-for="(r, j) in p" :key="j">
              <td :class="{ b: r?.bold }" :style="{ paddingLeft: 10 + (r?.indent ?? 0) * 16 + 'px' }">{{ r?.name }}</td>
              <td class="center muted">{{ r?.key }}</td>
              <td v-for="c in data.columns" :key="c.key" class="num" :class="{ b: r?.bold }">{{ fmt(r?.values?.[c.key]) }}</td>
            </template>
          </tr>
        </tbody>
      </table>
      <table v-else class="sheet" :style="{ maxWidth: data.columns.length > 2 ? '1180px' : '900px', margin: '0 auto' }">
        <thead>
          <tr>
            <th style="min-width: 300px">项目</th>
            <th style="width: 50px">行次</th>
            <th v-for="c in data.columns" :key="c.key" :style="{ width: data.columns.length > 2 ? '140px' : '160px' }">{{ c.name }}</th>
          </tr>
        </thead>
        <tbody>
          <tr v-for="(r, i) in data.rows" :key="i" :class="{ bold: r.bold }">
            <td :style="{ paddingLeft: 10 + (r.indent ?? 0) * 16 + 'px' }">{{ r.name }}</td>
            <td class="center muted">{{ r.key }}</td>
            <td v-for="c in data.columns" :key="c.key" class="num">{{ fmt(r.values?.[c.key]) }}</td>
          </tr>
        </tbody>
      </table>
    </div>
  </div>
</template>

<style scoped>
.report-title { text-align: center; font-size: 20px; margin: 4px 0 10px; }
.report-meta { display: flex; justify-content: space-between; margin-bottom: 8px; font-size: 13px; }
td.b { font-weight: 600; }
</style>

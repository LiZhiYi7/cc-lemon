<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useRoute } from 'vue-router'
import { http } from '@/api/http'
import type { ConfigItem } from '@/api/types'
import { menu, type MenuItem } from '@/config/menu'
import { useBook } from '@/stores/book'
import { periodStr } from '@/utils/period'
import UserMenu from '@/components/UserMenu.vue'

const route = useRoute()
const book = useBook()
const ready = ref(false)
const reports = ref<ConfigItem[]>([])

watch(
  () => Number(route.params.bookId),
  async (id) => {
    ready.value = false
    await book.load(id)
    reports.value = await http.get<ConfigItem[]>(`${book.base}/reports`)
    ready.value = true
  },
  { immediate: true },
)

const prefix = computed(() => `/b/${route.params.bookId}/`)
const groups = computed(() =>
  menu
    .map((g) => {
      const items: MenuItem[] =
        g.dynamic === 'reports' ? reports.value.map((r) => ({ title: r.name, path: `reports/${r.code}` })) : g.items
      return {
        ...g,
        items: items.filter((i) => (!i.perm || book.can(i.perm)) && (!i.multiLedgerOnly || book.multiLedger)),
      }
    })
    .filter((g) => g.items.length),
)
const active = computed(() => {
  const rel = route.path.slice(prefix.value.length)
  // 凭证编辑页归到“凭证列表”
  if (/^vouchers\/\d+/.test(rel)) return prefix.value + 'vouchers'
  return route.path
})
</script>

<template>
  <el-container v-if="ready && book.detail" class="layout">
    <el-aside width="200px" class="aside">
      <div class="brand">柠檬账</div>
      <el-menu :default-active="active" router :default-openeds="groups.map((g) => g.title)">
        <el-sub-menu v-for="g in groups" :key="g.title" :index="g.title">
          <template #title>
            <el-icon><component :is="g.icon" /></el-icon>
            <span>{{ g.title }}</span>
          </template>
          <el-menu-item v-for="i in g.items" :key="i.path" :index="prefix + i.path">{{ i.title }}</el-menu-item>
        </el-sub-menu>
      </el-menu>
    </el-aside>
    <el-container>
      <el-header class="header" height="52px">
        <router-link to="/books" class="book-name" title="切换账套">{{ book.detail.name }} ▾</router-link>
        <el-tag size="small" type="info">{{ book.detail.standard_name }}</el-tag>
        <span class="muted">当前期间 {{ periodStr(book.detail.current_period) }}</span>
        <span style="flex: 1" />
        <UserMenu />
      </el-header>
      <el-main class="main">
        <router-view :key="route.fullPath" />
      </el-main>
    </el-container>
  </el-container>
  <div v-else v-loading="true" style="height: 100%" />
</template>

<style scoped>
.layout { height: 100%; }
.aside { background: #fff; border-right: 1px solid var(--lemon-border); overflow-y: auto; }
.brand { height: 52px; line-height: 52px; padding-left: 20px; font-size: 18px; font-weight: 700; color: var(--el-color-primary); }
.aside :deep(.el-menu) { border-right: none; }
.aside :deep(.el-menu-item) { height: 40px; line-height: 40px; }
.header { background: #fff; border-bottom: 1px solid var(--lemon-border); display: flex; align-items: center; gap: 12px; }
.book-name { font-weight: 600; color: inherit; text-decoration: none; }
.main { padding: 0; }
</style>

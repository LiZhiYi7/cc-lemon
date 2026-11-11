<script setup lang="ts">
import { onMounted, reactive, ref } from 'vue'
import { useRouter } from 'vue-router'
import { http } from '@/api/http'
import type { Book, Standard } from '@/api/types'
import { periodStr } from '@/utils/period'
import UserMenu from '@/components/UserMenu.vue'

const router = useRouter()
const books = ref<Book[]>([])
const standards = ref<Standard[]>([])
const visible = ref(false)
const saving = ref(false)
const now = new Date()
const form = reactive({ name: '', company: '', standard_code: '', start: `${now.getFullYear()}-01` })

async function load() {
  books.value = await http.get<Book[]>('/books')
}
onMounted(async () => {
  await load()
  standards.value = await http.get<Standard[]>('/standards')
})

function open() {
  form.name = form.company = ''
  form.standard_code = standards.value[0]?.code ?? ''
  visible.value = true
}
async function create() {
  saving.value = true
  try {
    const [y, m] = form.start.split('-').map(Number)
    const b = await http.post<Book>('/books', { ...form, start_year: y, start_month: m })
    visible.value = false
    router.push(`/b/${b.id}/settings/opening`)
  } finally {
    saving.value = false
  }
}
</script>

<template>
  <div class="topbar">
    <b>柠檬账</b>
    <span class="spacer" />
    <UserMenu />
  </div>
  <div class="page">
    <div class="toolbar">
      <h2 class="page-title">我的账套</h2>
      <span class="spacer" />
      <el-button type="primary" @click="open">新建账套</el-button>
    </div>
    <el-empty v-if="!books.length" description="还没有账套，点击右上角“新建账套”开始" />
    <div class="grid">
      <div v-for="b in books" :key="b.id" class="card book" @click="router.push(`/b/${b.id}`)">
        <div class="name">{{ b.name }}</div>
        <div class="muted">{{ b.company || '—' }}</div>
        <div class="meta">
          <el-tag size="small" type="info">{{ b.standard_name }}</el-tag>
          <span class="muted">当前期间 {{ periodStr(b.current_period) }}</span>
        </div>
      </div>
    </div>
  </div>

  <el-dialog v-model="visible" title="新建账套" width="520px">
    <el-form label-width="96px">
      <el-form-item label="账套名称" required><el-input v-model="form.name" /></el-form-item>
      <el-form-item label="单位名称"><el-input v-model="form.company" /></el-form-item>
      <el-form-item label="会计准则" required>
        <el-select v-model="form.standard_code" style="width: 100%">
          <el-option v-for="s in standards" :key="s.code" :value="s.code" :label="s.name">
            <span>{{ s.name }}</span>
            <span v-if="s.ledgers.length > 1" class="muted" style="margin-left: 8px">平行记账</span>
          </el-option>
        </el-select>
        <div class="muted">{{ standards.find((s) => s.code === form.standard_code)?.description }}</div>
      </el-form-item>
      <el-form-item label="启用期间" required>
        <el-date-picker v-model="form.start" type="month" value-format="YYYY-MM" format="YYYY年M月" :clearable="false" />
        <div class="muted" style="width: 100%">会计准则与启用期间在建账后不可修改</div>
      </el-form-item>
    </el-form>
    <template #footer>
      <el-button @click="visible = false">取消</el-button>
      <el-button type="primary" :loading="saving" :disabled="!form.name || !form.standard_code" @click="create">创建</el-button>
    </template>
  </el-dialog>
</template>

<style scoped>
.topbar { height: 52px; background: #fff; border-bottom: 1px solid var(--lemon-border); display: flex; align-items: center; padding: 0 20px; font-size: 16px; }
.topbar .spacer { flex: 1; }
.grid { display: grid; grid-template-columns: repeat(auto-fill, minmax(280px, 1fr)); gap: 14px; }
.book { cursor: pointer; transition: box-shadow 0.15s; }
.book:hover { box-shadow: 0 2px 10px rgba(30, 60, 120, 0.12); border-color: var(--el-color-primary); }
.name { font-size: 16px; font-weight: 600; margin-bottom: 4px; }
.meta { display: flex; justify-content: space-between; align-items: center; margin-top: 14px; font-size: 13px; }
</style>

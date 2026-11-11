<script setup lang="ts">
import { onMounted, reactive, ref } from 'vue'
import { useRouter } from 'vue-router'
import { ElMessage, ElMessageBox } from 'element-plus'
import { http } from '@/api/http'
import type { BookSettings, Member, Role, User } from '@/api/types'
import { useBook } from '@/stores/book'

const book = useBook()
const router = useRouter()
const detail = book.detail!
const form = reactive({
  name: detail.name,
  company: detail.company,
  settings: JSON.parse(JSON.stringify(detail.settings)) as BookSettings,
})
const members = ref<Member[]>([])
const roles = ref<Role[]>([])
const users = ref<User[]>([])
const adding = reactive({ user_id: undefined as number | undefined, role: '' })

async function loadMembers() {
  members.value = await http.get<Member[]>(`${book.base}/members`)
}
onMounted(async () => {
  ;[roles.value, users.value] = await Promise.all([http.get<Role[]>('/roles'), http.get<User[]>('/users')])
  adding.role = roles.value[1]?.code ?? roles.value[0]?.code ?? ''
  loadMembers()
})

async function save() {
  await http.patch(book.base, form)
  await book.reloadDetail()
  ElMessage.success('设置已保存')
}
async function setRole(userId: number, role: string) {
  await http.put(`${book.base}/members/${userId}`, { role }).finally(loadMembers)
}
async function addMember() {
  if (!adding.user_id) return
  await setRole(adding.user_id, adding.role)
  adding.user_id = undefined
}
async function removeMember(m: Member) {
  await ElMessageBox.confirm(`将 ${m.display_name} 移出本账套？`, '移除成员', { type: 'warning' })
  await http.del(`${book.base}/members/${m.user_id}`)
  loadMembers()
}
async function removeBook() {
  const { value } = await ElMessageBox.prompt(`删除后账套全部数据无法恢复。请输入账套名称“${detail.name}”确认：`, '删除账套', { type: 'error' })
  await http.del(book.base, { confirm: value })
  ElMessage.success('账套已删除')
  router.replace('/books')
}
</script>

<template>
  <div class="page">
    <h2 class="page-title" style="margin-bottom: 12px">账套设置</h2>
    <div class="card block">
      <h3>基本信息</h3>
      <el-form label-width="150px" style="max-width: 640px">
        <el-form-item label="账套名称"><el-input v-model="form.name" /></el-form-item>
        <el-form-item label="单位名称"><el-input v-model="form.company" /></el-form-item>
        <el-form-item label="会计准则">{{ detail.standard_name }}</el-form-item>
        <el-form-item label="启用期间">{{ detail.start_year }} 年 {{ detail.start_month }} 月</el-form-item>
        <el-form-item label="科目编码方案">{{ form.settings.account_code_scheme.join('-') }}</el-form-item>
        <el-form-item label="审核人不得为制单人"><el-switch v-model="form.settings.voucher.reviewer_must_differ" /></el-form-item>
        <el-form-item label="结账前凭证须全部审核"><el-switch v-model="form.settings.voucher.require_review_to_close" /></el-form-item>
        <el-form-item v-if="book.multiLedger" label="平行记账完整性检查">
          <el-radio-group v-model="form.settings.parallel.mode">
            <el-radio value="off">不检查</el-radio>
            <el-radio value="warn">保存时提示</el-radio>
            <el-radio value="strict">缺少时禁止保存</el-radio>
          </el-radio-group>
          <div class="muted">财务会计分录涉及现金类科目、却没有录入预算会计分录时的处理方式</div>
        </el-form-item>
        <el-form-item><el-button type="primary" @click="save">保存设置</el-button></el-form-item>
      </el-form>
    </div>

    <div class="card block">
      <h3>成员与角色</h3>
      <el-table :data="members" border style="max-width: 760px">
        <el-table-column prop="display_name" label="姓名" />
        <el-table-column prop="username" label="用户名" />
        <el-table-column label="角色" width="200">
          <template #default="{ row }">
            <el-select :model-value="row.role" @change="(v: string) => setRole(row.user_id, v)">
              <el-option v-for="r in roles" :key="r.code" :value="r.code" :label="r.name" />
            </el-select>
          </template>
        </el-table-column>
        <el-table-column label="操作" width="100" align="center">
          <template #default="{ row }"><el-button link type="danger" @click="removeMember(row)">移除</el-button></template>
        </el-table-column>
      </el-table>
      <div class="toolbar" style="margin-top: 12px">
        <el-select v-model="adding.user_id" placeholder="选择用户" filterable style="width: 200px">
          <el-option v-for="u in users.filter((u) => u.enabled && !members.some((m) => m.user_id === u.id))" :key="u.id" :value="u.id" :label="`${u.display_name}（${u.username}）`" />
        </el-select>
        <el-select v-model="adding.role" style="width: 140px">
          <el-option v-for="r in roles" :key="r.code" :value="r.code" :label="r.name" />
        </el-select>
        <el-button :disabled="!adding.user_id" @click="addMember">添加成员</el-button>
        <span class="muted">新用户由系统管理员在“用户管理”中创建</span>
      </div>
    </div>

    <div class="card block">
      <h3>危险操作</h3>
      <el-button type="danger" plain @click="removeBook">删除账套</el-button>
    </div>
  </div>
</template>

<style scoped>
.block { margin-bottom: 14px; }
.block h3 { margin: 0 0 14px; font-size: 15px; }
</style>

<script setup lang="ts">
import { onMounted, reactive, ref } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import { http } from '@/api/http'
import type { User } from '@/api/types'
import UserMenu from '@/components/UserMenu.vue'

const users = ref<User[]>([])
const visible = ref(false)
const form = reactive({ username: '', display_name: '', password: '', is_admin: false })

const load = async () => (users.value = await http.get<User[]>('/users'))
onMounted(load)

async function create() {
  await http.post('/users', form)
  visible.value = false
  ElMessage.success('用户已创建')
  load()
}
async function patch(u: User, body: object) {
  await http.patch(`/users/${u.id}`, body).finally(load)
}
async function resetPassword(u: User) {
  const { value } = await ElMessageBox.prompt(`为 ${u.display_name} 设置新密码（至少 6 位）`, '重置密码', { inputType: 'password' })
  await patch(u, { password: value })
  ElMessage.success('密码已重置')
}
</script>

<template>
  <div class="topbar">
    <router-link to="/books" class="link">← 账套列表</router-link>
    <span style="flex: 1" />
    <UserMenu />
  </div>
  <div class="page">
    <div class="toolbar">
      <h2 class="page-title">用户管理</h2>
      <span class="spacer" />
      <el-button type="primary" @click="Object.assign(form, { username: '', display_name: '', password: '', is_admin: false }); visible = true">新增用户</el-button>
    </div>
    <el-table :data="users" border>
      <el-table-column prop="username" label="用户名" />
      <el-table-column prop="display_name" label="姓名" />
      <el-table-column label="系统管理员" width="120" align="center">
        <template #default="{ row }"><el-switch :model-value="row.is_admin" @change="(v: any) => patch(row, { is_admin: v })" /></template>
      </el-table-column>
      <el-table-column label="启用" width="100" align="center">
        <template #default="{ row }"><el-switch :model-value="row.enabled" @change="(v: any) => patch(row, { enabled: v })" /></template>
      </el-table-column>
      <el-table-column label="操作" width="120" align="center">
        <template #default="{ row }"><el-button link type="primary" @click="resetPassword(row)">重置密码</el-button></template>
      </el-table-column>
    </el-table>
    <p class="muted">用户能访问哪些账套、担任什么角色，在各账套的“账套设置 → 成员”中分配。</p>
  </div>
  <el-dialog v-model="visible" title="新增用户" width="420px">
    <el-form label-width="90px">
      <el-form-item label="用户名" required><el-input v-model="form.username" /></el-form-item>
      <el-form-item label="姓名"><el-input v-model="form.display_name" /></el-form-item>
      <el-form-item label="初始密码" required><el-input v-model="form.password" type="password" show-password /></el-form-item>
      <el-form-item label="系统管理员"><el-switch v-model="form.is_admin" /></el-form-item>
    </el-form>
    <template #footer>
      <el-button @click="visible = false">取消</el-button>
      <el-button type="primary" @click="create">创建</el-button>
    </template>
  </el-dialog>
</template>

<style scoped>
.topbar { height: 52px; background: #fff; border-bottom: 1px solid var(--lemon-border); display: flex; align-items: center; padding: 0 20px; }
</style>

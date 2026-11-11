<script setup lang="ts">
import { reactive, ref } from 'vue'
import { useRouter } from 'vue-router'
import { ElMessage } from 'element-plus'
import { http } from '@/api/http'
import { useSession } from '@/stores/session'

const session = useSession()
const router = useRouter()
const pwVisible = ref(false)
const pw = reactive({ old: '', new: '' })

function command(cmd: string) {
  if (cmd === 'logout') {
    session.logout()
    router.push('/login')
  } else if (cmd === 'password') {
    pw.old = pw.new = ''
    pwVisible.value = true
  } else router.push(cmd)
}
async function changePassword() {
  await http.post('/auth/password', pw)
  ElMessage.success('密码已修改')
  pwVisible.value = false
}
</script>

<template>
  <el-dropdown @command="command">
    <span class="user">{{ session.user?.display_name }} ▾</span>
    <template #dropdown>
      <el-dropdown-menu>
        <el-dropdown-item command="/books">账套列表</el-dropdown-item>
        <el-dropdown-item v-if="session.user?.is_admin" command="/users">用户管理</el-dropdown-item>
        <el-dropdown-item command="password">修改密码</el-dropdown-item>
        <el-dropdown-item divided command="logout">退出登录</el-dropdown-item>
      </el-dropdown-menu>
    </template>
  </el-dropdown>
  <el-dialog v-model="pwVisible" title="修改密码" width="380px" append-to-body>
    <el-form label-width="80px">
      <el-form-item label="原密码"><el-input v-model="pw.old" type="password" show-password /></el-form-item>
      <el-form-item label="新密码"><el-input v-model="pw.new" type="password" show-password /></el-form-item>
    </el-form>
    <template #footer>
      <el-button @click="pwVisible = false">取消</el-button>
      <el-button type="primary" @click="changePassword">确定</el-button>
    </template>
  </el-dialog>
</template>

<style scoped>
.user { cursor: pointer; color: inherit; outline: none; }
</style>

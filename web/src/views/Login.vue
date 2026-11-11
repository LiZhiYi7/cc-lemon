<script setup lang="ts">
import { onMounted, reactive, ref } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { http } from '@/api/http'
import { useSession } from '@/stores/session'

const session = useSession()
const router = useRouter()
const route = useRoute()
const initialized = ref(true)
const loading = ref(false)
const form = reactive({ username: '', password: '', display_name: '' })

onMounted(async () => {
  initialized.value = (await http.get<{ initialized: boolean }>('/setup')).initialized
})

async function submit() {
  if (!form.username || !form.password) return
  loading.value = true
  try {
    if (!initialized.value) {
      await http.post('/setup', form)
      initialized.value = true
    }
    await session.login(form.username, form.password)
    router.replace((route.query.redirect as string) || '/books')
  } finally {
    loading.value = false
  }
}
</script>

<template>
  <div class="login">
    <div class="box card">
      <h1>柠檬账</h1>
      <p class="muted">{{ initialized ? '局域网财务系统' : '首次使用，请创建系统管理员' }}</p>
      <el-form label-position="top" @submit.prevent="submit">
        <el-form-item label="用户名"><el-input v-model="form.username" autofocus /></el-form-item>
        <el-form-item v-if="!initialized" label="姓名"><el-input v-model="form.display_name" /></el-form-item>
        <el-form-item label="密码"><el-input v-model="form.password" type="password" show-password @keyup.enter="submit" /></el-form-item>
        <el-button type="primary" :loading="loading" style="width: 100%" @click="submit">
          {{ initialized ? '登录' : '创建管理员并登录' }}
        </el-button>
      </el-form>
    </div>
  </div>
</template>

<style scoped>
.login { height: 100%; display: flex; align-items: center; justify-content: center; }
.box { width: 360px; padding: 32px; }
h1 { margin: 0 0 4px; font-size: 24px; }
p { margin: 0 0 20px; }
</style>

import { defineConfig, loadEnv } from 'vite'
import vue from '@vitejs/plugin-vue'
import { fileURLToPath, URL } from 'node:url'

export default defineConfig(({ mode }) => {
  const env = loadEnv(mode, process.cwd(), '')
  return {
    plugins: [vue()],
    resolve: { alias: { '@': fileURLToPath(new URL('./src', import.meta.url)) } },
    server: {
      // 开发时把 /api 转发到后端；地址通过 VITE_API_TARGET 配置
      proxy: { '/api': env.VITE_API_TARGET || 'http://localhost:8080' },
    },
    build: { chunkSizeWarningLimit: 1500 },
  }
})

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
    build: {
      chunkSizeWarningLimit: 1500,
      // 客户端里有 Windows 7 机器。Win7 最后可用的浏览器是 Chrome 109 /
      // Firefox ESR 115，这里显式把构建目标压到这个基线，
      // 避免 Vite 6 默认目标（baseline-widely-available）之外的新语法漏进去。
      // 注意：IE11 不支持（Vue 3 本身不支持），Win7 必须用上述浏览器。
      target: ['chrome109', 'firefox115', 'safari16'],
    },
  }
})

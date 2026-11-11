import { createApp } from 'vue'
import { createPinia } from 'pinia'
import ElementPlus, { ElMessage } from 'element-plus'
import zhCn from 'element-plus/es/locale/lang/zh-cn'
import 'element-plus/dist/index.css'
import './style.css'
import App from './App.vue'
import router from './router'
import { ApiError, setUnauthorizedHandler } from './api/http'
import { useSession } from './stores/session'

const app = createApp(App)
app.use(createPinia())
app.use(router)
app.use(ElementPlus, { locale: zhCn })

setUnauthorizedHandler(() => {
  useSession().logout()
  if (router.currentRoute.value.path !== '/login') router.push('/login')
})

// 未被页面自行处理的接口错误统一提示
const report = (err: unknown) => {
  if (err instanceof ApiError) ElMessage.error(err.message)
  else console.error(err)
}
app.config.errorHandler = report
window.addEventListener('unhandledrejection', (e) => {
  if (e.reason instanceof ApiError) {
    e.preventDefault()
    report(e.reason)
  }
})

app.mount('#app')

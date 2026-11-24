// 凭证图片的“待上传队列”。
//
// 放在模块级而不是组件里：保存凭证后路由会 replace 一次，页面组件会被重建，
// 组件内的 ref 会丢。新凭证选的图在保存成功前只存在浏览器里，丢了用户就得重选。
import { ref } from 'vue'

export interface PendingFile {
  key: string
  file: File
  url: string
}

const queue = ref<PendingFile[]>([])

export function useAttachQueue() {
  return queue
}

export function addPending(files: PendingFile[]) {
  queue.value = [...queue.value, ...files]
}

export function dropPending(key: string) {
  const hit = queue.value.find((p) => p.key === key)
  if (hit) URL.revokeObjectURL(hit.url)
  queue.value = queue.value.filter((p) => p.key !== key)
}

export function clearPending() {
  queue.value.forEach((p) => URL.revokeObjectURL(p.url))
  queue.value = []
}

<script setup lang="ts">
// 凭证图片：缩略图条 + 上传（点击选择 / 拖拽）。
//
// 新凭证还没有 id，选好的图先进入模块级待上传队列做本地预览，
// 保存成功后由页面调用 flush(id) 逐张上传。
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import { http } from '@/api/http'
import type { Attachment } from '@/api/types'
import { addPending, dropPending, useAttachQueue } from '@/stores/attachQueue'

const props = defineProps<{
  base: string
  voucherId: number
  readonly?: boolean
  max?: number
}>()

const ACCEPT = ['image/jpeg', 'image/png', 'image/webp']
const MAX_BYTES = 12 * 1024 * 1024
const THUMB_EDGE = 512

const queue = useAttachQueue()
const items = ref<Attachment[]>([])
const urls = ref<Record<string, string>>({})
const loading = ref(false)
const uploading = ref(false)
const dragging = ref(false)
const fileInput = ref<HTMLInputElement | null>(null)

const limit = computed(() => props.max ?? 20)
const total = computed(() => items.value.length + queue.value.length)
const canEdit = computed(() => !props.readonly && total.value < limit.value)

/** 大图预览顺序：已保存的在前，待上传的在后。 */
const previewList = computed(() => [
  ...items.value.map((i) => urls.value[i.id]).filter(Boolean),
  ...queue.value.map((p) => p.url),
])

function pick() {
  fileInput.value?.click()
}

function onPicked(e: Event) {
  const el = e.target as HTMLInputElement
  addFiles(Array.from(el.files ?? []))
  el.value = ''
}

function onDrop(e: DragEvent) {
  dragging.value = false
  if (props.readonly) return
  addFiles(Array.from(e.dataTransfer?.files ?? []))
}

function addFiles(files: File[]) {
  if (props.readonly) return
  const room = limit.value - total.value
  if (room <= 0) {
    ElMessage.warning(`最多 ${limit.value} 张图片，请先删除一些`)
    return
  }
  const accepted = []
  for (const f of files) {
    if (accepted.length >= room) {
      ElMessage.warning(`最多 ${limit.value} 张图片，多出的已忽略`)
      break
    }
    if (!ACCEPT.includes(f.type)) {
      ElMessage.warning(`${f.name}：只支持 JPG / PNG / WebP`)
      continue
    }
    if (f.size > MAX_BYTES) {
      ElMessage.warning(`${f.name}：超过 12MB`)
      continue
    }
    accepted.push({ key: `${Date.now()}-${Math.random().toString(16).slice(2)}`, file: f, url: URL.createObjectURL(f) })
  }
  if (accepted.length) addPending(accepted)
}

async function reload() {
  if (props.voucherId <= 0) {
    items.value = []
    return
  }
  loading.value = true
  try {
    const list = await http.get<Attachment[]>(`${props.base}/vouchers/${props.voucherId}/attachments`)
    items.value = list ?? []
    // 逐个取图：令牌在请求头里，不能把接口地址直接塞进 <img src>。
    const next = { ...urls.value }
    await Promise.all(
      items.value.map(async (it) => {
        if (next[it.id]) return
        try {
          next[it.id] = URL.createObjectURL(await http.blob(`${props.base}/vouchers/${props.voucherId}/attachments/${it.id}`))
        } catch {
          /* 单张取不到不影响其它图片 */
        }
      }),
    )
    urls.value = next
  } catch (e: any) {
    ElMessage.error(e?.message || '读取凭证图片失败')
  } finally {
    loading.value = false
  }
}

async function removeImage(it: Attachment) {
  await ElMessageBox.confirm(`删除图片「${it.name}」？`, '删除图片', { type: 'warning' })
  await http.del(`${props.base}/vouchers/${props.voucherId}/attachments/${it.id}`)
  if (urls.value[it.id]) URL.revokeObjectURL(urls.value[it.id])
  items.value = items.value.filter((x) => x.id !== it.id)
  ElMessage.success('已删除')
}

/** 生成 512px 缩略图；浏览器不支持时返回 null，服务端会回落到原图。 */
function makeThumb(file: File): Promise<Blob | null> {
  return new Promise((resolve) => {
    const url = URL.createObjectURL(file)
    const img = new Image()
    img.onload = () => {
      try {
        const scale = Math.min(1, THUMB_EDGE / Math.max(img.width, img.height))
        const canvas = document.createElement('canvas')
        canvas.width = Math.max(1, Math.round(img.width * scale))
        canvas.height = Math.max(1, Math.round(img.height * scale))
        const ctx = canvas.getContext('2d')
        if (!ctx) {
          resolve(null)
          return
        }
        ctx.drawImage(img, 0, 0, canvas.width, canvas.height)
        canvas.toBlob((b) => resolve(b), 'image/jpeg', 0.82)
      } catch {
        resolve(null)
      } finally {
        URL.revokeObjectURL(url)
      }
    }
    img.onerror = () => {
      URL.revokeObjectURL(url)
      resolve(null)
    }
    img.src = url
  })
}

/** 逐张上传待上传队列。返回失败张数，由页面决定怎么提示。 */
async function flush(voucherId: number): Promise<{ ok: number; failed: number }> {
  if (!queue.value.length) return { ok: 0, failed: 0 }
  if (voucherId <= 0) return { ok: 0, failed: queue.value.length }
  uploading.value = true
  let ok = 0
  let failed = 0
  // 串行上传：保持图片顺序，也不会同时打满浏览器连接数。
  for (const p of [...queue.value]) {
    try {
      const form = new FormData()
      form.append('file', p.file, p.file.name)
      const thumb = await makeThumb(p.file)
      if (thumb) form.append('thumb', thumb, 'thumb.jpg')
      const saved = await http.upload<Attachment>(`${props.base}/vouchers/${voucherId}/attachments`, form)
      items.value = [...items.value, saved]
      try {
        urls.value = {
          ...urls.value,
          [saved.id]: URL.createObjectURL(await http.blob(`${props.base}/vouchers/${voucherId}/attachments/${saved.id}`)),
        }
      } catch {
        /* 预览失败不影响已经保存成功这件事 */
      }
      dropPending(p.key)
      ok++
    } catch (e: any) {
      failed++
      if (failed === 1 && e?.message) ElMessage.error(e.message)
    }
  }
  uploading.value = false
  return { ok, failed }
}

/** 凭证已保存过、只是上次有图没传上去时，手动补传。 */
async function retry() {
  if (props.voucherId <= 0) return
  const res = await flush(props.voucherId)
  if (res.ok && !res.failed) ElMessage.success(`已补传 ${res.ok} 张图片`)
}

onMounted(reload)
watch(() => props.voucherId, reload)

onBeforeUnmount(() => {
  // 只回收“已在服务器上”的预览地址；待上传队列归队列模块管，页面重建后还要用。
  Object.values(urls.value).forEach((u) => URL.revokeObjectURL(u))
})

defineExpose({ flush, reload, retry })
</script>

<template>
  <div
    class="attach"
    :class="{ dragging }"
    @dragover.prevent="dragging = true"
    @dragleave.prevent="dragging = false"
    @drop.prevent="onDrop"
  >
    <div class="attach-head">
      <span class="label">图片附件</span>
      <span class="muted count">{{ total }} / {{ limit }}</span>
      <span class="spacer" />
      <template v-if="canEdit">
        <el-button size="small" :loading="uploading" @click="pick">选择图片</el-button>
        <span class="muted hint no-print">也可直接拖进来</span>
      </template>
      <el-button v-else-if="queue.length && !readonly" size="small" type="warning" plain :loading="uploading" @click="retry">
        重试上传（{{ queue.length }}）
      </el-button>
    </div>

    <div v-if="!total" class="empty muted">
      {{ readonly ? '没有图片附件' : '把发票、回单等图片拖到这里，或点击「选择图片」' }}
    </div>

    <div v-else class="thumbs">
      <div v-for="it in items" :key="it.id" class="thumb">
        <el-image
          v-if="urls[it.id]"
          :src="urls[it.id]"
          :preview-src-list="previewList"
          :initial-index="previewList.indexOf(urls[it.id])"
          fit="cover"
        />
        <div v-else class="ph muted">{{ loading ? '…' : '预览失败' }}</div>
        <button v-if="canEdit" class="del no-print" title="删除" @click="removeImage(it)">×</button>
        <div class="name" :title="it.name">{{ it.name }}</div>
      </div>

      <div v-for="p in queue" :key="p.key" class="thumb pending">
        <el-image :src="p.url" :preview-src-list="previewList" :initial-index="previewList.indexOf(p.url)" fit="cover" />
        <button v-if="!readonly" class="del no-print" title="移除" @click="dropPending(p.key)">×</button>
        <div class="name" :title="p.file.name">待上传 · {{ p.file.name }}</div>
      </div>
    </div>

    <input ref="fileInput" type="file" :accept="ACCEPT.join(',')" multiple hidden @change="onPicked" />
  </div>
</template>

<style scoped>
.attach {
  margin-top: 12px;
  padding: 8px 10px;
  border: 1px dashed var(--el-border-color);
  border-radius: 4px;
  background: #fbfcfe;
}
.attach.dragging {
  border-color: var(--el-color-primary);
  background: var(--el-color-primary-light-9);
}
.attach-head {
  display: flex;
  align-items: center;
  gap: 8px;
  font-size: 13px;
}
.attach-head .label {
  font-weight: 600;
}
.attach-head .spacer {
  flex: 1;
}
.attach-head .hint,
.attach-head .count {
  font-size: 12px;
}
.empty {
  padding: 10px 2px 6px;
  font-size: 13px;
}
.thumbs {
  display: flex;
  flex-wrap: wrap;
  gap: 8px;
  margin-top: 8px;
}
.thumb {
  position: relative;
  width: 92px;
}
.thumb .el-image,
.thumb .ph {
  width: 92px;
  height: 92px;
  border: 1px solid var(--el-border-color-lighter);
  border-radius: 4px;
  background: #fff;
  cursor: zoom-in;
}
.thumb .ph {
  display: flex;
  align-items: center;
  justify-content: center;
  font-size: 12px;
  cursor: default;
}
.thumb.pending .el-image {
  outline: 1px dashed var(--el-color-warning);
}
.thumb .name {
  margin-top: 2px;
  font-size: 11px;
  color: var(--el-text-color-secondary);
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}
.thumb .del {
  position: absolute;
  top: -6px;
  right: -6px;
  width: 18px;
  height: 18px;
  line-height: 16px;
  border: none;
  border-radius: 50%;
  background: var(--el-color-danger);
  color: #fff;
  cursor: pointer;
  font-size: 13px;
  padding: 0;
}
@media print {
  .attach {
    border-style: solid;
  }
  .no-print {
    display: none !important;
  }
}
</style>

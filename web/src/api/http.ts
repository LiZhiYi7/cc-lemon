// 统一的 HTTP 客户端：自动带令牌、解析统一错误模型。

export class ApiError extends Error {
  constructor(public status: number, public code: string, message: string, public details?: unknown) {
    super(message)
  }
}

const TOKEN_KEY = 'yanghe-finance.token'
// 旧版本（柠檬账）用的 key。升级后先把它搬过来，避免所有人被强制退出登录。
const LEGACY_TOKEN_KEY = 'lemon.token'

export const tokenStore = {
  get: () => {
    const current = localStorage.getItem(TOKEN_KEY)
    if (current) return current
    const legacy = localStorage.getItem(LEGACY_TOKEN_KEY)
    if (legacy) {
      localStorage.setItem(TOKEN_KEY, legacy)
      localStorage.removeItem(LEGACY_TOKEN_KEY)
      return legacy
    }
    return ''
  },
  set: (t: string) => {
    if (t) {
      localStorage.setItem(TOKEN_KEY, t)
      return
    }
    localStorage.removeItem(TOKEN_KEY)
    localStorage.removeItem(LEGACY_TOKEN_KEY)
  },
}

let onUnauthorized: () => void = () => {}
export function setUnauthorizedHandler(fn: () => void) {
  onUnauthorized = fn
}

type Query = Record<string, string | number | boolean | undefined | null>

const NETWORK_ERROR = new ApiError(0, 'network', '无法连接服务器，请检查网络')

function authHeaders(): { headers: Record<string, string>; token: string } {
  const token = tokenStore.get()
  const headers: Record<string, string> = {}
  if (token) headers.Authorization = 'Bearer ' + token
  return { headers, token }
}

/** 统一把失败响应翻译成 ApiError（JSON、上传、取图三条路径共用）。 */
async function throwApiError(res: Response, token: string): Promise<never> {
  const data = await res.json().catch(() => null)
  const e = data?.error
  if (res.status === 401 && token) onUnauthorized()
  throw new ApiError(res.status, e?.code || 'error', e?.message || `请求失败（${res.status}）`, e?.details)
}

async function request<T>(method: string, path: string, body?: unknown, query?: Query): Promise<T> {
  let url = '/api/v1' + path
  if (query) {
    const qs = new URLSearchParams()
    for (const [k, v] of Object.entries(query)) if (v !== undefined && v !== null && v !== '') qs.set(k, String(v))
    if ([...qs].length) url += '?' + qs
  }
  const { headers, token } = authHeaders()
  if (body !== undefined) headers['Content-Type'] = 'application/json'
  let res: Response
  try {
    res = await fetch(url, { method, headers, body: body === undefined ? undefined : JSON.stringify(body) })
  } catch {
    throw NETWORK_ERROR
  }
  if (res.status === 204) return undefined as T
  if (!res.ok) return throwApiError(res, token)
  const data = await res.json().catch(() => null)
  return data as T
}

/** multipart 上传：绝不能自己设 Content-Type，浏览器要带上 boundary。 */
async function upload<T>(path: string, form: FormData): Promise<T> {
  const { headers, token } = authHeaders()
  let res: Response
  try {
    res = await fetch('/api/v1' + path, { method: 'POST', headers, body: form })
  } catch {
    throw NETWORK_ERROR
  }
  if (!res.ok) return throwApiError(res, token)
  const data = await res.json().catch(() => null)
  return data as T
}

/**
 * 取二进制内容。凭证图片要走这里而不是 <img src>：
 * 令牌放在 Authorization 头里，裸 src 带不上，只会得到 401。
 */
async function blob(path: string): Promise<Blob> {
  const { headers, token } = authHeaders()
  let res: Response
  try {
    res = await fetch('/api/v1' + path, { headers })
  } catch {
    throw NETWORK_ERROR
  }
  if (!res.ok) return throwApiError(res, token)
  return res.blob()
}

export const http = {
  get: <T>(path: string, query?: Query) => request<T>('GET', path, undefined, query),
  post: <T>(path: string, body?: unknown) => request<T>('POST', path, body ?? {}),
  put: <T>(path: string, body?: unknown) => request<T>('PUT', path, body ?? {}),
  patch: <T>(path: string, body?: unknown) => request<T>('PATCH', path, body ?? {}),
  del: <T>(path: string, query?: Query) => request<T>('DELETE', path, undefined, query),
  upload,
  blob,
}

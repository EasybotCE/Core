export type Dependency = { id: string; kind: string; name: string }
export class ApiError extends Error {
  readonly code: string
  readonly status: number
  readonly field: string
  readonly requestId: string
  readonly dependency?: Dependency
  constructor(message: string, code: string, status = 0, field = '', requestId = '', dependency?: Dependency) {
    super(message); this.name = 'ApiError'
    this.code = code; this.status = status; this.field = field; this.requestId = requestId; this.dependency = dependency
  }
}

type ClientOptions = { fetch?: typeof fetch; timeoutMs?: number; onUnauthorized?: () => void }
export function createApiClient(options: ClientOptions = {}) {
  return async function request<T>(url: string, init: RequestInit = {}): Promise<T> {
    const controller = new AbortController(), caller = init.signal
    let timedOut = false
    const abort = () => controller.abort()
    caller?.addEventListener('abort', abort, { once: true })
    const timer = setTimeout(() => { timedOut = true; controller.abort() }, options.timeoutMs ?? 30000)
    try {
      if (caller?.aborted) { controller.abort(); throw new ApiError('请求已取消', 'request_cancelled') }
      const headers = new Headers(init.headers)
      if (!headers.has('Accept')) headers.set('Accept', 'application/json')
      if (typeof init.body === 'string' && !headers.has('Content-Type')) headers.set('Content-Type', 'application/json')
      const response = await (options.fetch ?? globalThis.fetch)(url, { ...init, headers, credentials: 'same-origin', cache: 'no-store', signal: controller.signal })
      const requestId = response.headers.get('X-Request-ID') ?? ''
      if (!response.ok) {
        if (response.status === 401 && !url.startsWith('/api/v1/auth/')) options.onUnauthorized?.()
        const body = await response.json().catch(() => ({})) as Record<string, unknown>
        const message = typeof body.message === 'string' ? body.message.slice(0, 800) : `请求失败 (${response.status})`
        const code = typeof body.code === 'string' ? body.code : 'request_failed'
        const field = typeof body.field === 'string' ? body.field : ''
        const value = body.dependency as Partial<Dependency> | undefined
        const dependency = value && typeof value.id === 'string' && typeof value.kind === 'string' && typeof value.name === 'string' ? value as Dependency : undefined
        throw new ApiError(dependency ? `${message}：${dependency.name}` : message, code, response.status, field, requestId, dependency)
      }
      if (response.status === 204) return undefined as T
      return await response.json() as T
    } catch (error) {
      if (error instanceof ApiError) throw error
      if (timedOut) throw new ApiError('请求超时，请检查服务状态后重试', 'request_timeout')
      if (caller?.aborted) throw new ApiError('请求已取消', 'request_cancelled')
      throw new ApiError('无法连接管理服务或响应格式无效', 'network_error')
    } finally { clearTimeout(timer); caller?.removeEventListener('abort', abort) }
  }
}

let unauthorizedHandler: (() => void) | undefined
export function setUnauthorizedHandler(handler: () => void) { unauthorizedHandler = handler }
export const api = createApiClient({ onUnauthorized: () => unauthorizedHandler?.() })

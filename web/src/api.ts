const API_BASE = '/api/v1'

export function createApiClient(onUnauthorized: () => void) {
  return async function api<T>(path: string, init?: RequestInit): Promise<T> {
    const response = await fetch(path, {
      ...init,
      credentials: 'same-origin',
      headers: { 'Content-Type': 'application/json', ...init?.headers },
    })

    if (!response.ok) {
      if (response.status === 401 && !path.startsWith(`${API_BASE}/auth/`)) onUnauthorized()
      const body = await response.json().catch(() => ({}))
      throw new Error(body.message ?? `请求失败 (${response.status})`)
    }

    if (response.status === 204) return undefined as T
    return response.json() as Promise<T>
  }
}

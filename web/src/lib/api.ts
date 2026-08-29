const BASE = '/api/v1'

/** The API path prefix, for callers that cannot go through `request` (e.g. XHR uploads). */
export const API_BASE = BASE

export class ApiError extends Error {
  status: number
  body: Record<string, unknown> | null
  constructor(method: string, path: string, status: number, body: Record<string, unknown> | null = null) {
    super(`${method} ${path} -> ${status}`)
    this.name = 'ApiError'
    this.status = status
    this.body = body
  }
}

async function request<T>(method: string, path: string, body?: unknown): Promise<T> {
  const headers: Record<string, string> = {}
  if (body) headers['Content-Type'] = 'application/json'
  const isSyncPath = path === '/sync' || path.startsWith('/sync/')
  if (isSyncPath) {
    try {
      if (typeof window !== 'undefined' && window.localStorage) {
        const token = window.localStorage.getItem('reverb:syncToken')
        if (token) headers['Authorization'] = `Bearer ${token}`
      }
    } catch {
      /* ignore storage errors */
    }
  }
  const res = await fetch(BASE + path, {
    method,
    credentials: 'include',
    headers: Object.keys(headers).length ? headers : undefined,
    body: body ? JSON.stringify(body) : undefined,
  })
  if (!res.ok) {
    const text = await res.text().catch(() => '')
    let errBody: Record<string, unknown> | null = null
    try { errBody = text ? (JSON.parse(text) as Record<string, unknown>) : null } catch { /* ignore */ }
    throw new ApiError(method, path, res.status, errBody)
  }
  const text = await res.text()
  return (text ? JSON.parse(text) : null) as T
}

export const api = {
  get: <T>(p: string) => request<T>('GET', p),
  post: <T>(p: string, b?: unknown) => request<T>('POST', p, b),
  put: <T>(p: string, b?: unknown) => request<T>('PUT', p, b),
  patch: <T>(p: string, b?: unknown) => request<T>('PATCH', p, b),
  del: <T>(p: string) => request<T>('DELETE', p),
}

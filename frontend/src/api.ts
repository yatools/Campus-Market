import type { ApiErrorBody } from './types'
import { appConfig } from './config'

export class ApiError extends Error {
  status: number
  code: string
  fieldErrors: Record<string, string>
  requestId: string

  constructor(status: number, body: Partial<ApiErrorBody>) {
    super(body.message || `请求失败（${status}）`)
    this.status = status
    this.code = body.code || 'REQUEST_FAILED'
    this.fieldErrors = body.field_errors || {}
    this.requestId = body.request_id || ''
  }
}

function cookie(name: string): string {
  const prefix = `${name}=`
  return document.cookie
    .split(';')
    .map((item) => item.trim())
    .find((item) => item.startsWith(prefix))
    ?.slice(prefix.length) || ''
}

export async function api<T>(path: string, init: RequestInit = {}): Promise<T> {
  const headers = new Headers(init.headers)
  if (init.body && !(init.body instanceof FormData)) headers.set('Content-Type', 'application/json')
  const method = (init.method || 'GET').toUpperCase()
  if (!['GET', 'HEAD', 'OPTIONS'].includes(method)) {
    const csrf = decodeURIComponent(cookie(appConfig().csrf_cookie_name))
    if (csrf) headers.set('X-CSRF-Token', csrf)
  }
  const response = await fetch(`${appConfig().api_prefix}${path}`, { ...init, headers, credentials: 'include' })
  if (response.status === 204) { const empty: any = undefined; return empty }
  const data = await response.json().catch(() => ({}))
  if (!response.ok) throw new ApiError(response.status, data)
  return data
}

export function json(method: string, body?: unknown): RequestInit {
  return { method, body: body === undefined ? undefined : JSON.stringify(body) }
}

export function uploadImage(file: File): Promise<{ id: number; url: string; thumbnail_url: string }>
export function uploadImage(file: File, scope: 'market_dispute'): Promise<{ id: number; content_url: string }>
export async function uploadImage(file: File, scope: 'public' | 'market_dispute' = 'public'): Promise<{ id: number; url?: string; thumbnail_url?: string; content_url?: string }> {
  const data = new FormData()
  data.append('file', file)
  data.append('scope', scope)
  return api('/uploads/images', { method: 'POST', body: data })
}

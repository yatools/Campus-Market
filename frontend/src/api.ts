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

// The auth store registers a handler here so a mid-session 401 (expired/revoked
// session) can clear local auth state and prompt re-login. Kept as a callback to
// avoid api.ts ⇄ store circular imports; the handler itself no-ops when logged out.
let unauthorizedHandler: (() => void) | null = null
export function setUnauthorizedHandler(handler: (() => void) | null): void {
  unauthorizedHandler = handler
}

function cookie(name: string): string {
  const prefix = `${name}=`
  return document.cookie
    .split(';')
    .map((item) => item.trim())
    .find((item) => item.startsWith(prefix))
    ?.slice(prefix.length) || ''
}

function csrfHeaders(method: string, source?: HeadersInit): Headers {
  const headers = new Headers(source)
  if (!['GET', 'HEAD', 'OPTIONS'].includes(method.toUpperCase())) {
    const csrf = decodeURIComponent(cookie(appConfig().csrf_cookie_name))
    if (csrf) headers.set('X-CSRF-Token', csrf)
  }
  return headers
}

/** Fetch implementation shared by generated SDK calls. */
export async function sdkFetch(input: RequestInfo | URL, init: RequestInit = {}): Promise<Response> {
  const requestInput = typeof input === 'string' && input.startsWith('/')
    ? new URL(input, globalThis.location?.origin || 'http://localhost')
    : input
  const request = new Request(requestInput, init)
  const response = await globalThis.fetch(request, {
    headers: csrfHeaders(request.method, request.headers),
    credentials: 'include',
  })
  if (response.status === 401) unauthorizedHandler?.()
  return response
}

/** @deprecated Use an operationId-named function from generated/sdk in migrated features. */
export async function api<T>(path: string, init: RequestInit = {}): Promise<T> {
  const headers = new Headers(init.headers)
  if (init.body && !(init.body instanceof FormData)) headers.set('Content-Type', 'application/json')
  const method = (init.method || 'GET').toUpperCase()
  const securedHeaders = csrfHeaders(method, headers)
  const response = await fetch(`${appConfig().api_prefix}${path}`, { ...init, headers: securedHeaders, credentials: 'include' })
  if (response.status === 204) return undefined as T
  const data = await response.json().catch(() => ({}))
  if (!response.ok) {
    if (response.status === 401) unauthorizedHandler?.()
    throw new ApiError(response.status, data)
  }
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

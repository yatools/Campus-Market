import { beforeEach, describe, expect, it, vi } from 'vitest'
import { api, json } from './api'

describe('api client', () => {
  beforeEach(() => {
    Object.defineProperty(document, 'cookie', { writable: true, value: 'wutong_csrf=test-token' })
  })

  it('adds CSRF header and credentials to mutations', async () => {
    const fetchMock = vi.fn().mockResolvedValue(new Response(JSON.stringify({ ok: true }), { status: 200, headers: { 'Content-Type': 'application/json' } }))
    vi.stubGlobal('fetch', fetchMock)
    await api('/posts', json('POST', { body: 'test' }))
    const init = fetchMock.mock.calls[0][1] as RequestInit
    expect(new Headers(init.headers).get('X-CSRF-Token')).toBe('test-token')
    expect(init.credentials).toBe('include')
  })
})


import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { ApiError, api, json, sdkFetch, uploadImage } from './api'
import { setAppConfigForTest } from './config'

describe('api client', () => {
  beforeEach(() => {
    Object.defineProperty(document, 'cookie', { writable: true, value: 'wutong_csrf=test-token' })
    setAppConfigForTest({ api_prefix: '/campus/api', csrf_cookie_name: 'wutong_csrf' })
  })

  afterEach(() => vi.unstubAllGlobals())

  it('adds CSRF header and credentials to mutations', async () => {
    const fetchMock = vi.fn().mockResolvedValue(new Response(JSON.stringify({ ok: true }), { status: 200, headers: { 'Content-Type': 'application/json' } }))
    vi.stubGlobal('fetch', fetchMock)
    await api('/posts', json('POST', { body: 'test' }))
    const init = fetchMock.mock.calls[0][1] as RequestInit
    expect(new Headers(init.headers).get('X-CSRF-Token')).toBe('test-token')
    expect(init.credentials).toBe('include')
    expect(fetchMock.mock.calls[0][0]).toBe('/campus/api/posts')
  })

  it('does not add mutation headers to GET requests', async () => {
    const fetchMock = vi.fn().mockResolvedValue(new Response(JSON.stringify({ items: [] }), { status: 200 }))
    vi.stubGlobal('fetch', fetchMock)
    await api('/posts')
    const headers = new Headers((fetchMock.mock.calls[0][1] as RequestInit).headers)
    expect(headers.has('X-CSRF-Token')).toBe(false)
    expect(headers.has('Content-Type')).toBe(false)
  })

  it('applies the same session and CSRF policy to generated SDK requests', async () => {
    const fetchMock = vi.fn().mockResolvedValue(new Response(null, { status: 204 }))
    vi.stubGlobal('fetch', fetchMock)

    await sdkFetch('/api/v1/teams', { method: 'POST', body: '{}' })

    const request = fetchMock.mock.calls[0][0] as Request
    const init = fetchMock.mock.calls[0][1] as RequestInit
    expect(request.url).toBe('http://localhost:3000/api/v1/teams')
    expect(new Headers(init.headers).get('X-CSRF-Token')).toBe('test-token')
    expect(init.credentials).toBe('include')
  })

  it('returns undefined for successful no-content responses', async () => {
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(new Response(null, { status: 204 })))
    await expect(api('/sessions/1', { method: 'DELETE' })).resolves.toBeUndefined()
  })

  it('throws the structured API error returned by the server', async () => {
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(new Response(JSON.stringify({ code: 'XP_NOT_ENOUGH', message: '经验不足', field_errors: { bounty_xp: '余额不足' }, request_id: 'req-1' }), { status: 400 })))
    const error = await api('/questions', json('POST', {})).catch((value) => value)
    expect(error).toBeInstanceOf(ApiError)
    expect(error).toMatchObject({ status: 400, code: 'XP_NOT_ENOUGH', fieldErrors: { bounty_xp: '余额不足' }, requestId: 'req-1' })
  })

  it('uploads public and private images without forcing a JSON content type', async () => {
    const fetchMock = vi.fn()
      .mockResolvedValueOnce(new Response(JSON.stringify({ id: 1, url: '/uploads/a.webp', thumbnail_url: '/uploads/a-thumb.webp' }), { status: 200 }))
      .mockResolvedValueOnce(new Response(JSON.stringify({ id: 2, content_url: '/evidence/2' }), { status: 200 }))
    vi.stubGlobal('fetch', fetchMock)
    await uploadImage(new File(['a'], 'a.png', { type: 'image/png' }))
    await uploadImage(new File(['b'], 'b.png', { type: 'image/png' }), 'market_dispute')
    const first = fetchMock.mock.calls[0][1] as RequestInit
    const second = fetchMock.mock.calls[1][1] as RequestInit
    expect(new Headers(first.headers).has('Content-Type')).toBe(false)
    expect((first.body as FormData).get('scope')).toBe('public')
    expect((second.body as FormData).get('scope')).toBe('market_dispute')
  })
})

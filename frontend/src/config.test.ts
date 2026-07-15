import { afterEach, describe, expect, it, vi } from 'vitest'
import { appConfig, loadAppConfig, setAppConfigForTest } from './config'

afterEach(() => {
  vi.unstubAllGlobals()
  setAppConfigForTest({ api_prefix: '/api/v1', csrf_cookie_name: 'wutong_csrf' })
})

describe('runtime config', () => {
  it('loads the API prefix and CSRF cookie name before requests', async () => {
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(new Response(JSON.stringify({ api_prefix: '/campus/api', csrf_cookie_name: 'campus_csrf' }), { status: 200 })))
    await loadAppConfig()
    expect(appConfig()).toEqual({ api_prefix: '/campus/api', csrf_cookie_name: 'campus_csrf' })
  })

  it('rejects incomplete config', async () => {
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(new Response('{}', { status: 200 })))
    await expect(loadAppConfig()).rejects.toThrow('运行时配置缺少必要字段')
  })
})

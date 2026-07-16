import { describe, expect, it, vi } from 'vitest'

const { apiMock } = vi.hoisted(() => ({ apiMock: vi.fn() }))

vi.mock('./api', () => ({
  api: apiMock,
  json: (method: string, body?: unknown) => ({ method, body: body === undefined ? undefined : JSON.stringify(body) }),
}))

import { contractApi } from './contract-api'

describe('contractApi', () => {
  it('uses the configured client with an API-prefix-free literal path', async () => {
    apiMock.mockResolvedValue({ categories: [], locations: [] })

    await contractApi('/api/v1/market/options', 'get')

    expect(apiMock).toHaveBeenCalledWith('/market/options', { method: 'GET' })
  })
})

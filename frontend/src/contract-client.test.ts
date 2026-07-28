import { describe, expect, it } from 'vitest'
import { ApiError } from './api'
import { contractData } from './contract-client'

describe('contractData', () => {
  it('returns typed data from generated SDK responses', () => {
    expect(contractData({ data: { items: [] }, error: undefined, response: new Response(null, { status: 200 }) })).toEqual({ items: [] })
  })

  it('preserves the structured API error contract', () => {
    expect(() => contractData({
      data: undefined,
      error: { code: 'TEAM_FULL', message: '车队已满', field_errors: {}, request_id: 'req-1' },
      response: new Response(null, { status: 409 }),
    })).toThrowError(ApiError)
  })
})

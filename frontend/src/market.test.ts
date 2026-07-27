import { describe, expect, it } from 'vitest'
import type { MarketTransaction } from './types'
import { buildMarketListingRequest, formatPrice, marketTransactionActions } from './market'

function transaction(status: MarketTransaction['status']): MarketTransaction {
  return {
    id: 1,
    listing: { id: 2, title: '显示器', price_cents: 5050 },
    seller: { id: 10, nickname: '卖家' },
    buyer: { id: 20, nickname: '买家' },
    status,
    message: '',
    reserved_until: null,
    buyer_confirmed_at: null,
    seller_confirmed_at: null,
    completed_at: null,
    dispute: null,
    created_at: '',
    updated_at: '',
  }
}

describe('market presentation policy', () => {
  it('formats integer cents without binary floating-point input', () => {
    expect(formatPrice(0)).toBe('0')
    expect(formatPrice(5050)).toBe('50.50')
    expect(formatPrice(10000)).toBe('100')
  })

  it('builds a listing request with only API contract fields', () => {
    const request = buildMarketListingRequest({
      category_id: 2,
      location_id: 5,
      title: '二手显示器',
      description: '功能正常',
      price_yuan: 399.99,
      condition: 'excellent',
      negotiable: true,
      purchased_at: '',
      body: '',
      attachments: [{ id: 8 }],
    }, [8])

    expect(request).toEqual({
      category_id: 2,
      location_id: 5,
      title: '二手显示器',
      description: '功能正常',
      price_cents: 39999,
      condition: 'excellent',
      negotiable: true,
      purchased_at: null,
      attachment_ids: [8],
    })
    expect(request).not.toHaveProperty('body')
    expect(request).not.toHaveProperty('attachments')
    expect(request).not.toHaveProperty('price_yuan')
  })

  it('only exposes status actions to the correct transaction party', () => {
    expect(marketTransactionActions(transaction('requested'), 10)).toEqual(['accept', 'reject'])
    expect(marketTransactionActions(transaction('requested'), 20)).toEqual(['cancel'])
    expect(marketTransactionActions(transaction('reserved'), 20)).toEqual(['confirm', 'cancel', 'dispute'])
    expect(marketTransactionActions(transaction('completed'), 10)).toEqual(['review'])
    expect(marketTransactionActions(transaction('disputed'), 10)).toEqual([])
    expect(marketTransactionActions(transaction('reserved'), 999)).toEqual([])
  })

  it('hides confirm for the party that already confirmed', () => {
    const buyerConfirmed = { ...transaction('reserved'), buyer_confirmed_at: '2026-07-01T00:00:00Z' }
    expect(marketTransactionActions(buyerConfirmed, 20)).toEqual(['cancel', 'dispute'])
    // The seller has not confirmed yet, so it is still their turn.
    expect(marketTransactionActions(buyerConfirmed, 10)).toEqual(['confirm', 'cancel', 'dispute'])

    const sellerConfirmed = { ...transaction('reserved'), seller_confirmed_at: '2026-07-01T00:00:00Z' }
    expect(marketTransactionActions(sellerConfirmed, 10)).toEqual(['cancel', 'dispute'])
    expect(marketTransactionActions(sellerConfirmed, 20)).toEqual(['confirm', 'cancel', 'dispute'])
  })
})

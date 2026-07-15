import { describe, expect, it } from 'vitest'
import type { MarketTransaction } from './types'
import { formatPrice, marketTransactionActions } from './market'

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

  it('only exposes status actions to the correct transaction party', () => {
    expect(marketTransactionActions(transaction('requested'), 10)).toEqual(['accept', 'reject'])
    expect(marketTransactionActions(transaction('requested'), 20)).toEqual(['cancel'])
    expect(marketTransactionActions(transaction('reserved'), 20)).toEqual(['confirm', 'cancel', 'dispute'])
    expect(marketTransactionActions(transaction('completed'), 10)).toEqual(['review'])
    expect(marketTransactionActions(transaction('disputed'), 10)).toEqual([])
    expect(marketTransactionActions(transaction('reserved'), 999)).toEqual([])
  })
})

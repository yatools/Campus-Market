import type { MarketTransaction } from './types'

export type MarketAction = 'accept' | 'reject' | 'confirm' | 'cancel' | 'dispute' | 'review'

export function formatPrice(cents: number): string {
  return (cents / 100).toFixed(2).replace(/\.00$/, '')
}

export function marketTransactionActions(transaction: MarketTransaction, userId: number): MarketAction[] {
  const isBuyer = transaction.buyer.id === userId
  const isSeller = transaction.seller.id === userId
  if (!isBuyer && !isSeller) return []
  if (transaction.status === 'requested') return isSeller ? ['accept', 'reject'] : ['cancel']
  if (transaction.status === 'reserved') return ['confirm', 'cancel', 'dispute']
  if (transaction.status === 'completed') return ['review']
  return []
}

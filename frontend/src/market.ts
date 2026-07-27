import type { MarketListingCreate, MarketTransaction } from './types'

export type MarketAction = 'accept' | 'reject' | 'confirm' | 'cancel' | 'dispute' | 'review'

export function formatPrice(cents: number): string {
  return (cents / 100).toFixed(2).replace(/\.00$/, '')
}

export function buildMarketListingRequest(form: Record<string, unknown>, attachmentIds: number[]): MarketListingCreate {
  return {
    category_id: Number(form.category_id),
    location_id: Number(form.location_id),
    title: String(form.title || ''),
    description: String(form.description || ''),
    price_cents: Math.round(Number(form.price_yuan) * 100),
    condition: form.condition as MarketListingCreate['condition'],
    negotiable: Boolean(form.negotiable),
    purchased_at: form.purchased_at ? String(form.purchased_at) : null,
    attachment_ids: attachmentIds,
  }
}

export function marketTransactionActions(transaction: MarketTransaction, userId: number): MarketAction[] {
  const isBuyer = transaction.buyer.id === userId
  const isSeller = transaction.seller.id === userId
  if (!isBuyer && !isSeller) return []
  if (transaction.status === 'requested') return isSeller ? ['accept', 'reject'] : ['cancel']
  if (transaction.status === 'reserved') {
    // Hide "confirm" for the side that has already confirmed, so the button reflects
    // whether it is still this user's turn (backend confirm is idempotent regardless).
    const alreadyConfirmed = (isBuyer && Boolean(transaction.buyer_confirmed_at)) || (isSeller && Boolean(transaction.seller_confirmed_at))
    return alreadyConfirmed ? ['cancel', 'dispute'] : ['confirm', 'cancel', 'dispute']
  }
  if (transaction.status === 'completed') return ['review']
  return []
}

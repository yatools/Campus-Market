import { describe, expect, it } from 'vitest'
import { buildExploreRequest, type ExploreRequestKind } from './requests'

const dirtyForm = {
  category_id: 99,
  location_id: 98,
  category: '其他',
  title: '测试标题',
  body: '这是一段满足长度要求的测试正文',
  description: '测试描述',
  tags: ' 校园, 求助 ,,',
  bounty_xp: '10',
  draft: true,
  offering_id: '12',
  rating: '5',
  capacity: '20',
  location: '图书馆',
  starts_at: '2026-07-28T10:00',
  ends_at: '',
  kind: 'lost',
  item_name: '校园卡',
  happened_at: '',
  price_yuan: 100,
  attachments: [{ id: 7 }],
}

describe('explore API request builders', () => {
  it.each<[ExploreRequestKind, string[]]>([
    ['question.create', ['attachment_ids', 'body', 'bounty_xp', 'category', 'tags', 'title']],
    ['question.update', ['attachment_ids', 'body', 'category', 'tags', 'title']],
    ['article.create', ['attachment_ids', 'body', 'category', 'draft', 'title']],
    ['article.update', ['attachment_ids', 'body', 'category', 'title']],
    ['review.create', ['attachment_ids', 'body', 'offering_id', 'rating', 'tags']],
    ['activity.create', ['attachment_ids', 'body', 'capacity', 'category', 'ends_at', 'location', 'starts_at', 'title']],
    ['activity.update', ['attachment_ids', 'body', 'capacity', 'category', 'ends_at', 'location', 'starts_at', 'title']],
    ['lost.create', ['attachment_ids', 'description', 'happened_at', 'item_name', 'kind', 'location']],
    ['lost.update', ['attachment_ids', 'description', 'happened_at', 'item_name', 'location']],
    ['observe.create', ['attachment_ids', 'body', 'title']],
  ])('keeps only contract fields for %s', (kind, keys) => {
    const request = buildExploreRequest(kind, dirtyForm, [7])
    expect(Object.keys(request).sort()).toEqual(keys)
    expect(request).not.toHaveProperty('attachments')
    expect(request).not.toHaveProperty('price_yuan')
  })

  it('normalizes numeric, tag and optional datetime fields', () => {
    expect(buildExploreRequest('question.create', dirtyForm, [7])).toMatchObject({
      bounty_xp: 10,
      tags: ['校园', '求助'],
    })
    expect(buildExploreRequest('activity.create', dirtyForm, [7])).toMatchObject({
      capacity: 20,
      starts_at: new Date(dirtyForm.starts_at).toISOString(),
      ends_at: null,
    })
  })
})

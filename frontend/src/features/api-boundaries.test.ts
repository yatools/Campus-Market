import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { ApiError } from '../api'
import { adminApi } from './admin/api'
import { exploreApi } from './explore/api'
import { meApi } from './me/api'
import { teamsApi } from './teams/api'

const fetchMock = vi.fn()

function response(body: unknown, status = 200): Response {
  return new Response(JSON.stringify(body), {
    status,
    headers: { 'Content-Type': 'application/json' },
  })
}

describe('generated SDK feature boundaries', () => {
  beforeEach(() => {
    fetchMock.mockReset()
    vi.stubGlobal('fetch', fetchMock)
  })

  afterEach(() => vi.unstubAllGlobals())

  it('maps Explore contract DTOs into an explicit page view model', async () => {
    fetchMock.mockResolvedValue(response({
      items: [{
        id: 7,
        title: 'Typed question',
        body: 'Body',
        category: 'campus',
        tags: ['typed'],
        bounty_xp: 5,
        accepted_answer_id: null,
        author: 'Tester',
        answer_count: 0,
        mine: false,
        status: 'published',
        created_at: '2026-07-28T00:00:00Z',
        updated_at: '2026-07-28T00:00:00Z',
        attachments: [],
      }],
      page: 1,
      page_size: 50,
      total: 1,
    }))

    const page = await exploreApi.list('questions', 1)

    expect(page.items[0]).toMatchObject({ id: 7, title: 'Typed question', claims: [], unmasked: false })
    expect((fetchMock.mock.calls[0][0] as Request).url).toContain('/api/v1/questions?page=1&page_size=50')
  })

  it('normalizes optional team DTO fields for the page model', async () => {
    fetchMock.mockResolvedValue(response({
      items: [{
        id: 3,
        game: 'Game',
        game_id: 1,
        mode: 'Ranked',
        rank_requirement: '',
        capacity: 5,
        member_count: 1,
        members: [],
        owner: { id: 1, nickname: 'Owner', credit: 900, verified: true },
        rating_tags: {},
        voice_name: '',
        voice_link: '',
        notes: '',
        newbie_level: '',
        vibe: '',
        reminder_channels: [],
        my_reminder_channels: [],
        recurrence: 'once',
        reminder_minutes: 30,
        post_departure_retention_minutes: 120,
        status: 'active',
        joined: false,
        mine: false,
      }],
      page: 1,
      page_size: 50,
      total: 1,
    }))

    const page = await teamsApi.list()

    expect(page.items[0]).toMatchObject({ completion_rate: null, next_run: null })
  })

  it('passes account filters through operation parameters', async () => {
    fetchMock.mockResolvedValue(response({ items: [], page: 2, page_size: 20, total: 0 }))

    await meApi.content({ page: 2, type: 'handbook', status: 'draft' })

    expect((fetchMock.mock.calls[0][0] as Request).url).toContain('/api/v1/me/content?page=2&page_size=20&type=handbook&status=draft')
  })

  it('converts contract errors into the shared application error model', async () => {
    fetchMock.mockResolvedValue(response({
      code: 'CONFLICT',
      message: 'Concurrent update',
      field_errors: {},
      request_id: 'request-1',
    }, 409))

    let failure: unknown
    try {
      await adminApi.health()
    } catch (error: unknown) {
      failure = error
    }
    expect(failure).toBeInstanceOf(ApiError)
    expect(failure).toMatchObject({
      status: 409,
      code: 'CONFLICT',
      requestId: 'request-1',
    })
  })
})

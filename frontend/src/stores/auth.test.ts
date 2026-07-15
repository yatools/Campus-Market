import { createPinia, setActivePinia } from 'pinia'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { useAuthStore } from './auth'

const apiMock = vi.fn()
vi.mock('../api', () => ({
  api: (...args: unknown[]) => apiMock(...args),
  json: (method: string, body?: unknown) => ({ method, body: body === undefined ? undefined : JSON.stringify(body) }),
}))

const user = {
  id: 7,
  email: 'admin@test.edu.cn',
  nickname: '管理员',
  alias: '梧桐#000007',
  campus_identity: 'staff' as const,
  role: 'admin' as const,
  status: 'active' as const,
  credit: 900,
  xp: 10,
  avatar_url: null,
  dm_stranger_off: false,
  hide_online: false,
  unread_notifications: 0,
}

class EventSourceStub {
  static instances: EventSourceStub[] = []
  listeners = new Map<string, (event: MessageEvent) => void>()
  closed = false
  constructor(public url: string, public options: EventSourceInit) { EventSourceStub.instances.push(this) }
  addEventListener(name: string, listener: EventListener) { this.listeners.set(name, listener as (event: MessageEvent) => void) }
  close() { this.closed = true }
}

describe('auth store', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
    EventSourceStub.instances = []
    vi.stubGlobal('EventSource', EventSourceStub)
    apiMock.mockReset()
  })

  afterEach(() => vi.unstubAllGlobals())

  it('loads rules and the current user and synchronizes unread events', async () => {
    apiMock.mockImplementation(async (path: string) => path === '/credit-rules'
      ? { max_score: 1000, initial_score: 800, values: { 'threshold.team_create': 650 }, rules: [] }
      : user)
    const auth = useAuthStore()
    await auth.load()
    expect(auth.user).toEqual(user)
    expect(auth.isAdmin).toBe(true)
    expect(auth.canModerate).toBe(true)
    expect(auth.creditRule('threshold.team_create')).toBe(650)
    const stream = EventSourceStub.instances[0]
    expect(stream.url).toBe('/api/v1/notifications/stream')
    stream.listeners.get('unread')?.(new MessageEvent('unread', { data: '{"count":4}' }))
    expect(auth.user?.unread_notifications).toBe(4)
    stream.listeners.get('unread')?.(new MessageEvent('unread', { data: 'invalid' }))
    expect(auth.user?.unread_notifications).toBe(4)
  })

  it('falls back safely when rules and session loading fail', async () => {
    apiMock.mockRejectedValue(new Error('offline'))
    const auth = useAuthStore()
    await auth.load()
    expect(auth.user).toBeNull()
    expect(auth.loading).toBe(false)
    expect(auth.creditRule('threshold.team_create')).toBe(600)
    expect(EventSourceStub.instances).toHaveLength(0)
  })

  it('logs in, logs out and opens the requested authentication mode', async () => {
    apiMock.mockResolvedValueOnce({ user }).mockResolvedValueOnce({})
    const auth = useAuthStore()
    await auth.login(user.email, 'password')
    expect(auth.user?.id).toBe(user.id)
    expect(auth.authOpen).toBe(false)
    expect(EventSourceStub.instances).toHaveLength(1)
    await auth.logout()
    expect(auth.user).toBeNull()
    expect(EventSourceStub.instances[0].closed).toBe(true)
    expect(auth.requireLogin()).toBe(false)
    expect(auth.authOpen).toBe(true)
    expect(auth.authMode).toBe('login')
    auth.openAuth('reset')
    expect(auth.authMode).toBe('reset')
  })
})

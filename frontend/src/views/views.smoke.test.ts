import { flushPromises, mount } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { createRouter, createMemoryHistory } from 'vue-router'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { useAuthStore } from '../stores/auth'
import AdminView from './AdminView.vue'
import DashboardView from './DashboardView.vue'
import ExploreView from './ExploreView.vue'
import HomeView from './HomeView.vue'
import MeView from './MeView.vue'
import MessagesView from './MessagesView.vue'
import SearchView from './SearchView.vue'
import TeamsView from './TeamsView.vue'

const apiMock = vi.fn()
vi.mock('../api', () => ({
  api: (...args: unknown[]) => apiMock(...args),
  json: (method: string, body?: unknown) => ({ method, body: body === undefined ? undefined : JSON.stringify(body) }),
  // The auth store registers a global 401 handler at setup time, so every factory mock
  // of this module must export it or useAuthStore() throws before any test runs.
  setUnauthorizedHandler: () => {},
  uploadImage: vi.fn(),
}))

const user = {
  id: 1,
  email: 'admin@test.edu.cn',
  nickname: '管理员',
  alias: '梧桐#000001',
  campus_identity: 'staff',
  role: 'admin',
  status: 'active',
  credit: 900,
  xp: 100,
  avatar_url: null,
  dm_stranger_off: false,
  hide_online: false,
  unread_notifications: 0,
  unread_messages: 0,
}

const creditRules = { max_score: 1000, initial_score: 800, values: {}, rules: [] }
const emptyPage = { items: [], page: 1, page_size: 20, total: 0 }
const healthySystem = {
  worker: { stale: false, last_seen_at: '2026-07-15T12:00:00Z', last_success_at: '2026-07-15T12:00:00Z', last_error: '' },
  object_storage: { ok: true },
  database: { size_bytes: 1024 ** 2 },
  disk: { ok: true, temp_free_bytes: 1024 ** 3 },
  email: { pending: 0, failed: 0 },
  backup: { status: '', object_key: '', finished_at: null },
}

class EventSourceStub {
  addEventListener() {}
  close() {}
}

async function mountAt(component: object, path: string, primeAuth = false) {
  const pinia = createPinia()
  setActivePinia(pinia)
  if (primeAuth) await useAuthStore().load()
  const router = createRouter({
    history: createMemoryHistory(),
    routes: [
      { path: '/', component: { template: '<div />' } },
      { path: '/treehole', component: { template: '<div />' } },
      { path: '/search', component: { template: '<div />' } },
      { path: '/teams/:id?', component: { template: '<div />' } },
      { path: '/explore/:section?', component: { template: '<div />' } },
      { path: '/messages/:id?', component: { template: '<div />' } },
      { path: '/me', component: { template: '<div />' } },
      { path: '/admin', component: { template: '<div />' } },
    ],
  })
  await router.push(path)
  await router.isReady()
  const wrapper = mount(component, { global: { plugins: [pinia, router], stubs: { teleport: true } } })
  await flushPromises()
  await flushPromises()
  return wrapper
}

describe('major views', () => {
  beforeEach(() => {
    vi.stubGlobal('EventSource', EventSourceStub)
    apiMock.mockReset()
    apiMock.mockImplementation(async (path: string) => {
      if (path === '/me') return user
      if (path === '/credit-rules' || path === '/admin/credit-rules') return creditRules
      if (path === '/admin/system-health') return healthySystem
      if (path === '/admin/overview' || path === '/admin/settings') return {}
      if (path === '/market/options') return { categories: [], locations: [], conditions: [] }
      if (path === '/team-games' || path === '/campus-services' || path.startsWith('/admin/market/categories') || path.startsWith('/admin/market/locations')) return { items: [] }
      if (path.startsWith('/feed?')) return { ...emptyPage, watermark: 'initial' }
      return emptyPage
    })
  })

  afterEach(() => vi.unstubAllGlobals())

  for (const [name, component, path] of [
    ['dashboard', DashboardView, '/'],
    ['treehole', HomeView, '/treehole'],
    ['market', ExploreView, '/explore/listings'],
    ['teams', TeamsView, '/teams'],
    ['messages', MessagesView, '/messages'],
    ['account', MeView, '/me'],
    ['admin', AdminView, '/admin'],
    ['search', SearchView, '/search?q='],
  ] as const) {
    it(`renders the ${name} view after its initial API load`, async () => {
      const wrapper = await mountAt(component, path)
      expect(wrapper.find('section').exists()).toBe(true)
      expect(wrapper.text()).not.toContain('加载失败')
      wrapper.unmount()
    })
  }

  it('toggles a conversation block and restores the composer', async () => {
    const conversation = { id: 9, context_type: 'direct', context_id: null, other_user: { id: 2, nickname: '对方' }, last_message: '你好', last_message_at: '2026-07-15T12:00:00Z', unread: 0, blocked_by_me: false }
    apiMock.mockImplementation(async (path: string) => {
      if (path === '/me') return user
      if (path === '/credit-rules') return creditRules
      if (path === '/conversations') return { ...emptyPage, items: [conversation] }
      if (path === '/conversations/9/messages') return emptyPage
      return { active: true }
    })
    vi.stubGlobal('confirm', vi.fn(() => true))
    const wrapper = await mountAt(MessagesView, '/messages/9')
    const action = () => wrapper.findAll('button').find((button) => ['拉黑', '取消拉黑'].includes(button.text()))
    expect(action()?.text()).toBe('拉黑')
    await action()?.trigger('click')
    await flushPromises()
    expect(apiMock).toHaveBeenCalledWith('/blocks/2', { method: 'PUT' })
    expect(action()?.text()).toBe('取消拉黑')
    expect(wrapper.find('textarea').attributes('disabled')).toBeDefined()
    await action()?.trigger('click')
    await flushPromises()
    expect(apiMock).toHaveBeenCalledWith('/blocks/2', { method: 'DELETE' })
    expect(action()?.text()).toBe('拉黑')
    expect(wrapper.find('textarea').attributes('disabled')).toBeUndefined()
    wrapper.unmount()
  })

  it('marks all private messages read without clearing system notifications', async () => {
    const currentUser = { ...user, unread_notifications: 3, unread_messages: 2 }
    const conversations = [
      { id: 9, context_type: 'direct', context_id: null, other_user: { id: 2, nickname: '甲' }, last_message: '已读', last_message_at: '2026-07-15T12:00:00Z', unread: 0, blocked_by_me: false },
      { id: 10, context_type: 'direct', context_id: null, other_user: { id: 3, nickname: '乙' }, last_message: '未读', last_message_at: '2026-07-15T12:01:00Z', unread: 2, blocked_by_me: false },
    ]
    apiMock.mockImplementation(async (path: string) => {
      if (path === '/me') return currentUser
      if (path === '/credit-rules') return creditRules
      if (path === '/conversations') return { ...emptyPage, items: conversations }
      if (path === '/conversations/9/messages') return emptyPage
      if (path === '/conversations/read-all') return { ok: true, marked_messages: 2, unread_messages: 0, unread_notifications: 1 }
      return emptyPage
    })
    const wrapper = await mountAt(MessagesView, '/messages/9')
    const readAll = wrapper.findAll('button').find((button) => button.text().includes('全部标为已读'))
    expect(readAll?.text()).toContain('2')
    await readAll?.trigger('click')
    await flushPromises()
    expect(apiMock).toHaveBeenCalledWith('/conversations/read-all', expect.objectContaining({ method: 'POST' }))
    expect(wrapper.text()).toContain('已将 2 条私信通知标为已读')
    expect(currentUser.unread_notifications).toBe(1)
    expect(currentUser.unread_messages).toBe(0)
    wrapper.unmount()
  })

  it('shows the observe publisher for qualified users and requires rule confirmation', async () => {
    const wrapper = await mountAt(ExploreView, '/explore/observe', true)
    const publish = wrapper.findAll('button').find((button) => button.text().includes('发布观察'))
    expect(publish?.attributes('disabled')).toBeUndefined()
    await publish?.trigger('click')
    const submit = wrapper.find('.modal-card form button[type="submit"], .modal-card form > button')
    expect(wrapper.text()).toContain('我已阅读并同意本区须知')
    expect(submit.attributes('disabled')).toBeDefined()
    await wrapper.find('.observe-confirm input').setValue(true)
    expect(wrapper.find('.modal-card form button[type="submit"], .modal-card form > button').attributes('disabled')).toBeUndefined()
    wrapper.unmount()
  })

  it('keeps the observe entry visible but disabled below the credit threshold', async () => {
    apiMock.mockImplementation(async (path: string) => {
      if (path === '/me') return { ...user, credit: 700 }
      if (path === '/credit-rules') return { ...creditRules, values: { 'threshold.observe_publish': 750 } }
      return emptyPage
    })
    const wrapper = await mountAt(ExploreView, '/explore/observe', true)
    const publish = wrapper.findAll('button').find((button) => button.text().includes('信用不足'))
    expect(publish?.attributes('disabled')).toBeDefined()
    expect(wrapper.text()).toContain('还差 50 信用分')
    wrapper.unmount()
  })

  it('keeps the observe entry available as a login prompt for guests', async () => {
    apiMock.mockImplementation(async (path: string) => {
      if (path === '/credit-rules') return { ...creditRules, values: { 'threshold.observe_publish': 750 } }
      if (path === '/me') throw new Error('AUTH_REQUIRED')
      return emptyPage
    })
    const wrapper = await mountAt(ExploreView, '/explore/observe', true)
    const publish = wrapper.findAll('button').find((button) => button.text().includes('登录后发布'))
    expect(publish?.attributes('disabled')).toBeUndefined()
    await publish?.trigger('click')
    expect(useAuthStore().authOpen).toBe(true)
    wrapper.unmount()
  })

  it('validates and reports audited team actions without duplicate or silent requests', async () => {
    const nextRun = {
      id: 10,
      starts_at: new Date(Date.now() + 5 * 60_000).toISOString(),
      expires_at: null,
      status: 'scheduled',
      my_status: 'joined',
      checked_in: false,
      excused: false,
      member_count: 2,
    }
    const team = {
      id: 1,
      game: '无畏契约',
      game_id: 1,
      mode: '排位',
      rank_requirement: '不限',
      capacity: 5,
      member_count: 2,
      members: [
        { id: 1, nickname: '车头', credit: 900 },
        { id: 2, nickname: '队友', credit: 880 },
      ],
      owner: { id: 1, nickname: '车头', credit: 900, verified: true },
      completion_rate: 100,
      rating_tags: {},
      voice_name: 'KOOK',
      voice_link: 'https://example.test/voice',
      notes: '准时发车',
      newbie_level: '欢迎新手',
      vibe: '友好',
      reminder_channels: ['in_app'],
      my_reminder_channels: ['calendar'],
      recurrence: 'once',
      reminder_minutes: 30,
      post_departure_retention_minutes: 120,
      status: 'active',
      joined: true,
      mine: true,
      next_run: nextRun,
    }
    const pastRun = {
      ...nextRun,
      id: 11,
      starts_at: new Date(Date.now() - 5 * 60_000).toISOString(),
    }
    apiMock.mockImplementation(async (path: string) => {
      if (path === '/me') return user
      if (path === '/credit-rules') return creditRules
      if (path === '/teams?page_size=50') return { ...emptyPage, items: [team] }
      if (path === '/team-games') return { items: [{ id: 1, name: '无畏契约', aliases: [], active: true }] }
      if (path === '/teams/1/runs?page_size=100') return { ...emptyPage, items: [pastRun] }
      if (path.endsWith('/check-in')) return { ok: true, credit_delta: 2 }
      return { ok: true }
    })
    const promptMock = vi.fn()
    vi.stubGlobal('prompt', promptMock)
    vi.stubGlobal('confirm', vi.fn(() => true))
    const wrapper = await mountAt(TeamsView, '/teams', true)
    const button = (label: string) => wrapper.findAll('button').find((item) => item.text().includes(label))

    await button('管理车队')?.trigger('click')
    await button('场次签到')?.trigger('click')
    await flushPromises()
    expect(apiMock).toHaveBeenCalledWith('/teams/1/runs/10/check-in', expect.objectContaining({ method: 'POST' }))
    expect(wrapper.text()).toContain('签到成功，信用 +2')

    await button('本次请假')?.trigger('click')
    await flushPromises()
    expect(apiMock).toHaveBeenCalledWith('/teams/1/runs/10/excuse', expect.objectContaining({ method: 'POST' }))
    expect(wrapper.text()).toContain('本场请假已记录')

    promptMock.mockReset()
    promptMock.mockReturnValueOnce('排位').mockReturnValueOnce('备注').mockReturnValueOnce('1')
    await button('编辑车队')?.trigger('click')
    expect(wrapper.text()).toContain('容量需为 2 到 99 之间的整数')

    promptMock.mockReset()
    promptMock.mockReturnValueOnce('排位').mockReturnValueOnce('备注').mockReturnValueOnce('4')
    await button('编辑车队')?.trigger('click')
    await flushPromises()
    expect(apiMock).toHaveBeenCalledWith('/teams/1', expect.objectContaining({ method: 'PATCH' }))

    promptMock.mockReset()
    promptMock.mockReturnValue('明晚八点')
    await button('新增场次')?.trigger('click')
    expect(wrapper.text()).toContain('时间格式无法识别')

    promptMock.mockReset()
    promptMock.mockReturnValue(new Date(Date.now() + 3 * 60 * 60_000).toISOString())
    await button('新增场次')?.trigger('click')
    await flushPromises()
    expect(apiMock).toHaveBeenCalledWith('/teams/1/runs', expect.objectContaining({ method: 'POST' }))

    promptMock.mockReset()
    promptMock.mockReturnValue('friendly, punctual')
    await button('评价 队友')?.trigger('click')
    await flushPromises()
    expect(apiMock).toHaveBeenCalledWith('/teams/1/runs/11/ratings', expect.objectContaining({ method: 'POST' }))
    expect(wrapper.text()).toContain('评价已记录')

    apiMock.mockRejectedValueOnce(new Error('评价接口失败'))
    await button('评价 队友')?.trigger('click')
    await flushPromises()
    expect(wrapper.text()).toContain('评价接口失败')
    wrapper.unmount()
  })

  it('opens a team from the dashboard ranking', async () => {
    apiMock.mockImplementation(async (path: string) => {
      if (path === '/hot') {
        return {
          ...emptyPage,
          items: [{ id: 1, type: 'team', title: 'Ranked team', score: 42, comments: 2, favorites: 3 }],
        }
      }
      if (path.startsWith('/feed?')) return { ...emptyPage, watermark: 'initial' }
      if (path.startsWith('/announcements')) return emptyPage
      return emptyPage
    })

    const wrapper = await mountAt(DashboardView, '/')
    const rankedTeam = wrapper.get('.sticky-note')
    expect(rankedTeam.text()).toContain('Ranked team')
    await rankedTeam.trigger('click')
    await flushPromises()
    expect(wrapper.vm.$route.fullPath).toBe('/teams')
    wrapper.unmount()
  })
})

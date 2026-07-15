import { flushPromises, mount } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { createRouter, createMemoryHistory } from 'vue-router'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
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
}

const creditRules = { max_score: 1000, initial_score: 800, values: {}, rules: [] }
const emptyPage = { items: [], page: 1, page_size: 20, total: 0 }

class EventSourceStub {
  addEventListener() {}
  close() {}
}

async function mountAt(component: object, path: string) {
  const pinia = createPinia()
  setActivePinia(pinia)
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
  const wrapper = mount(component, { global: { plugins: [pinia, router] } })
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
      if (path === '/admin/overview' || path === '/admin/settings' || path === '/admin/system-health') return {}
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
})

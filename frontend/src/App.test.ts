import { flushPromises, mount } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { createRouter, createMemoryHistory } from 'vue-router'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import App from './App.vue'

const apiMock = vi.fn()
vi.mock('./api', () => ({
  api: (...args: unknown[]) => apiMock(...args),
  json: (method: string, body?: unknown) => ({ method, body: body === undefined ? undefined : JSON.stringify(body) }),
}))

const user = { id: 1, email: 'admin@test.edu.cn', nickname: '管理员', alias: '梧桐#1', campus_identity: 'staff', role: 'admin', status: 'active', credit: 900, xp: 0, avatar_url: null, dm_stranger_off: false, hide_online: false, unread_notifications: 2 }

class EventSourceStub {
  addEventListener() {}
  close() {}
}

describe('application shell', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
    vi.stubGlobal('EventSource', EventSourceStub)
    sessionStorage.clear()
    apiMock.mockReset()
    apiMock.mockImplementation(async (path: string) => {
      if (path === '/credit-rules') return { max_score: 1000, initial_score: 800, values: { 'threshold.team_create': 600, 'threshold.listing_publish': 700, 'threshold.high_credit': 800 }, rules: [] }
      if (path === '/me') return user
      if (path.startsWith('/announcements')) return { items: [{ id: 2, title: '维护公告', body: '今晚维护', level: 'strong', audience: 'all', read: false, read_count: 0, published_at: new Date().toISOString() }] }
      if (path.startsWith('/teams')) return { items: [] }
      return {}
    })
  })

  afterEach(() => vi.unstubAllGlobals())

  it('loads navigation data and handles search, announcements, publishing and feedback', async () => {
    const router = createRouter({
      history: createMemoryHistory(),
      routes: [
        { path: '/', component: { template: '<p>home</p>' } },
        { path: '/search', component: { template: '<p>search</p>' } },
        { path: '/treehole', component: { template: '<p>treehole</p>' } },
        { path: '/explore/:section?', component: { template: '<p>explore</p>' } },
        { path: '/:pathMatch(.*)*', component: { template: '<p>page</p>' } },
      ],
    })
    await router.push('/')
    await router.isReady()
    const wrapper = mount(App, { global: { plugins: [router], stubs: { teleport: true } } })
    await flushPromises()
    expect(wrapper.text()).toContain('维护公告')
    expect(wrapper.text()).toContain('教职工')

    await wrapper.find('.close-notice').trigger('click')
    expect(wrapper.find('.notice-bar').exists()).toBe(false)

    await wrapper.find('.searchbox input').setValue('显示器')
    await wrapper.find('.searchbox').trigger('submit')
    await flushPromises()
    expect(router.currentRoute.value.fullPath).toBe('/search?q=%E6%98%BE%E7%A4%BA%E5%99%A8')

    await wrapper.find('.btn-post').trigger('click')
    expect(wrapper.find('.publish-menu').exists()).toBe(true)
    await wrapper.find('.publish-menu button').trigger('click')
    await flushPromises()
    expect(router.currentRoute.value.fullPath).toBe('/treehole?create=1')

    await wrapper.find('.feedback-link').trigger('click')
    const feedbackForm = wrapper.find('.modal-card form')
    await feedbackForm.find('input').setValue('新的建议')
    await feedbackForm.find('textarea').setValue('这是一个足够详细的功能建议内容。')
    await feedbackForm.trigger('submit')
    await flushPromises()
    expect(apiMock).toHaveBeenCalledWith('/feedback', expect.objectContaining({ method: 'POST' }))
    expect(wrapper.text()).toContain('反馈已提交')
    wrapper.unmount()
  })
})

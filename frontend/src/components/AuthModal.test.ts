import { flushPromises, mount } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import AuthModal from './AuthModal.vue'

const apiMock = vi.fn()
vi.mock('../api', () => ({
  api: (...args: unknown[]) => apiMock(...args),
  json: (method: string, body?: unknown) => ({ method, body: body === undefined ? undefined : JSON.stringify(body) }),
  // The auth store registers a global 401 handler at setup time, so every factory mock
  // of this module must export it or useAuthStore() throws before any test runs.
  setUnauthorizedHandler: () => {},
}))

const user = { id: 1, email: 'user@test.edu.cn', nickname: '同学', alias: '梧桐#1', campus_identity: 'student', role: 'user', status: 'active', credit: 800, xp: 0, avatar_url: null, dm_stranger_off: false, hide_online: false, unread_notifications: 0 }

class EventSourceStub {
  addEventListener() {}
  close() {}
}

describe('authentication modal', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
    vi.stubGlobal('EventSource', EventSourceStub)
    apiMock.mockReset()
    apiMock.mockImplementation(async (path: string) => {
      if (path === '/credit-rules') return { max_score: 1000, initial_score: 800, values: {}, rules: [] }
      if (path === '/me' || path === '/auth/login' || path === '/auth/register') return { ...user, ...(path.startsWith('/auth/') ? { user } : {}) }
      return {}
    })
  })

  afterEach(() => vi.unstubAllGlobals())

  it('submits login credentials through the auth store', async () => {
    const wrapper = mount(AuthModal, { global: { plugins: [createPinia()], stubs: { teleport: true } } })
    await wrapper.find('input[type="email"]').setValue(user.email)
    await wrapper.find('input[type="password"]').setValue('password')
    await wrapper.find('form').trigger('submit')
    await flushPromises()
    expect(apiMock).toHaveBeenCalledWith('/auth/login', expect.objectContaining({ method: 'POST' }))
    wrapper.unmount()
  })

  it('requests a registration code and registers a new account', async () => {
    const wrapper = mount(AuthModal, { props: { initialMode: 'register' }, global: { plugins: [createPinia()], stubs: { teleport: true } } })
    const inputs = wrapper.findAll('input')
    await inputs.find((input) => input.attributes('type') === 'email')!.setValue(user.email)
    await inputs.find((input) => input.attributes('pattern') === '\\d{6}')!.setValue('123456')
    await inputs.find((input) => input.attributes('maxlength') === '20')!.setValue('新同学')
    await inputs.find((input) => input.attributes('type') === 'password')!.setValue('password-long')
    await inputs.find((input) => input.attributes('type') === 'checkbox')!.setValue(true)
    await wrapper.find('.auth-email-row-v4 button').trigger('click')
    await flushPromises()
    expect(wrapper.text()).toContain('验证码已发送')
    await wrapper.find('form').trigger('submit')
    await flushPromises()
    expect(apiMock).toHaveBeenCalledWith('/auth/register', expect.objectContaining({ method: 'POST' }))
    wrapper.unmount()
  })

  it('resets a password and returns to login mode', async () => {
    const wrapper = mount(AuthModal, { props: { initialMode: 'reset' }, global: { plugins: [createPinia()], stubs: { teleport: true } } })
    await wrapper.find('input[type="email"]').setValue(user.email)
    await wrapper.findAll('input').find((input) => input.attributes('pattern') === '\\d{6}')!.setValue('123456')
    await wrapper.find('input[type="password"]').setValue('new-password-long')
    await wrapper.find('.auth-email-row-v4 button').trigger('click')
    await wrapper.find('form').trigger('submit')
    await flushPromises()
    expect(apiMock).toHaveBeenCalledWith('/auth/reset-password', expect.objectContaining({ method: 'POST' }))
    expect(wrapper.text()).toContain('密码已重置')
    wrapper.unmount()
  })
})

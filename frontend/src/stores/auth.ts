import { defineStore } from 'pinia'
import { computed, ref } from 'vue'
import { api, json, setUnauthorizedHandler } from '../api'
import type { AuthMode, CreditRuleSet, User } from '../types'
import { appConfig } from '../config'

const defaultCreditRules: CreditRuleSet = {
  max_score: 1000,
  initial_score: 800,
  values: {
    'baseline.initial_credit': 800,
    'threshold.anonymous_post': 600,
    'threshold.team_create': 600,
    'threshold.course_review': 600,
    'threshold.listing_publish': 700,
    'threshold.contact_publish': 700,
    'threshold.observe_publish': 750,
    'threshold.observe_unmask': 900,
    'threshold.high_credit': 800,
    'threshold.dm_unlimited': 850,
    'reward.team_check_in': 2,
    'reward.lost_claim': 5,
    'reward.feedback_accepted': 5,
    'penalty.team_late_leave': -20,
  },
  rules: [],
}

export const useAuthStore = defineStore('auth', () => {
  const user = ref<User | null>(null)
  const loading = ref(true)
  const authOpen = ref(false)
  const authMode = ref<AuthMode>('login')
  const creditRules = ref<CreditRuleSet>(defaultCreditRules)
  const isAdmin = computed(() => user.value?.role === 'admin')
  const canModerate = computed(() => ['moderator', 'admin'].includes(user.value?.role || ''))
  let notificationStream: EventSource | null = null

  function connectNotifications() {
    notificationStream?.close()
    notificationStream = null
    if (!user.value || typeof EventSource === 'undefined') return
    notificationStream = new EventSource(`${appConfig().api_prefix}/notifications/stream`, { withCredentials: true })
    notificationStream.addEventListener('unread', (event) => {
      try {
        const value = JSON.parse((event as MessageEvent).data) as { count?: number; messages?: number }
        if (user.value && typeof value.count === 'number') user.value.unread_notifications = value.count
        if (user.value && typeof value.messages === 'number') user.value.unread_messages = value.messages
      } catch { /* 下一次服务端事件会重新同步。 */ }
    })
  }

  async function load() {
    loading.value = true
    try { creditRules.value = await api<CreditRuleSet>('/credit-rules') }
    catch { creditRules.value = defaultCreditRules }
    try {
      user.value = await api<User>('/me')
      connectNotifications()
    } catch {
      user.value = null
      connectNotifications()
    } finally {
      loading.value = false
    }
  }

  async function login(email: string, password: string) {
    const result = await api<{ user: User }>('/auth/login', json('POST', { email, password }))
    user.value = result.user
    connectNotifications()
    authOpen.value = false
  }

  // Set while a logout request is in flight so the global 401 handler stays quiet: an
  // expired session makes POST /auth/logout return 401, and reacting to that by opening
  // the login modal is the exact opposite of what the user just asked for.
  let loggingOut = false

  async function logout() {
    loggingOut = true
    try {
      await api('/auth/logout', json('POST'))
    } catch {
      // An already-invalid session is still a successful logout from the user's point of
      // view; fall through to clearing local state either way.
    } finally {
      loggingOut = false
      clearSession()
    }
  }

  // Drop local session state and tear down the notification stream. Handlers that revoke
  // sessions server-side (change password, deactivate account) must call this rather than
  // assigning user.value = null, which would leave the EventSource open and reconnecting
  // every few seconds against an endpoint that now 401s.
  function clearSession() {
    user.value = null
    connectNotifications()
  }

  function requireLogin(): boolean {
    if (user.value) return true
    openAuth('login')
    return false
  }

  // Clear local session state and prompt re-login when any request 401s mid-session.
  // No-ops while logged out, so the initial anonymous GET /me does not trigger it.
  setUnauthorizedHandler(() => {
    if (!user.value || loggingOut) return
    clearSession()
    openAuth('login')
  })

  async function agreeObserveUnmask() {
    await api('/me/observe-unmask-agreement', json('POST'))
    if (user.value) user.value.observe_unmask_agreed = true
  }

  function openAuth(mode: AuthMode = 'login') {
    authMode.value = mode
    authOpen.value = true
  }

  function creditRule(key: string): number {
    return creditRules.value.values[key] ?? defaultCreditRules.values[key] ?? 0
  }

  function acknowledgeMessages(count: number) {
    if (!user.value || count <= 0) return
    const previous = user.value.unread_messages || 0
    const next = Math.max(0, previous - count)
    user.value.unread_messages = next
    user.value.unread_notifications = Math.max(0, (user.value.unread_notifications || 0) - (previous - next))
  }

  function setUnreadCounts(notifications: number, messages: number) {
    if (!user.value) return
    user.value.unread_notifications = Math.max(0, notifications)
    user.value.unread_messages = Math.max(0, messages)
  }

  return { user, loading, authOpen, authMode, creditRules, isAdmin, canModerate, load, login, logout, clearSession, requireLogin, openAuth, creditRule, acknowledgeMessages, setUnreadCounts, agreeObserveUnmask }
})

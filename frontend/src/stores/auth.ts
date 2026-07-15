import { defineStore } from 'pinia'
import { computed, ref } from 'vue'
import { api, json } from '../api'
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

  async function logout() {
    await api('/auth/logout', json('POST'))
    user.value = null
    connectNotifications()
  }

  function requireLogin(): boolean {
    if (user.value) return true
    openAuth('login')
    return false
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

  return { user, loading, authOpen, authMode, creditRules, isAdmin, canModerate, load, login, logout, requireLogin, openAuth, creditRule, acknowledgeMessages, setUnreadCounts }
})

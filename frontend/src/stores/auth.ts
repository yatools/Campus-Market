import { defineStore } from 'pinia'
import { computed, ref } from 'vue'
import { api, json } from '../api'
import type { User } from '../types'

export const useAuthStore = defineStore('auth', () => {
  const user = ref<User | null>(null)
  const loading = ref(true)
  const authOpen = ref(false)
  const isAdmin = computed(() => user.value?.role === 'admin')
  const canModerate = computed(() => ['moderator', 'admin'].includes(user.value?.role || ''))
  let notificationStream: EventSource | null = null

  function connectNotifications() {
    notificationStream?.close()
    notificationStream = null
    if (!user.value || typeof EventSource === 'undefined') return
    notificationStream = new EventSource('/api/v1/notifications/stream', { withCredentials: true })
    notificationStream.addEventListener('unread', (event) => {
      try {
        const value = JSON.parse((event as MessageEvent).data) as { count?: number }
        if (user.value && typeof value.count === 'number') user.value.unread_notifications = value.count
      } catch { /* 下一次服务端事件会重新同步。 */ }
    })
  }

  async function load() {
    loading.value = true
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
    authOpen.value = true
    return false
  }

  return { user, loading, authOpen, isAdmin, canModerate, load, login, logout, requireLogin }
})

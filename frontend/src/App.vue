<script setup lang="ts">
import { computed, onMounted, reactive, ref, watch } from 'vue'
import { RouterLink, RouterView, useRoute, useRouter } from 'vue-router'
import { api, json } from './api'
import AuthModal from './components/AuthModal.vue'
import BaseModal from './components/BaseModal.vue'
import { useAuthStore } from './stores/auth'
import type { Announcement, Page, Team } from './types'

const auth = useAuthStore()
const route = useRoute()
const router = useRouter()
const announcements = ref<Announcement[]>([])
const teams = ref<Team[]>([])
const drawerOpen = ref(false)
const publishOpen = ref(false)
const feedbackOpen = ref(false)
const searchText = ref('')
const dismissedNotice = ref<number | null>(null)
const feedback = reactive({ type: 'suggestion', title: '', body: '' })
const feedbackMessage = ref('')
const feedbackError = ref('')

const navGroups = [
  { label: '广场', items: [['🏠', '首页 · 热榜', '/'], ['🌳', '树洞 · 微墙', '/treehole']] },
  { label: '核心', items: [['🎮', '游戏车队', '/teams'], ['🛒', '二手集市', '/explore/listings'], ['🙋', '打听 · 求助', '/explore/questions'], ['📖', '生存手册', '/explore/handbook'], ['🎓', '课程评价', '/explore/courses']] },
  { label: '秩序', items: [['🔍', '文明观察台', '/explore/observe'], ['⚖️', '治理公示', '/explore/governance'], ['📢', '公告中心', '/explore/announcements']] },
  { label: '更多', items: [['🎪', '校园活动', '/explore/activities'], ['🧣', '失物招领', '/explore/lost'], ['✉️', '站内私信', '/messages'], ['🪪', '我的后台', '/me']] },
] as const

const publishEntries = [
  ['🌳', '树洞', '/treehole'], ['🎮', '游戏车队', '/teams'], ['🙋', '提问', '/explore/questions'],
  ['📖', '生存手册', '/explore/handbook'], ['🛒', '二手出售', '/explore/listings'],
  ['🎪', '校园活动', '/explore/activities'], ['🧣', '失物招领', '/explore/lost'], ['🔍', '文明观察', '/explore/observe'],
] as const

const isAdminRoute = computed(() => route.path.startsWith('/admin'))
const showRail = computed(() => !isAdminRoute.value)
const notice = computed(() => {
  const preferred = announcements.value.find((item) => !item.read && item.level === 'strong')
    || announcements.value.find((item) => !item.read)
    || announcements.value[0]
  return preferred && preferred.id !== dismissedNotice.value ? preferred : null
})
const tonightTeams = computed(() => {
  const now = new Date()
  return teams.value.filter((team) => {
    if (!team.next_run || team.next_run.status !== 'scheduled') return false
    const date = new Date(team.next_run.starts_at)
    return date.getFullYear() === now.getFullYear() && date.getMonth() === now.getMonth() && date.getDate() === now.getDate()
  }).sort((a, b) => new Date(a.next_run!.starts_at).getTime() - new Date(b.next_run!.starts_at).getTime()).slice(0, 4)
})

function identityLabel() {
  if (!auth.user) return ''
  return auth.user.campus_identity === 'student' ? '已认证学生' : auth.user.campus_identity === 'staff' ? '教职工' : '校友'
}

async function loadShell() {
  const [announcementResult, teamResult] = await Promise.allSettled([
    api<Page<Announcement>>('/announcements?page_size=5'),
    api<Page<Team>>('/teams?page_size=20'),
  ])
  if (announcementResult.status === 'fulfilled') announcements.value = announcementResult.value.items
  if (teamResult.status === 'fulfilled') teams.value = teamResult.value.items
  try {
    const saved = sessionStorage.getItem('dismissed-announcement')
    dismissedNotice.value = saved ? Number(saved) : null
  } catch { /* 浏览器禁用存储时仍可正常显示公告。 */ }
}

function search() {
  const q = searchText.value.trim()
  if (q) router.push({ path: '/search', query: { q } })
}

function openPublish() {
  if (auth.requireLogin()) publishOpen.value = !publishOpen.value
}

function choosePublish(path: string) {
  publishOpen.value = false
  router.push({ path, query: { create: '1' } })
}

function dismissAnnouncement() {
  if (!notice.value) return
  dismissedNotice.value = notice.value.id
  try { sessionStorage.setItem('dismissed-announcement', String(notice.value.id)) } catch { /* 无持久化权限时只在当前页面关闭。 */ }
}

function openFeedback() {
  if (auth.requireLogin()) {
    feedbackMessage.value = ''
    feedbackError.value = ''
    feedbackOpen.value = true
  }
}

async function sendFeedback() {
  feedbackMessage.value = ''
  feedbackError.value = ''
  try {
    await api('/feedback', json('POST', feedback))
    feedbackMessage.value = '反馈已提交，管理员处理后会通过站内通知回复。'
    Object.assign(feedback, { type: 'suggestion', title: '', body: '' })
  } catch (e) { feedbackError.value = e instanceof Error ? e.message : '反馈提交失败' }
}

watch(() => route.fullPath, () => {
  drawerOpen.value = false
  publishOpen.value = false
  if (route.path === '/search') searchText.value = String(route.query.q || '')
})

onMounted(async () => {
  await auth.load()
  await loadShell()
  if (route.path === '/search') searchText.value = String(route.query.q || '')
})
</script>

<template>
  <div class="app-shell">
    <header class="topbar">
      <RouterLink to="/" class="brand"><strong>梧桐墙</strong><span>WUTONG WALL · 校园社区</span></RouterLink>
      <form class="searchbox" role="search" @submit.prevent="search">
        <input v-model="searchText" type="search" aria-label="全站搜索" placeholder="搜帖子 / 二手 / 问答 / 课程…（匿名帖不进搜索）" />
        <button>搜索</button>
      </form>
      <div class="top-actions">
        <div class="publish-wrap">
          <button class="btn-post" @click="openPublish">✏️ 发帖</button>
          <div v-if="publishOpen" class="publish-menu">
            <button v-for="entry in publishEntries" :key="entry[2]" @click="choosePublish(entry[2])"><span>{{ entry[0] }}</span>{{ entry[1] }}</button>
          </div>
        </div>
        <button class="login-link feedback-link" @click="openFeedback">💡 反馈</button>
        <template v-if="auth.user">
          <RouterLink to="/me" class="userchip">
            <span class="avatar">{{ auth.user.nickname.slice(0, 1) }}</span>
            <span><b>{{ auth.user.nickname }}</b><small>{{ identityLabel() }} · 信用 {{ auth.user.credit }}</small></span>
          </RouterLink>
          <button class="login-link logout-link" @click="auth.logout">退出</button>
        </template>
        <template v-else>
          <button class="login-link" @click="auth.openAuth('login')">登录</button>
          <button class="register-link" @click="auth.openAuth('register')">注册</button>
        </template>
      </div>
    </header>

    <div v-if="notice" class="notice-bar">
      <span class="tag-notice">{{ notice.level === 'strong' ? '重要公告' : '公告' }}</span>
      <div class="ticker">📌 <b>{{ notice.title }}</b>：{{ notice.body }} <RouterLink to="/explore/announcements">查看全部 →</RouterLink></div>
      <button class="close-notice" aria-label="收起公告" @click="dismissAnnouncement">×</button>
    </div>

    <div class="site-layout" :class="{ 'without-rail': !showRail, 'admin-layout': isAdminRoute }">
      <nav v-if="!isAdminRoute" class="sidenav" aria-label="板块导航">
        <template v-for="group in navGroups" :key="group.label">
          <div class="group-label">{{ group.label }}</div>
          <RouterLink v-for="item in group.items" :key="item[2]" :to="item[2]" class="navitem" :class="{ exact: item[2] === '/' }">
            <span class="nav-ico">{{ item[0] }}</span><span>{{ item[1] }}</span>
            <i v-if="item[2] === '/messages' && auth.user?.unread_notifications">{{ auth.user.unread_notifications }}</i>
          </RouterLink>
        </template>
        <RouterLink v-if="auth.canModerate" to="/admin" class="navitem"><span class="nav-ico">🛠️</span><span>管理后台</span></RouterLink>
      </nav>

      <main class="main-content"><RouterView /></main>

      <aside v-if="showRail" class="rail">
        <RouterLink v-if="auth.user" to="/me" class="idcard">
          <div class="idcard-head"><span class="avatar">{{ auth.user.nickname.slice(0, 1) }}</span><span><b>{{ auth.user.nickname }}</b><small>{{ auth.user.email ? '校园邮箱已验证' : identityLabel() }}</small></span><span class="credit"><b>{{ auth.user.credit }}</b><small>信用分</small></span></div>
          <div class="id-tags"><span>{{ identityLabel() }}</span><span>{{ auth.user.role === 'user' ? '社区成员' : auth.user.role === 'admin' ? '管理员' : '审核员' }}</span><span v-if="auth.user.credit >= auth.creditRule('threshold.high_credit')">高信用用户</span></div>
          <div class="credit-bar"><i :style="{ width: `${Math.max(0, Math.min(100, auth.user.credit / auth.creditRules.max_score * 100))}%` }" /></div>
          <p>匿名/车队 ≥ {{ auth.creditRule('threshold.team_create') }} · 交易 ≥ {{ auth.creditRule('threshold.listing_publish') }}</p>
        </RouterLink>
        <button v-else class="idcard guest-card" @click="auth.openAuth('register')"><b>加入梧桐墙</b><span>使用校园邮箱验证身份，参与本校社区讨论。</span><em>注册校园账号 →</em></button>

        <section v-if="announcements.length" class="card rail-card">
          <h3>📌 侧栏公告 <RouterLink to="/explore/announcements">→</RouterLink></h3>
          <RouterLink v-for="item in announcements.slice(0, 4)" :key="item.id" to="/explore/announcements" class="rail-link"><span>●</span>{{ item.title }}</RouterLink>
        </section>
        <section class="card rail-card">
          <h3>🚗 今晚发车 <RouterLink to="/teams">→</RouterLink></h3>
          <RouterLink v-for="team in tonightTeams" :key="team.id" :to="`/teams/${team.id}`" class="team-rail-link"><span>🎮 {{ team.game }}</span><b>{{ new Date(team.next_run!.starts_at).toLocaleTimeString('zh-CN', { hour: '2-digit', minute: '2-digit' }) }}</b></RouterLink>
          <p v-if="!tonightTeams.length" class="muted">今晚还没有待发车队。</p>
          <RouterLink to="/teams" class="button secondary small full-button">进入车队大厅 →</RouterLink>
        </section>
      </aside>
    </div>

    <nav v-if="!isAdminRoute" class="mobile-nav" aria-label="移动端导航">
      <RouterLink to="/"><span>🏠</span>首页</RouterLink><RouterLink to="/treehole"><span>🌳</span>树洞</RouterLink><RouterLink to="/teams"><span>🎮</span>车队</RouterLink><RouterLink to="/messages"><span>✉️</span>私信</RouterLink><button @click="drawerOpen = true"><span>☰</span>更多</button>
    </nav>
    <div v-if="drawerOpen" class="drawer-mask" @click="drawerOpen = false" />
    <nav class="mobile-drawer" :class="{ open: drawerOpen }" aria-label="全部板块">
      <header><b>梧桐墙 · 全部板块</b><button aria-label="关闭导航" @click="drawerOpen = false">×</button></header>
      <template v-for="group in navGroups" :key="group.label">
        <div class="group-label">{{ group.label }}</div>
        <RouterLink v-for="item in group.items" :key="item[2]" :to="item[2]" class="navitem"><span class="nav-ico">{{ item[0] }}</span>{{ item[1] }}</RouterLink>
      </template>
      <RouterLink v-if="auth.canModerate" to="/admin" class="navitem"><span class="nav-ico">🛠️</span>管理后台</RouterLink>
      <section v-if="announcements.length" class="card drawer-info"><h3>📌 公告</h3><RouterLink v-for="item in announcements.slice(0, 3)" :key="item.id" to="/explore/announcements" class="rail-link"><span>●</span>{{ item.title }}</RouterLink></section>
      <section class="card drawer-info"><h3>🚗 今晚发车</h3><RouterLink v-for="team in tonightTeams" :key="team.id" :to="`/teams/${team.id}`" class="team-rail-link"><span>{{ team.game }}</span><b>{{ new Date(team.next_run!.starts_at).toLocaleTimeString('zh-CN', { hour: '2-digit', minute: '2-digit' }) }}</b></RouterLink><p v-if="!tonightTeams.length" class="muted">今晚还没有待发车队。</p></section>
    </nav>

    <AuthModal v-if="auth.authOpen" :initial-mode="auth.authMode" />
    <BaseModal v-if="feedbackOpen" title="💡 反馈与建议" @close="feedbackOpen = false">
      <form class="form-stack" @submit.prevent="sendFeedback">
        <label>类型<select v-model="feedback.type"><option value="suggestion">功能建议</option><option value="bug">问题反馈</option><option value="complaint">社区投诉</option></select></label>
        <label>标题<input v-model.trim="feedback.title" required minlength="3" maxlength="160" /></label>
        <label>具体说明<textarea v-model.trim="feedback.body" rows="7" required minlength="10" maxlength="10000" /></label>
        <p v-if="feedbackMessage" class="notice success">{{ feedbackMessage }}</p>
        <p v-if="feedbackError" class="notice danger">{{ feedbackError }}</p>
        <button class="button primary">提交反馈</button>
      </form>
    </BaseModal>
  </div>
</template>

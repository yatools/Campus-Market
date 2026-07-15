<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, onMounted, ref } from 'vue'
import { useRouter } from 'vue-router'
import { api } from '../api'
import AttachmentGrid from '../components/AttachmentGrid.vue'
import RichText from '../components/RichText.vue'
import { useAuthStore } from '../stores/auth'
import type { Announcement, FeedItem, HotItem, Page } from '../types'
import { formatPrice } from '../market'

const router = useRouter(), auth = useAuthStore()
const hot = ref<HotItem[]>([])
const feed = ref<FeedItem[]>([])
const loading = ref(true)
const error = ref('')
const rankType = ref('all')
const watermark = ref('')
const newCount = ref(0)
const feedTop = ref<HTMLElement | null>(null)
const importantAnnouncement = ref<Announcement | null>(null)
let pollTimer: number | undefined
const rankTabs = [
  ['all', '全站热榜'], ['handbook', '本周精华'], ['fresh', '新生必看'], ['team', '游戏发车榜'],
  ['listing', '交易热度榜'], ['question', '问答高价值榜'], ['course_review', '课评新增榜'], ['observe', '文明观察榜'],
] as const
const visibleHot = computed(() => {
  if (rankType.value === 'all') return hot.value.slice(0, 8)
  if (rankType.value === 'fresh') return hot.value.filter((item) => item.type === 'handbook' && /新生|入学|报到|选课/.test(item.title)).slice(0, 8)
  return hot.value.filter((item) => item.type === rankType.value).slice(0, 8)
})

const typeInfo: Record<string, { icon: string; name: string; color: string }> = {
  post: { icon: '🌳', name: '树洞', color: 'gray' }, team: { icon: '🎮', name: '游戏车队', color: 'blue' },
  question: { icon: '🙋', name: '打听求助', color: 'red' }, handbook: { icon: '📖', name: '生存手册', color: 'green' },
  course_review: { icon: '🎓', name: '课程评价', color: 'yellow' }, listing: { icon: '🛒', name: '二手集市', color: 'yellow' },
  activity: { icon: '🎪', name: '校园活动', color: 'blue' }, lost_item: { icon: '🧣', name: '失物招领', color: 'red' },
  observe: { icon: '🔍', name: '文明观察', color: 'green' },
}

function info(type: string) { return typeInfo[type] || { icon: '📌', name: type, color: 'gray' } }
function target(type: string) { return ({ post: '/treehole', team: '/teams', question: '/explore/questions', listing: '/explore/listings', activity: '/explore/activities' } as Record<string, string>)[type] || '/' }
function hasReadAnnouncement(id: number) {
  try { return localStorage.getItem(`announcement-read:${id}`) === '1' } catch { return false }
}

async function load(initial = false) {
  if (initial) loading.value = true
  error.value = ''
  try {
    const [rank, latest, announcements] = await Promise.all([
      api<Page<HotItem>>('/hot'),
      api<Page<FeedItem> & { watermark: string }>('/feed?page=1&page_size=20'),
      api<Page<Announcement>>('/announcements?page_size=10'),
    ])
    hot.value = rank.items
    feed.value = latest.items
    watermark.value = latest.watermark
    newCount.value = 0
    if (initial) {
      importantAnnouncement.value = announcements.items.find((item) => item.level === 'strong' && !item.read && !hasReadAnnouncement(item.id)) || null
      try {
        const dismissed = sessionStorage.getItem('announcement-popup-dismissed')
        if (importantAnnouncement.value && dismissed === String(importantAnnouncement.value.id)) importantAnnouncement.value = null
      } catch { /* 无存储权限时仍可显示。 */ }
    }
  } catch (e) { error.value = e instanceof Error ? e.message : '首页加载失败' }
  finally { loading.value = false }
}

async function pollChanges() {
  if (!watermark.value || document.hidden) return
  try {
    const result = await api<{ count: number; watermark: string }>(`/feed/changes?after=${encodeURIComponent(watermark.value)}`)
    newCount.value = result.count
  } catch { /* 增量检查失败不影响已加载内容。 */ }
}

async function showUpdates() {
  await load()
  await nextTick()
  feedTop.value?.scrollIntoView({ behavior: 'smooth', block: 'start' })
}

function dismissImportant() {
  if (!importantAnnouncement.value) return
  try { sessionStorage.setItem('announcement-popup-dismissed', String(importantAnnouncement.value.id)) } catch { /* 仅当前渲染周期隐藏。 */ }
  importantAnnouncement.value = null
}

async function markImportantRead() {
  const item = importantAnnouncement.value
  if (!item) return
  if (auth.user) await api(`/announcements/${item.id}/read`, { method: 'PUT' })
  try { localStorage.setItem(`announcement-read:${item.id}`, '1') } catch { /* 服务端记录仍然有效。 */ }
  importantAnnouncement.value = null
}

onMounted(async () => {
  await load(true)
  pollTimer = window.setInterval(pollChanges, 30_000)
})
onBeforeUnmount(() => { if (pollTimer) window.clearInterval(pollTimer) })
</script>

<template>
  <section class="dashboard-page">
    <div class="board-frame">
      <div class="board-title"><h1>今日公告栏</h1><span title="热度综合点赞、收藏、回复与时间衰减计算">热度公式说明 ⓘ</span></div>
      <div class="rank-tabs"><button v-for="tab in rankTabs" :key="tab[0]" :class="{ active: rankType === tab[0] }" @click="rankType = tab[0]">{{ tab[1] }}</button></div>
      <p v-if="loading" class="board-empty">正在整理今天的校园热榜…</p>
      <div v-else-if="visibleHot.length" class="sticky-grid">
        <button v-for="(item, index) in visibleHot" :key="item.id" class="sticky-note" @click="router.push(target(item.type))"><span class="rank-number">{{ String(index + 1).padStart(2, '0') }}</span><strong>{{ item.title }}</strong><small>{{ info(item.type).name }} · 🔥 {{ item.score }} · 💬 {{ item.comments }} · ⭐ {{ item.favorites }}</small></button>
      </div>
      <p v-else class="board-empty">这个榜单还没有内容，去发布第一条吧。</p>
    </div>

    <p v-if="error" class="notice danger">{{ error }}</p>
    <header ref="feedTop" class="page-head feed-heading"><h2>最新动态</h2><p>全站各板块实时流 · 匿名帖不进入搜索引擎收录</p></header>
    <button v-if="newCount" class="new-feed-button" @click="showUpdates">查看 {{ newCount }} 个新的或更新的动态</button>
    <div class="feed-list">
      <article v-for="item in feed" :key="`${item.type}-${item.id}-${item.updated_at}`" class="post feed-card" :class="`feed-${item.type}`">
        <div class="p-head"><span class="p-avatar">{{ item.author?.slice(0, 1) || info(item.type).icon }}</span><span class="p-name">{{ item.author || info(item.type).name }}</span><span class="tag" :class="info(item.type).color">{{ info(item.type).icon }} {{ info(item.type).name }}</span><span class="p-time">{{ new Date(item.updated_at).toLocaleString() }}<small v-if="item.updated_at !== item.created_at"> · 有更新</small></span></div>
        <div v-if="item.title" class="p-title">{{ item.title }}</div><div class="p-body"><RichText :content="item.body" /></div>
        <AttachmentGrid :content="item.body" :attachments="item.attachments" />
        <div class="p-foot"><span v-if="item.type === 'team'">🚗 {{ item.meta.game }}</span><span v-if="item.type === 'listing'">¥{{ formatPrice(Number(item.meta.price_cents || 0)) }}{{ item.meta.negotiable ? ' · 可议价' : '' }}</span><span>👍 {{ item.likes }}</span><span>💬 {{ item.comments }}</span><span>⭐ {{ item.favorites }}</span><RouterLink :to="item.route">查看详情 →</RouterLink></div>
      </article>
      <p v-if="!loading && !feed.length" class="empty-state">校园里还很安静，来写下第一条动态。</p>
    </div>

    <Teleport to="body">
      <div v-if="importantAnnouncement" class="modal-backdrop announcement-popup" @click.self="dismissImportant">
        <section class="modal-card important-announcement" role="dialog" aria-modal="true" aria-label="重要公告">
          <button class="announcement-close" aria-label="暂时关闭" @click="dismissImportant">×</button>
          <h2>📢 重要公告</h2><p class="announcement-sub">重要公告仅弹窗一次，确认已读后不再打扰</p>
          <RichText :content="`**《${importantAnnouncement.title}》** ${importantAnnouncement.body}`" />
          <div class="announcement-actions"><button class="button secondary" @click="router.push('/explore/announcements'); dismissImportant()">查看全文</button><button class="button primary" @click="markImportantRead">我已阅读 ✓</button></div>
        </section>
      </div>
    </Teleport>
  </section>
</template>

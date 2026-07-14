<script setup lang="ts">
import { computed, onMounted, reactive, ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { api, json } from '../api'
import AttachmentGrid from '../components/AttachmentGrid.vue'
import BaseModal from '../components/BaseModal.vue'
import CommentThread from '../components/CommentThread.vue'
import RichText from '../components/RichText.vue'
import RichEditor from '../components/RichEditor.vue'
import { useAuthStore } from '../stores/auth'
import type { Attachment, Page, Post } from '../types'

const auth = useAuthStore()
const route = useRoute(), router = useRouter()
const posts = ref<Post[]>([])
const page = ref(1), total = ref(0)
const loading = ref(true)
const error = ref('')
const composer = ref(false)
const commentsOpen = ref<number | null>(null)
const reportTarget = ref<Post | null>(null)
const report = reactive({ reason: '不友善或辱骂', detail: '' })
const form = reactive({ title: '', body: '', identity_mode: 'anonymous', visibility: 'forever', allow_comments: true, attachments: [] as Attachment[] })
const careVisible = computed(() => /不想活|自杀|自伤|撑不下|结束生命|活着没意思/.test(form.body))

async function load(reset = false) {
  if (reset) page.value = 1
  loading.value = true
  error.value = ''
  try {
    const feed = await api<Page<Post>>(`/posts?page=${page.value}&page_size=30`)
    posts.value = feed.items
    total.value = feed.total
  } catch (e) {
    error.value = e instanceof Error ? e.message : '页面加载失败'
  } finally {
    loading.value = false
  }
}

async function openComposer() {
  if (auth.requireLogin()) {
    composer.value = true
  }
}

async function publish() {
  if (!auth.requireLogin()) return
  try {
    await api('/posts', json('POST', {
      title: form.title,
      body: form.body,
      identity_mode: form.identity_mode,
      visibility: form.visibility,
      allow_comments: form.allow_comments,
      attachment_ids: form.attachments.map((x) => x.id),
    }))
    Object.assign(form, { title: '', body: '', identity_mode: 'anonymous', visibility: 'forever', allow_comments: true, attachments: [] })
    composer.value = false
    await load()
  } catch (e) {
    error.value = e instanceof Error ? e.message : '发布失败'
  }
}

async function toggleLike(post: Post) {
  if (!auth.requireLogin()) return
  const result = post.liked
    ? await api<{ active: boolean; count: number }>(`/entities/${post.id}/reactions/like`, { method: 'DELETE' })
    : await api<{ active: boolean; count: number }>(`/entities/${post.id}/reactions/like`, { method: 'PUT' })
  post.liked = result.active
  post.likes = result.count
}

async function toggleFavorite(post: Post) {
  if (!auth.requireLogin()) return
  await api(`/entities/${post.id}/favorite`, { method: post.favorited ? 'DELETE' : 'PUT' })
  post.favorited = !post.favorited
  post.favorites += post.favorited ? 1 : -1
}

async function sendReport() {
  if (!reportTarget.value) return
  await api(`/entities/${reportTarget.value.id}/reports`, json('POST', report))
  reportTarget.value = null
  report.detail = ''
}

async function remove(post: Post) {
  if (!confirm('确定删除这条内容吗？有回帖时将保留讨论结构。')) return
  await api(`/entities/${post.id}`, { method: 'DELETE' })
  await load()
}
async function edit(post: Post) {
  const title = prompt('修改标题：', post.title); if (title === null) return
  const body = prompt('修改正文：', post.body); if (body === null) return
  await api(`/posts/${post.id}`, json('PATCH', { title, body }))
  await load()
}

async function consumeCreate() {
  if (route.query.create !== '1') return
  await openComposer()
  const query = { ...route.query }
  delete query.create
  await router.replace({ path: route.path, query })
}

watch(() => route.query.create, consumeCreate)
onMounted(async () => { await load(); await consumeCreate() })
</script>

<template>
  <section class="treehole-page-v4">
      <header class="page-head"><h2>🌳 树洞 · 微墙</h2><p>可以任意发牢骚 · 匿名帖默认不被搜索引擎收录 · 全部内容经敏感词与辱骂检测</p></header>
      <section class="card treehole-composer"><h3>写点什么…</h3>
        <button v-if="!composer" class="composer-prompt" @click="openComposer"><span class="post-avatar">{{ auth.user?.nickname.slice(0, 1) || '匿' }}</span><span>今天有什么想说的？</span><b>发布</b></button>
        <form v-else class="form-stack" @submit.prevent="publish">
          <label>标题（可选）<input v-model.trim="form.title" maxlength="120" placeholder="一句话概括，也可以留空" /></label>
          <div class="editor-field"><span class="editor-field-label">正文</span><RichEditor v-model="form.body" v-model:attachments="form.attachments" aria-label="树洞正文" placeholder="今天有什么想说的？" :max-length="10000" /></div>
          <div v-if="careVisible" class="carebox-v4"><b>🫂 我们注意到你可能正经历艰难的时刻</b><p>你并不孤单。可以联系校心理健康中心、辅导员，或拨打全国统一心理援助热线 <strong class="mono">12356</strong>（24 小时）。</p></div>
          <div class="composer-options">
            <label>身份<select v-model="form.identity_mode"><option value="anonymous">完全匿名</option><option value="alias">匿名昵称（固定马甲）</option><option value="nickname">实名昵称</option></select></label>
            <label>可见期<select v-model="form.visibility"><option value="forever">永久</option><option value="7d">7 天后隐藏</option><option value="24h">24 小时后隐藏</option></select></label>
            <label class="check"><input v-model="form.allow_comments" type="checkbox" /> 接收回帖</label>
          </div>
          <div class="composer-actions"><p>ⓘ 后台保留账号与发帖记录用于治理，前台不会公开真实身份。</p><button type="button" class="button secondary small" @click="composer = false">收起</button><button class="button primary small">发布</button></div>
        </form>
      </section>
      <p v-if="error" class="notice danger">{{ error }}</p>
      <div class="treehole-feed-v4">
        <p v-if="loading" class="empty-state">正在连接校园社区…</p>
        <p v-else-if="!posts.length" class="empty-state">这里还很安静，来写下第一句话。</p>
        <article v-for="post in posts" :key="post.id" class="post">
          <div class="p-head"><span class="p-avatar">{{ post.author.slice(0, 1) }}</span><span class="p-name">{{ post.author }}</span><span v-if="post.identity_mode === 'anonymous'" class="tag gray">完全匿名</span><span v-else-if="post.identity_mode === 'alias'" class="tag gray">匿名昵称</span><span v-if="post.expires_at" class="tag yellow">限时可见</span><span v-if="!post.allow_comments" class="tag gray">已关评</span><span v-if="post.status !== 'published'" class="tag red">{{ post.status }}</span><span class="p-time">{{ new Date(post.created_at).toLocaleString() }}</span></div>
          <div v-if="post.title" class="p-title">{{ post.title }}</div><div class="p-body"><RichText :content="post.body" /></div>
          <AttachmentGrid :content="post.body" :attachments="post.attachments" />
          <div class="p-foot"><button @click="toggleLike(post)">{{ post.liked ? '👍 已赞' : '👍 赞' }} {{ post.likes }}</button><button v-if="post.allow_comments" @click="commentsOpen = commentsOpen === post.id ? null : post.id">💬 回帖 {{ post.comments }}</button><span v-else>评论已关闭</span><button @click="toggleFavorite(post)">{{ post.favorited ? '⭐ 已收藏' : '☆ 收藏' }} {{ post.favorites }}</button><button @click="auth.requireLogin() && (reportTarget = post)">🚩 举报</button><template v-if="post.mine"><button @click="edit(post)">编辑</button><button @click="remove(post)">删除</button></template></div>
          <CommentThread v-if="commentsOpen === post.id" :entity-id="post.id" allow-anonymous />
        </article>
      </div>
      <div v-if="total > 30" class="actions"><button :disabled="page === 1" @click="page--; load()">上一页</button><span>第 {{ page }} 页 · 共 {{ total }} 条</span><button :disabled="page * 30 >= total" @click="page++; load()">下一页</button></div>
    <BaseModal v-if="reportTarget" title="举报内容" @close="reportTarget = null">
      <form class="form-stack" @submit.prevent="sendReport"><label>原因<select v-model="report.reason"><option>不友善或辱骂</option><option>隐私泄露</option><option>欺诈或违禁交易</option><option>垃圾广告</option><option>其他</option></select></label><label>补充说明<textarea v-model="report.detail" rows="5" maxlength="2000" /></label><button class="button danger">提交审核</button></form>
    </BaseModal>
  </section>
</template>

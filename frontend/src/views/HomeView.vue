<script setup lang="ts">
import { onMounted, reactive, ref } from 'vue'
import { api, json, uploadImage } from '../api'
import BaseModal from '../components/BaseModal.vue'
import CommentThread from '../components/CommentThread.vue'
import RichText from '../components/RichText.vue'
import { useAuthStore } from '../stores/auth'
import type { Attachment, Page, Post } from '../types'

const auth = useAuthStore()
const posts = ref<Post[]>([])
const page = ref(1), total = ref(0)
const hot = ref<Array<{ id: number; title: string; type: string; score: number }>>([])
const loading = ref(true)
const error = ref('')
const search = ref('')
const composer = ref(false)
const commentsOpen = ref<number | null>(null)
const reportTarget = ref<Post | null>(null)
const report = reactive({ reason: '不友善或辱骂', detail: '' })
const form = reactive({ title: '', body: '', identity_mode: 'anonymous', visibility: 'forever', allow_comments: true, attachments: [] as Attachment[] })

async function load(reset = false) {
  if (reset) page.value = 1
  loading.value = true
  error.value = ''
  try {
    const [feed, rank] = await Promise.all([
      api<Page<Post>>(`/posts?page=${page.value}&page_size=30${search.value ? `&q=${encodeURIComponent(search.value)}` : ''}`),
      api<Page<{ id: number; title: string; type: string; score: number }>>('/hot'),
    ])
    posts.value = feed.items
    total.value = feed.total
    hot.value = rank.items
  } catch (e) {
    error.value = e instanceof Error ? e.message : '页面加载失败'
  } finally {
    loading.value = false
  }
}

function openComposer() {
  if (auth.requireLogin()) composer.value = true
}

async function addImages(event: Event) {
  const input = event.target as HTMLInputElement
  const selected = [...(input.files || [])].slice(0, 9 - form.attachments.length)
  for (const file of selected) form.attachments.push(await uploadImage(file))
  input.value = ''
}

async function publish() {
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

onMounted(load)
</script>

<template>
  <div class="page-layout">
    <section>
      <header class="page-head with-action"><div><h1>树洞广场</h1><p>匿名不是免责，认真表达也值得被认真回应。</p></div><button class="button primary" @click="openComposer">写点什么</button></header>
      <form class="card compact row" role="search" @submit.prevent="load(true)"><input v-model.trim="search" placeholder="搜索公开帖子（匿名内容不会进入搜索）" /><button class="button secondary small">搜索</button></form>
      <p v-if="error" class="notice danger">{{ error }}</p>
      <div class="stack" style="margin-top:14px">
        <p v-if="loading" class="empty">正在连接校园社区…</p>
        <p v-else-if="!posts.length" class="empty">这里还很安静，来写下第一句话。</p>
        <article v-for="post in posts" :key="post.id" class="card">
          <div class="card-head"><div class="row"><span class="avatar">{{ post.author.slice(0, 1) }}</span><div><strong>{{ post.author }}</strong><div class="meta"><span>{{ new Date(post.created_at).toLocaleString() }}</span><span v-if="post.expires_at" class="badge yellow">限时可见</span><span v-if="post.status !== 'published'" class="badge red">{{ post.status }}</span></div></div></div><div v-if="post.mine" class="actions"><button class="text-button" @click="edit(post)">编辑</button><button class="text-button" @click="remove(post)">删除</button></div></div>
          <h2 v-if="post.title">{{ post.title }}</h2><RichText :content="post.body" />
          <div v-if="post.attachments.length" class="media-grid"><a v-for="image in post.attachments" :key="image.id" :href="image.url" target="_blank"><img :src="image.thumbnail_url" alt="帖子图片" loading="lazy" /></a></div>
          <div class="actions"><button @click="toggleLike(post)">{{ post.liked ? '👍 已赞' : '👍 赞' }} {{ post.likes }}</button><button v-if="post.allow_comments" @click="commentsOpen = commentsOpen === post.id ? null : post.id">💬 回帖 {{ post.comments }}</button><span v-else class="muted">回帖已关闭</span><button @click="toggleFavorite(post)">{{ post.favorited ? '⭐ 已收藏' : '☆ 收藏' }} {{ post.favorites }}</button><button @click="auth.requireLogin() && (reportTarget = post)">举报</button><span class="muted">浏览 {{ post.views }}</span></div>
          <CommentThread v-if="commentsOpen === post.id" :entity-id="post.id" allow-anonymous />
        </article>
      </div>
      <div v-if="total > 30" class="actions"><button :disabled="page === 1" @click="page--; load()">上一页</button><span>第 {{ page }} 页 · 共 {{ total }} 条</span><button :disabled="page * 30 >= total" @click="page++; load()">下一页</button></div>
    </section>
    <aside class="sidebar">
      <div class="card"><h3>本周热议</h3><div class="rank-list"><div v-for="(item, index) in hot.slice(0, 8)" :key="item.id" class="rank-item"><span>{{ String(index + 1).padStart(2, '0') }}</span><div><b>{{ item.title }}</b><small>{{ item.type }} · 热度 {{ item.score }}</small></div></div><p v-if="!hot.length" class="muted">暂无热榜数据</p></div></div>
      <div class="card compact"><h3>社区边界</h3><p class="muted">观察台内容先审后发；商品只允许线下面交；遇到隐私泄露、欺诈或人身攻击，请使用举报入口。</p></div>
    </aside>
    <BaseModal v-if="composer" title="发布到树洞" wide @close="composer = false">
      <form class="form-stack" @submit.prevent="publish"><div class="form-grid"><label class="full">标题（可选）<input v-model.trim="form.title" maxlength="120" /></label><label>展示身份<select v-model="form.identity_mode"><option value="anonymous">匿名</option><option value="alias">固定马甲</option><option value="nickname">显示昵称</option></select></label><label>可见期<select v-model="form.visibility"><option value="forever">永久</option><option value="7d">7 天</option><option value="24h">24 小时</option></select></label><label class="full">正文<textarea v-model="form.body" rows="8" required maxlength="10000" placeholder="支持 Markdown，图片请使用下方上传。" /></label><label class="full">图片（JPG/PNG/WebP，每张不超过 8MB）<input type="file" accept="image/jpeg,image/png,image/webp" multiple @change="addImages" /></label></div><div v-if="form.attachments.length" class="media-grid"><img v-for="item in form.attachments" :key="item.id" :src="item.thumbnail_url" alt="待发布图片" /></div><label class="check"><input v-model="form.allow_comments" type="checkbox" /> 接收回帖</label><button class="button primary">发布</button></form>
    </BaseModal>
    <BaseModal v-if="reportTarget" title="举报内容" @close="reportTarget = null">
      <form class="form-stack" @submit.prevent="sendReport"><label>原因<select v-model="report.reason"><option>不友善或辱骂</option><option>隐私泄露</option><option>欺诈或违禁交易</option><option>垃圾广告</option><option>其他</option></select></label><label>补充说明<textarea v-model="report.detail" rows="5" maxlength="2000" /></label><button class="button danger">提交审核</button></form>
    </BaseModal>
  </div>
</template>

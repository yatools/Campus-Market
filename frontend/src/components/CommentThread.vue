<script setup lang="ts">
import { onMounted, reactive, ref } from 'vue'
import { api, json } from '../api'
import { useAuthStore } from '../stores/auth'
import type { Comment, Page } from '../types'
import RichText from './RichText.vue'

const props = defineProps<{ entityId: number; allowAnonymous?: boolean }>()
const auth = useAuthStore()
const rows = ref<Comment[]>([])
const loading = ref(true)
const error = ref('')
const form = reactive({ body: '', identity_mode: props.allowAnonymous ? 'anonymous' : 'nickname', parent_id: null as number | null })
const replyingTo = ref('')

async function load() {
  loading.value = true
  try {
    rows.value = (await api<Page<Comment>>(`/entities/${props.entityId}/comments?page_size=50`)).items
  } catch (e) {
    error.value = e instanceof Error ? e.message : '回帖加载失败'
  } finally {
    loading.value = false
  }
}

function reply(comment: Comment) {
  if (!auth.requireLogin()) return
  form.parent_id = comment.parent_id || comment.id
  replyingTo.value = comment.author
}

async function submit() {
  if (!auth.requireLogin() || !form.body.trim()) return
  error.value = ''
  try {
    await api(`/entities/${props.entityId}/comments`, json('POST', form))
    form.body = ''
    form.parent_id = null
    replyingTo.value = ''
    await load()
  } catch (e) {
    error.value = e instanceof Error ? e.message : '回帖失败'
  }
}

async function toggleLike(item: Comment) {
  if (!auth.requireLogin()) return
  const result = item.liked
    ? await api<{ active: boolean; count: number }>(`/entities/${item.id}/reactions/like`, { method: 'DELETE' })
    : await api<{ active: boolean; count: number }>(`/entities/${item.id}/reactions/like`, { method: 'PUT' })
  item.liked = result.active
  item.likes = result.count
}

onMounted(load)
</script>

<template>
  <section class="comments">
    <h4>回帖</h4>
    <p v-if="loading" class="muted">正在加载回帖…</p>
    <p v-else-if="!rows.length" class="empty">还没有回帖，来坐第一排。</p>
    <article v-for="comment in rows" :key="comment.id" class="comment">
      <div class="comment-meta"><strong>{{ comment.author }}</strong><time>{{ new Date(comment.created_at).toLocaleString() }}</time></div>
      <RichText :content="comment.body" />
      <div class="comment-actions"><button @click="toggleLike(comment)">{{ comment.liked ? '已赞' : '赞' }} {{ comment.likes }}</button><button @click="reply(comment)">回复</button></div>
      <article v-for="child in comment.replies" :key="child.id" class="comment child">
        <div class="comment-meta"><strong>{{ child.author }}</strong><time>{{ new Date(child.created_at).toLocaleString() }}</time></div>
        <RichText :content="child.body" />
        <div class="comment-actions"><button @click="toggleLike(child)">{{ child.liked ? '已赞' : '赞' }} {{ child.likes }}</button><button @click="reply(child)">回复</button></div>
      </article>
    </article>
    <form class="comment-form" @submit.prevent="submit">
      <span v-if="replyingTo" class="replying">回复 {{ replyingTo }} <button type="button" @click="form.parent_id = null; replyingTo = ''">取消</button></span>
      <textarea v-model="form.body" rows="3" maxlength="3000" placeholder="具体、友善地参与讨论…" />
      <div class="row between">
        <select v-if="allowAnonymous" v-model="form.identity_mode"><option value="nickname">显示昵称</option><option value="alias">固定马甲</option><option value="anonymous">匿名</option></select>
        <span v-else />
        <button class="button primary small">发布回帖</button>
      </div>
      <p v-if="error" class="notice danger">{{ error }}</p>
    </form>
  </section>
</template>


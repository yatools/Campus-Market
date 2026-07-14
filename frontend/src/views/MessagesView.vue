<script setup lang="ts">
import { computed, onMounted, ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { api, json } from '../api'
import { useAuthStore } from '../stores/auth'
import type { Page } from '../types'

const route = useRoute(), router = useRouter(), auth = useAuthStore()
const conversations = ref<any[]>([]), messages = ref<any[]>([])
const text = ref(''), loading = ref(true), sending = ref(false), error = ref('')
const activeId = computed(() => Number(route.params.id || conversations.value[0]?.id || 0))
const active = computed(() => conversations.value.find((x) => x.id === activeId.value))

async function loadConversations() {
  if (!auth.user) { loading.value = false; return }
  try {
    conversations.value = (await api<Page<any>>('/conversations')).items
    if (!route.params.id && conversations.value.length) await router.replace(`/messages/${conversations.value[0].id}`)
  } catch (e) { error.value = e instanceof Error ? e.message : '私信加载失败' }
  finally { loading.value = false }
}
async function loadMessages() {
  if (!activeId.value) { messages.value = []; return }
  try { messages.value = (await api<Page<any>>(`/conversations/${activeId.value}/messages`)).items }
  catch (e) { error.value = e instanceof Error ? e.message : '消息加载失败' }
}
async function send() {
  if (!text.value.trim()) return
  sending.value = true; error.value = ''
  try {
    await api(`/conversations/${activeId.value}/messages`, json('POST', { body: text.value }))
    text.value = ''
    await Promise.all([loadMessages(), loadConversations()])
  } catch (e) { error.value = e instanceof Error ? e.message : '发送失败' }
  finally { sending.value = false }
}
async function reportMessage(item: any) {
  const detail = prompt('请说明这条私信的问题（如骚扰、欺诈、隐私泄露）：', '')
  if (detail === null) return
  try {
    await api(`/entities/${item.id}/reports`, json('POST', { reason: '不当私信', detail }))
    error.value = '举报已提交给审核队列。'
  } catch (e) { error.value = e instanceof Error ? e.message : '举报失败' }
}
async function block() {
  if (!active.value?.other_user || !confirm(`确定拉黑 ${active.value.other_user.nickname}？`)) return
  await api(`/blocks/${active.value.other_user.id}`, { method: 'PUT' })
  error.value = '已拉黑，对方不能再向你发送私信。'
}
watch(activeId, loadMessages)
onMounted(async () => { await auth.load(); await loadConversations(); await loadMessages() })
</script>

<template>
  <section>
    <header class="page-head"><h1>✉️ 站内私信</h1><p>商品和车队会话保留上下文；可随时拉黑或举报不当消息。</p></header>
    <p v-if="!auth.loading && !auth.user" class="empty-state">请先登录查看私信。<button class="button primary" @click="auth.openAuth('login')">登录</button></p>
    <p v-else-if="loading" class="empty-state">正在加载会话…</p>
    <p v-if="error" class="notice" :class="error.startsWith('已') ? 'success' : 'danger'">{{ error }}</p>
    <div v-if="auth.user" class="message-layout">
      <aside class="card conversation-list"><h3>会话</h3><button v-for="item in conversations" :key="item.id" :class="{ active: item.id === activeId }" @click="router.push(`/messages/${item.id}`)"><span class="avatar">{{ item.other_user?.nickname?.slice(0,1) || '?' }}</span><span><b>{{ item.other_user?.nickname || '已注销用户' }}</b><small>{{ item.last_message }}</small></span><i v-if="item.unread">{{ item.unread }}</i></button><p v-if="!conversations.length" class="muted">暂无私信。可以从商品页联系卖家。</p></aside>
      <section class="card chat"><header v-if="active"><div><h3>{{ active.other_user?.nickname }}</h3><span class="muted">{{ active.context_type === 'listing' ? '商品会话' : active.context_type === 'team' ? '车队会话' : active.context_type === 'activity' ? '活动会话' : '私信' }}</span></div><button class="text-button" @click="block">拉黑</button></header><div class="message-feed"><p v-if="!messages.length" class="empty-state">选择会话开始交流。</p><div v-for="item in messages" :key="item.id" class="bubble" :class="{ mine: item.mine }"><p>{{ item.body }}</p><time>{{ new Date(item.created_at).toLocaleString() }}</time><button v-if="!item.mine" class="report-link" @click="reportMessage(item)">举报</button></div></div><form v-if="active" class="row" @submit.prevent="send"><textarea v-model="text" rows="2" maxlength="2000" placeholder="写消息…" :disabled="sending" /><button class="button primary" :disabled="sending">{{ sending ? '发送中…' : '发送' }}</button></form></section>
    </div>
  </section>
</template>

<style scoped>
.message-layout{display:grid;grid-template-columns:300px 1fr;gap:16px}.conversation-list{display:grid;gap:6px;align-content:start}.conversation-list>button{position:relative;display:grid;grid-template-columns:38px 1fr;gap:8px;padding:8px;border:0;border-radius:10px;text-align:left;background:transparent}.conversation-list>button.active{background:var(--green-soft)}.conversation-list b,.conversation-list small{display:block}.conversation-list small{max-width:190px;overflow:hidden;color:var(--muted);text-overflow:ellipsis;white-space:nowrap}.conversation-list i{position:absolute;right:7px;top:7px;min-width:18px;padding:2px;border-radius:99px;color:#fff;background:var(--red);font-size:10px;text-align:center}.chat{min-height:560px;display:grid;grid-template-rows:auto 1fr auto;gap:12px}.chat>header{display:flex;justify-content:space-between}.message-feed{display:flex;flex-direction:column;gap:8px;overflow-y:auto;max-height:55vh;padding:8px}.bubble{max-width:72%;align-self:flex-start;padding:9px 12px;border-radius:12px 12px 12px 3px;background:var(--paper-deep)}.bubble.mine{align-self:flex-end;border-radius:12px 12px 3px;background:var(--green-soft)}.bubble p{margin:0}.bubble time{display:inline-block;margin-top:3px;color:var(--muted);font-size:9px}.report-link{margin-left:8px;padding:0;border:0;color:var(--muted);background:transparent;font-size:9px;text-decoration:underline}@media(max-width:760px){.message-layout{grid-template-columns:1fr}.conversation-list{max-height:220px;overflow:auto}.chat{min-height:430px}}
</style>

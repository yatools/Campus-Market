<script setup lang="ts">
import { computed, onMounted, ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { api, json } from '../api'
import { useAuthStore } from '../stores/auth'
import type { ConversationMessagePage, ConversationSummary, DirectMessage, MessageReadAllResult, Page } from '../types'

const route = useRoute(), router = useRouter(), auth = useAuthStore()
const conversations = ref<ConversationSummary[]>([]), messages = ref<DirectMessage[]>([])
const text = ref(''), loading = ref(true), sending = ref(false), blocking = ref(false), markingAll = ref(false)
const notice = ref(''), noticeKind = ref<'success' | 'danger' | 'info'>('info')
const activeId = computed(() => Number(route.params.id || conversations.value[0]?.id || 0))
const active = computed(() => conversations.value.find((x) => x.id === activeId.value))
const unreadMessages = computed(() => Math.max(auth.user?.unread_messages || 0, conversations.value.reduce((sum, item) => sum + item.unread, 0)))

function showNotice(message: string, kind: 'success' | 'danger' | 'info') {
  notice.value = message
  noticeKind.value = kind
}

async function loadConversations() {
  if (!auth.user) { loading.value = false; return }
  try {
    conversations.value = (await api<Page<ConversationSummary>>('/conversations')).items
    if (!route.params.id && conversations.value.length) await router.replace(`/messages/${conversations.value[0].id}`)
  } catch (e) { showNotice(e instanceof Error ? e.message : '私信加载失败', 'danger') }
  finally { loading.value = false }
}
async function loadMessages() {
  if (!activeId.value) { messages.value = []; return }
  const conversation = active.value
  const unread = conversation?.unread || 0
  try {
    const response = await api<ConversationMessagePage>(`/conversations/${activeId.value}/messages`)
    messages.value = response.items
    if (conversation) conversation.unread = 0
    if (typeof response.unread_notifications === 'number' && typeof response.unread_messages === 'number') auth.setUnreadCounts(response.unread_notifications, response.unread_messages)
    else auth.acknowledgeMessages(unread)
  }
  catch (e) { showNotice(e instanceof Error ? e.message : '消息加载失败', 'danger') }
}
async function markAllRead() {
  markingAll.value = true
  notice.value = ''
  try {
    const result = await api<MessageReadAllResult>('/conversations/read-all', json('POST'))
    for (const conversation of conversations.value) conversation.unread = 0
    auth.setUnreadCounts(result.unread_notifications, result.unread_messages)
    showNotice(result.marked_messages > 0 ? `已将 ${result.marked_messages} 条私信通知标为已读。` : '私信已经全部读过了。', 'success')
  } catch (e) { showNotice(e instanceof Error ? e.message : '全部标为已读失败', 'danger') }
  finally { markingAll.value = false }
}
async function send() {
  if (!text.value.trim() || active.value?.blocked_by_me) return
  sending.value = true; notice.value = ''
  try {
    await api(`/conversations/${activeId.value}/messages`, json('POST', { body: text.value }))
    text.value = ''
    await Promise.all([loadMessages(), loadConversations()])
  } catch (e) { showNotice(e instanceof Error ? e.message : '发送失败', 'danger') }
  finally { sending.value = false }
}
async function reportMessage(item: DirectMessage) {
  const detail = prompt('请说明这条私信的问题（如骚扰、欺诈、隐私泄露）：', '')
  if (detail === null) return
  try {
    await api(`/entities/${item.id}/reports`, json('POST', { reason: '不当私信', detail }))
    showNotice('举报已提交给审核队列。', 'success')
  } catch (e) { showNotice(e instanceof Error ? e.message : '举报失败', 'danger') }
}
async function toggleBlock() {
  const conversation = active.value
  if (!conversation?.other_user) return
  const undo = conversation.blocked_by_me
  if (!confirm(undo ? `确定取消拉黑 ${conversation.other_user.nickname}？` : `确定拉黑 ${conversation.other_user.nickname}？拉黑后仍可查看历史消息。`)) return
  blocking.value = true
  notice.value = ''
  try {
    await api(`/blocks/${conversation.other_user.id}`, { method: undo ? 'DELETE' : 'PUT' })
    conversation.blocked_by_me = !undo
    showNotice(undo ? '已取消拉黑，现在可以继续发送消息。' : '已拉黑，发送已暂停；你可以随时取消拉黑。', 'success')
  } catch (e) { showNotice(e instanceof Error ? e.message : undo ? '取消拉黑失败' : '拉黑失败', 'danger') }
  finally { blocking.value = false }
}
watch(activeId, loadMessages)
onMounted(async () => { await auth.load(); await loadConversations(); await loadMessages() })
</script>

<template>
  <section>
    <header class="page-head v4-page-head-action"><div><h1>✉️ 站内私信</h1><p>商品和车队会话保留上下文；可随时拉黑或举报不当消息。</p></div><button v-if="auth.user" class="button secondary" :disabled="markingAll || unreadMessages === 0" @click="markAllRead">{{ markingAll ? '处理中…' : unreadMessages ? `全部标为已读（${unreadMessages}）` : '已全部阅读' }}</button></header>
    <p v-if="!auth.loading && !auth.user" class="empty-state">请先登录查看私信。<button class="button primary" @click="auth.openAuth('login')">登录</button></p>
    <p v-else-if="loading" class="empty-state">正在加载会话…</p>
    <p v-if="notice" class="notice" :class="noticeKind">{{ notice }}</p>
    <div v-if="auth.user" class="message-layout">
      <aside class="card conversation-list"><h3>会话</h3><button v-for="item in conversations" :key="item.id" :class="{ active: item.id === activeId }" @click="router.push(`/messages/${item.id}`)"><span class="avatar">{{ item.other_user?.nickname?.slice(0,1) || '?' }}</span><span><b>{{ item.other_user?.nickname || '已注销用户' }}</b><small>{{ item.last_message }}</small></span><i v-if="item.unread">{{ item.unread }}</i></button><p v-if="!conversations.length" class="muted">暂无私信。可以从商品页联系卖家。</p></aside>
      <section class="card chat"><header v-if="active"><div><h3>{{ active.other_user?.nickname }}</h3><span class="muted">{{ active.context_type === 'listing' ? '商品会话' : active.context_type === 'team' ? '车队会话' : active.context_type === 'activity' ? '活动会话' : '私信' }}</span></div><button class="text-button" :class="{ 'unblock-button': active.blocked_by_me }" :disabled="blocking" @click="toggleBlock">{{ blocking ? '处理中…' : active.blocked_by_me ? '取消拉黑' : '拉黑' }}</button></header><p v-if="active?.blocked_by_me" class="notice info blocked-notice">你已拉黑该用户，历史消息仍保留；取消拉黑后可继续发送。</p><div class="message-feed"><p v-if="!messages.length" class="empty-state">选择会话开始交流。</p><div v-for="item in messages" :key="item.id" class="bubble" :class="{ mine: item.mine }"><p>{{ item.body }}</p><time>{{ new Date(item.created_at).toLocaleString() }}</time><button v-if="!item.mine" class="report-link" @click="reportMessage(item)">举报</button></div></div><form v-if="active" class="row" @submit.prevent="send"><textarea v-model="text" rows="2" maxlength="2000" :placeholder="active.blocked_by_me ? '取消拉黑后可继续发送' : '写消息…'" :disabled="sending || active.blocked_by_me" /><button class="button primary" :disabled="sending || active.blocked_by_me || !text.trim()">{{ sending ? '发送中…' : '发送' }}</button></form></section>
    </div>
  </section>
</template>

<style scoped>
.message-layout{display:grid;grid-template-columns:300px 1fr;gap:16px}.conversation-list{display:grid;gap:6px;align-content:start}.conversation-list>button{position:relative;display:grid;grid-template-columns:38px 1fr;gap:8px;padding:8px;border:0;border-radius:10px;text-align:left;background:transparent}.conversation-list>button.active{background:var(--green-soft)}.conversation-list b,.conversation-list small{display:block}.conversation-list small{max-width:190px;overflow:hidden;color:var(--muted);text-overflow:ellipsis;white-space:nowrap}.conversation-list i{position:absolute;right:7px;top:7px;min-width:18px;padding:2px;border-radius:99px;color:#fff;background:var(--red);font-size:10px;text-align:center}.chat{min-height:560px;display:grid;grid-template-rows:auto auto 1fr auto;gap:12px}.chat>header{display:flex;justify-content:space-between}.blocked-notice{margin:0}.unblock-button{color:var(--green)}.message-feed{display:flex;flex-direction:column;gap:8px;overflow-y:auto;max-height:55vh;padding:8px}.bubble{max-width:72%;align-self:flex-start;padding:9px 12px;border-radius:12px 12px 12px 3px;background:var(--paper-deep)}.bubble.mine{align-self:flex-end;border-radius:12px 12px 3px;background:var(--green-soft)}.bubble p{margin:0}.bubble time{display:inline-block;margin-top:3px;color:var(--muted);font-size:9px}.report-link{margin-left:8px;padding:0;border:0;color:var(--muted);background:transparent;font-size:9px;text-decoration:underline}@media(max-width:760px){.message-layout{grid-template-columns:1fr}.conversation-list{max-height:220px;overflow:auto}.chat{min-height:430px}}
</style>

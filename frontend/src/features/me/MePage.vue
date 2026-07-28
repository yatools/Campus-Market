<script setup lang="ts">
import { onMounted, reactive, ref } from 'vue'
import { useAuthStore } from '../../stores/auth'
import type { ContentSummary, FavoriteSummary, Notification, Session, UserAppeal, UserReport } from '../../generated/sdk'
import { meApi } from './api'

const auth = useAuthStore()
const tab = ref('overview'), error = ref(''), success = ref('')
const loading = ref(true)
const content = ref<ContentSummary[]>([]), favorites = ref<FavoriteSummary[]>([]), notifications = ref<Notification[]>([]), sessions = ref<Session[]>([]), reports = ref<UserReport[]>([]), appeals = ref<UserAppeal[]>([])
const contentPage = ref(1), contentTotal = ref(0), contentType = ref(''), contentStatus = ref('')
const profile = reactive({ nickname: '', alias: '', dm_stranger_off: false, hide_online: false })
const password = reactive({ old_password: '', new_password: '' })
const email = reactive({ new_email: '', code: '' })
const deactivate = reactive({ password: '', confirmation: '' })

async function load() {
  loading.value = true
  try {
    await auth.load()
    if (!auth.user) return
    Object.assign(profile, { nickname: auth.user.nickname, alias: auth.user.alias, dm_stranger_off: auth.user.dm_stranger_off, hide_online: auth.user.hide_online })
    const overview = await meApi.overview()
    favorites.value = overview.favorites.items; notifications.value = overview.notifications.items; sessions.value = overview.sessions.items; reports.value = overview.reports.items; appeals.value = overview.appeals.items
    await loadContent()
  } finally {
    loading.value = false
  }
}
async function loadContent(reset = false) {
  if (reset) contentPage.value = 1
  const mine = await meApi.content({ page: contentPage.value, type: contentType.value, status: contentStatus.value })
  content.value = mine.items; contentTotal.value = mine.total
}
async function saveProfile() { await meApi.saveProfile({ nickname: profile.nickname, alias: profile.alias }, { dm_stranger_off: profile.dm_stranger_off, hide_online: profile.hide_online }); success.value = '资料与隐私设置已保存'; await auth.load() }
async function avatar(event: Event) { const file = (event.target as HTMLInputElement).files?.[0]; if (!file) return; const upload = await meApi.uploadAvatar(file); await meApi.setAvatar(upload.id); await auth.load() }
// clearSession(), not `auth.user = null`: the latter leaves the notification EventSource
// open, so the browser keeps reconnecting to a stream that now 401s on every attempt.
async function changePassword() { await meApi.changePassword(password); auth.clearSession(); Object.assign(password, { old_password: '', new_password: '' }); success.value = '密码已修改，所有设备需要重新登录。'; auth.openAuth('login') }
async function sendEmailCode() { await meApi.requestEmailCode(email.new_email); success.value = '验证码已发送到新校园邮箱。' }
async function changeEmail() { await meApi.changeEmail(email); success.value = '校园邮箱已更换'; await auth.load() }
async function remove(item: ContentSummary) { if (confirm('确定删除这条内容？')) { await meApi.removeContent(item.id); await load() } }
async function publishDraft(item: ContentSummary) { await meApi.publishDraft(item.id); success.value = '草稿已提交发布'; await load() }
async function read(item: Notification) { await meApi.readNotification(item.id); item.read_at = new Date().toISOString() }
async function revoke(item: Session) { await meApi.revokeSession(item.id); await load() }
async function deactivateNow() { if (!confirm('账号将立即退出，30 天后清除个人资料。确定继续？')) return; await meApi.deactivate(deactivate); auth.clearSession() }
function run(task: () => Promise<void>) { error.value = ''; success.value = ''; task().catch((e) => { error.value = e instanceof Error ? e.message : '操作失败' }) }
onMounted(() => run(load))
</script>

<template>
  <section>
    <header class="page-head"><h2>🪪 我的信用与身份</h2><p>信用分决定你在社区的权限边界 · 违规扣分，优质贡献加分</p></header>
    <p v-if="loading" class="empty-state">正在加载账户资料…</p>
    <p v-else-if="!auth.user" class="empty-state">登录后才能进入用户后台。<button class="button primary" @click="auth.openAuth('login')">校邮登录</button></p>
    <template v-else-if="auth.user">
      <p v-if="error" class="notice danger">{{ error }}</p><p v-if="success" class="notice success">{{ success }}</p>
      <nav class="section-tabs account-tabs-v4"><button v-for="item in [['overview','信用概览'],['profile','资料'],['content','我的内容'],['favorites','收藏'],['notifications','通知'],['security','安全'],['reports','举报申诉']]" :key="item[0]" :class="{ active: tab === item[0] }" @click="tab = item[0]">{{ item[1] }}</button></nav>
      <div v-if="tab === 'overview'" class="credit-overview-v4">
        <div class="card credit-identity-v4"><div class="credit-score-v4"><strong>{{ auth.user.credit }}</strong><span>当前信用分 / 满分 {{ auth.creditRules.max_score }}</span></div><div class="credit-copy-v4"><p>身份标签：<span class="tag green">{{ auth.user.campus_identity === 'student' ? '已认证学生' : auth.user.campus_identity === 'staff' ? '已认证教职工' : '已认证校友' }}</span><span v-if="auth.user.credit >= auth.creditRule('threshold.high_credit')" class="tag green">高信用用户</span><span class="tag gray">{{ auth.user.role === 'user' ? '社区成员' : auth.user.role === 'admin' ? '管理员' : '审核员' }}</span></p><p>经验值 <b class="mono">{{ auth.user.xp }}</b> · 未读通知 <b class="mono">{{ auth.user.unread_notifications || 0 }}</b></p></div></div>
        <div class="card"><h3>🔓 权限门槛</h3><div class="table-wrap"><table class="gov"><thead><tr><th>权限</th><th>要求</th><th>状态</th></tr></thead><tbody><tr><td>匿名发帖</td><td>信用 ≥ {{ auth.creditRule('threshold.anonymous_post') }}</td><td><span class="tag" :class="auth.user.credit >= auth.creditRule('threshold.anonymous_post') ? 'green' : 'yellow'">{{ auth.user.credit >= auth.creditRule('threshold.anonymous_post') ? '✓ 已解锁' : '未解锁' }}</span></td></tr><tr><td>发布交易帖</td><td>信用 ≥ {{ auth.creditRule('threshold.listing_publish') }} + 已认证</td><td><span class="tag" :class="auth.user.credit >= auth.creditRule('threshold.listing_publish') ? 'green' : 'yellow'">{{ auth.user.credit >= auth.creditRule('threshold.listing_publish') ? '✓ 已解锁' : '未解锁' }}</span></td></tr><tr><td>观察台发帖</td><td>信用 ≥ {{ auth.creditRule('threshold.observe_publish') }}</td><td><span class="tag" :class="auth.user.credit >= auth.creditRule('threshold.observe_publish') ? 'green' : 'yellow'">{{ auth.user.credit >= auth.creditRule('threshold.observe_publish') ? '✓ 已解锁' : '未解锁' }}</span></td></tr><tr><td>发布联系方式</td><td>信用 ≥ {{ auth.creditRule('threshold.contact_publish') }}</td><td><span class="tag" :class="auth.user.credit >= auth.creditRule('threshold.contact_publish') ? 'green' : 'yellow'">{{ auth.user.credit >= auth.creditRule('threshold.contact_publish') ? '✓ 已解锁' : '未解锁' }}</span></td></tr><tr><td>创建游戏车队</td><td>信用 ≥ {{ auth.creditRule('threshold.team_create') }}</td><td><span class="tag" :class="auth.user.credit >= auth.creditRule('threshold.team_create') ? 'green' : 'yellow'">{{ auth.user.credit >= auth.creditRule('threshold.team_create') ? '✓ 已解锁' : '未解锁' }}</span></td></tr><tr><td>评价课程</td><td>信用 ≥ {{ auth.creditRule('threshold.course_review') }} + 修读记录</td><td><span class="tag" :class="auth.user.credit >= auth.creditRule('threshold.course_review') ? 'green' : 'yellow'">{{ auth.user.credit >= auth.creditRule('threshold.course_review') ? '信用达标' : '未解锁' }}</span></td></tr><tr><td>私信不限量</td><td>信用 ≥ {{ auth.creditRule('threshold.dm_unlimited') }}</td><td><span class="tag" :class="auth.user.credit >= auth.creditRule('threshold.dm_unlimited') ? 'green' : 'yellow'">{{ auth.user.credit >= auth.creditRule('threshold.dm_unlimited') ? '✓ 已解锁' : `差 ${auth.creditRule('threshold.dm_unlimited') - auth.user.credit} 分` }}</span></td></tr></tbody></table></div></div>
        <form class="card privacy-card-v4" @submit.prevent="run(saveProfile)"><h3>✉️ 私信设置（克制原则）</h3><label><input v-model="profile.dm_stranger_off" type="checkbox" /> 关闭陌生人私信（商品与同队上下文仍可联系）</label><label><input v-model="profile.hide_online" type="checkbox" /> 隐藏在线状态</label><p class="muted">ⓘ 新用户存在每日私信上限；举报私信可一键附带聊天记录。</p><button class="btn primary sm">保存设置</button></form>
      </div>
      <form v-else-if="tab === 'profile'" class="card form-grid" @submit.prevent="run(saveProfile)"><h3 class="full">资料设置</h3><label>昵称<input v-model="profile.nickname" required minlength="2" maxlength="20" /></label><label>固定匿名昵称（马甲）<input v-model="profile.alias" required minlength="2" maxlength="20" /><small class="muted">选择“固定马甲”发帖时显示，修改后历史马甲内容也会同步更新。</small></label><label>头像<input type="file" accept="image/jpeg,image/png,image/webp" @change="(e) => run(() => avatar(e))" /></label><button class="button primary full">保存资料</button></form>
      <div v-else-if="tab === 'content'" class="card"><div class="card-head"><h3>我的内容</h3><form class="row" @submit.prevent="run(() => loadContent(true))"><select v-model="contentType"><option value="">全部类型</option><option v-for="value in ['post','comment','question','answer','handbook','course_review','team','listing','activity','lost_item','observe','feedback']" :key="value" :value="value">{{ value }}</option></select><select v-model="contentStatus"><option value="">全部状态</option><option v-for="value in ['draft','pending','published','hidden','deleted','expired']" :key="value" :value="value">{{ value }}</option></select><button class="button secondary small">筛选</button></form></div><div class="table-wrap"><table><thead><tr><th>类型</th><th>标题</th><th>状态</th><th>更新时间</th><th>操作</th></tr></thead><tbody><tr v-for="item in content" :key="item.id"><td><span class="badge">{{ item.type }}</span></td><td>{{ item.title }}</td><td>{{ item.status }}</td><td>{{ new Date(item.updated_at).toLocaleDateString() }}</td><td><button v-if="item.type === 'handbook' && item.status === 'draft'" class="text-button" @click="run(() => publishDraft(item))">发布草稿</button><button v-if="item.status !== 'deleted'" class="text-button" @click="run(() => remove(item))">删除</button></td></tr></tbody></table></div><p v-if="!content.length" class="empty-state">当前筛选下没有内容。</p><div v-if="contentTotal > 20" class="actions"><button :disabled="contentPage === 1" @click="contentPage--; run(loadContent)">上一页</button><span>第 {{ contentPage }} 页 · 共 {{ contentTotal }} 条</span><button :disabled="contentPage * 20 >= contentTotal" @click="contentPage++; run(loadContent)">下一页</button></div></div>
      <div v-else-if="tab === 'favorites'" class="stack"><article v-for="item in favorites" :key="item.id" class="card compact"><span class="badge">{{ item.type }}</span> <strong>{{ item.title }}</strong><span class="muted"> · {{ new Date(item.favorited_at).toLocaleString() }}</span></article><p v-if="!favorites.length" class="empty-state">还没有收藏。</p></div>
      <div v-else-if="tab === 'notifications'" class="stack"><article v-for="item in notifications" :key="item.id" class="card compact" :style="{ opacity: item.read_at ? .68 : 1 }"><div class="card-head"><div><strong>{{ item.title }}</strong><p>{{ item.body }}</p></div><button v-if="!item.read_at" class="button secondary small" @click="run(() => read(item))">标为已读</button></div></article><p v-if="!notifications.length" class="empty-state">暂无通知。</p></div>
      <div v-else-if="tab === 'security'" class="stack"><form class="card form-grid" @submit.prevent="run(changePassword)"><h3 class="full">修改密码</h3><label>原密码<input v-model="password.old_password" type="password" required /></label><label>新密码（至少 10 位）<input v-model="password.new_password" type="password" minlength="10" required /></label><button class="button primary full">修改并退出全部设备</button></form><form class="card form-grid" @submit.prevent="run(changeEmail)"><h3 class="full">更换校园邮箱</h3><label>新校园邮箱<input v-model="email.new_email" type="email" required /></label><label>验证码<input v-model="email.code" pattern="\d{6}" required /></label><button type="button" class="button secondary" @click="run(sendEmailCode)">发送验证码</button><button class="button primary">确认更换</button></form><div class="card"><h3>登录设备</h3><div v-for="item in sessions" :key="item.id" class="row between" style="padding:8px 0;border-bottom:1px solid var(--line)"><span><strong>{{ item.ip_address || '未知 IP' }}</strong><small class="muted"> {{ item.user_agent?.slice(0,70) }}</small></span><button v-if="!item.revoked" class="button secondary small" @click="run(() => revoke(item))">退出该设备</button><span v-else class="muted">已退出</span></div></div><form class="card form-grid" @submit.prevent="run(deactivateNow)"><h3 class="full" style="color:var(--red)">注销账号</h3><p class="muted full">账号立即停用，30 天后清除邮箱、昵称、头像和私信；公共讨论匿名化保留。</p><label>密码<input v-model="deactivate.password" type="password" required /></label><label>输入“注销我的账号”<input v-model="deactivate.confirmation" required /></label><button class="button danger full">注销账号</button></form></div>
      <div v-else-if="tab === 'reports'" class="stack"><div class="card"><h3>我提交的举报</h3><p v-for="item in reports" :key="item.id"><span class="badge yellow">{{ item.status }}</span> 内容 #{{ item.entity_id }} · {{ item.reason }}</p><p v-if="!reports.length" class="muted">暂无举报记录。</p></div><div class="card"><h3>我的申诉</h3><p v-for="item in appeals" :key="item.id"><span class="badge">{{ item.status }}</span> 处罚 #{{ item.penalty_id }} · {{ item.admin_note }}</p><p v-if="!appeals.length" class="muted">暂无申诉记录。</p></div></div>
    </template>
  </section>
</template>

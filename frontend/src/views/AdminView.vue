<script setup lang="ts">
import { computed, onMounted, reactive, ref } from 'vue'
import { api, json } from '../api'
import { useAuthStore } from '../stores/auth'
import { buildHealthCards, formatHealthTime } from '../systemHealth'
import type { AdminMarketOption, AdminSystemHealth, CampusService, CreditRuleSet, GameSubmission, Page, TeamGame } from '../types'

const auth = useAuthStore()
const overview = ref<Record<string, number>>({}), users = ref<any[]>([]), cases = ref<any[]>([]), appeals = ref<any[]>([]), feedback = ref<any[]>([]), backups = ref<any[]>([]), auditRows = ref<any[]>([])
const marketDisputes = ref<any[]>([]), systemHealth = ref<AdminSystemHealth | null>(null)
const systemHealthRefreshing = ref(false), systemHealthRefreshedAt = ref<Date | null>(null)
const gameSubmissions = ref<GameSubmission[]>([]), teamGames = ref<TeamGame[]>([])
const campusServices = ref<CampusService[]>([])
const marketCategories = ref<AdminMarketOption[]>([]), marketLocations = ref<AdminMarketOption[]>([])
const creditRules = ref<CreditRuleSet>({ max_score: 1000, initial_score: 800, values: {}, rules: [] })
const settings = reactive<Record<string, string>>({}), announcement = reactive({ title: '', body: '', level: 'normal', audience: 'all' })
const courseForm = reactive({ name: '', teacher: '', semester: '', section: '' })
const serviceForm = reactive({ name: '', category: '校园服务', manager_user_id: '' })
const marketOptionForm = reactive({ kind: 'categories' as 'categories' | 'locations', name: '', slug: '', sort_order: 0 })
const tab = ref('queue'), error = ref(''), success = ref('')
const siteSettingKeys = computed(() => Object.keys(settings).filter((key) => key !== 'anonymous_nickname_pool'))
const healthCards = computed(() => systemHealth.value ? buildHealthCards(systemHealth.value) : [])

async function load() {
  await auth.load()
  if (!auth.canModerate) return
  const tasks: Promise<any>[] = [
    api('/admin/overview'), api<Page<any>>('/admin/users?page_size=100'), api<Page<any>>('/admin/moderation-cases'),
    api<Page<any>>('/admin/appeals'), api<Page<any>>('/admin/feedback'), api<Page<any>>('/admin/market/disputes'),
  ]
  if (auth.isAdmin) tasks.push(api('/admin/settings'), api<Page<any>>('/admin/backups'), api<Page<any>>('/admin/audit-logs?page_size=100'), api<CreditRuleSet>('/admin/credit-rules'), api<AdminSystemHealth>('/admin/system-health'))
  const values = await Promise.all(tasks)
  overview.value = values[0]; users.value = values[1].items; cases.value = values[2].items; appeals.value = values[3].items; feedback.value = values[4].items; marketDisputes.value = values[5].items
  if (auth.isAdmin) {
    Object.assign(settings, values[6]); backups.value = values[7].items; auditRows.value = values[8].items; creditRules.value = values[9]; systemHealth.value = values[10]; systemHealthRefreshedAt.value = new Date()
    const [catalog, submissions, services, categories, locations] = await Promise.all([
      api<{ items: TeamGame[] }>('/team-games'), api<Page<GameSubmission>>('/admin/game-submissions'), api<{ items: CampusService[] }>('/campus-services'),
      api<{ items: AdminMarketOption[] }>('/admin/market/categories'), api<{ items: AdminMarketOption[] }>('/admin/market/locations'),
    ])
    teamGames.value = catalog.items; gameSubmissions.value = submissions.items; campusServices.value = services.items
    marketCategories.value = categories.items; marketLocations.value = locations.items
  }
}
async function refreshSystemHealth() {
  systemHealthRefreshing.value = true
  try {
    systemHealth.value = await api<AdminSystemHealth>('/admin/system-health')
    systemHealthRefreshedAt.value = new Date()
  } finally {
    systemHealthRefreshing.value = false
  }
}
function run(task: () => Promise<void>) { error.value = ''; success.value = ''; task().catch((e) => { error.value = e instanceof Error ? e.message : '操作失败' }) }
async function decide(item: any, decision: string) {
  const note = prompt('请输入审核依据或处理说明：', '')
  if (note === null) return
  let respondent_id: number | null = null
  if (item.entity_type === 'observe' && decision === 'approve') { const raw = prompt('指定回应方用户 ID（可留空）：', ''); respondent_id = raw ? Number(raw) : null }
  await api(`/admin/moderation-cases/${item.id}/decision`, json('POST', { decision, note, respondent_id })); success.value = '审核已完成并记录审计日志'; await load()
}
async function saveUser(item: any) {
  const reason = prompt('请输入本次修改原因：', '')
  if (!reason) return
  await api(`/admin/users/${item.id}`, json('PATCH', { role: item.role, campus_identity: item.campus_identity, status: item.status, credit: Number(item.credit), reason })); success.value = '用户状态已更新'; await load()
}
async function punish(item: any) {
  const violation_type = prompt('违规类型：', '')
  if (!violation_type) return
  const result = prompt('处理结果与说明：', '')
  if (!result) return
  const rule = prompt('依据的社区规则：', '')
  if (!rule) return
  // Number(null) is 0, so cancelling this prompt used to submit a valid "credit -0"
  // penalty: the backend accepts 0, and the record — notification, audit entry and public
  // listing included — could then only be undone through the appeals process.
  const rawDelta = prompt('信用分变化（0 到 -1000）：', '-50')
  if (rawDelta === null) return
  const credit_delta = Number(rawDelta)
  if (!Number.isInteger(credit_delta) || credit_delta > 0 || credit_delta < -1000) {
    error.value = '信用分变化需为 -1000 到 0 之间的整数'
    return
  }
  await api('/admin/penalties', json('POST', { user_id: item.id, violation_type, result, rule, credit_delta }))
  success.value = '处罚已记录、通知责任人并进入可申诉流程'; await load()
}
async function decideAppeal(item: any, status: string) { const note = prompt('处理说明：', ''); if (note === null) return; await api(`/admin/appeals/${item.id}/decision`, json('POST', { status, note })); await load() }
async function decideFeedback(item: any, status: string) { const admin_note = prompt('给提交者的说明：', ''); if (admin_note === null) return; await api(`/admin/feedback/${item.id}/decision`, json('POST', { status, admin_note, reward_xp: status === 'accepted' ? 20 : 0 })); await load() }
async function decideMarketDispute(item: any, decision: 'completed' | 'cancelled') { const note = prompt('请输入交易纠纷裁决说明：', ''); if (note === null || !note) return; await api(`/admin/market/disputes/${item.id}/decision`, json('POST', { decision, note })); success.value = '交易纠纷已裁决并写入审计日志'; await load() }
async function publishAnnouncement() { await api('/admin/announcements', json('POST', announcement)); Object.assign(announcement, { title: '', body: '', level: 'normal', audience: 'all' }); success.value = '公告已发布' }
async function createCourseOffering() { const course = await api<any>('/courses', json('POST', { name: courseForm.name, teacher: courseForm.teacher })); await api('/course-offerings', json('POST', { course_id: course.id, semester: courseForm.semester, section: courseForm.section })); Object.assign(courseForm, { name: '', teacher: '', semester: '', section: '' }); success.value = '课程与班次已加入目录' }
async function createCampusService() { await api('/admin/campus-services', json('POST', { name: serviceForm.name, category: serviceForm.category, manager_user_id: serviceForm.manager_user_id ? Number(serviceForm.manager_user_id) : null })); Object.assign(serviceForm, { name: '', category: '校园服务', manager_user_id: '' }); success.value = '校园服务已加入评分目录'; await load() }
async function editCampusService(item: CampusService) { const manager = prompt('服务管理者用户 ID（留空为未指定）：', '') ; if (manager === null) return; await api(`/admin/campus-services/${item.id}`, json('PATCH', { manager_user_id: manager ? Number(manager) : null })); success.value = '服务管理者已更新'; await load() }
async function createMarketOption() {
  await api(`/admin/market/${marketOptionForm.kind}`, json('POST', { name: marketOptionForm.name, slug: marketOptionForm.slug, sort_order: Number(marketOptionForm.sort_order), active: true }))
  Object.assign(marketOptionForm, { name: '', slug: '', sort_order: 0 })
  success.value = '市场字典项已创建'; await load()
}
async function saveMarketOption(kind: 'categories' | 'locations', item: AdminMarketOption) {
  await api(`/admin/market/${kind}/${item.id}`, json('PATCH', { name: item.name, slug: item.slug, sort_order: Number(item.sort_order), active: item.active }))
  success.value = '市场字典项已保存'; await load()
}
async function disableMarketOption(kind: 'categories' | 'locations', item: AdminMarketOption) {
  if (!confirm(`确认停用“${item.name}”？已发布商品仍保留该值。`)) return
  await api(`/admin/market/${kind}/${item.id}`, { method: 'DELETE' })
  success.value = '市场字典项已停用'; await load()
}
async function decideGame(item: GameSubmission, action: string) {
  let target_game_id: number | null = null, canonical_name = ''
  if (action === 'merge') { const raw = prompt(`合并到游戏 ID（${teamGames.value.map((x) => `${x.id}:${x.name}`).join('，')}）：`, ''); if (!raw) return; target_game_id = Number(raw) }
  if (action === 'approve_new') canonical_name = prompt('规范游戏名称：', item.name) || item.name
  const admin_note = prompt('给提交者的说明：', '') || ''
  await api(`/admin/game-submissions/${item.id}/decision`, json('POST', { action, target_game_id, canonical_name, admin_note }))
  success.value = '游戏提交已处理'; await load()
}
async function saveSettings() { for (const key of siteSettingKeys.value) await api(`/admin/settings/${key}`, json('PUT', { value: settings[key] })); success.value = '站点设置已保存' }
async function saveCreditRules() { creditRules.value = await api<CreditRuleSet>('/admin/credit-rules', json('PATCH', { rules: creditRules.value.rules.map(({ key, value }) => ({ key, value: Number(value) })) })); success.value = '信用规则已保存，新的权限判断和奖惩会立即采用'; await auth.load() }
async function saveAnonymousPool() { const result = await api<{ value: string }>('/admin/settings/anonymous_nickname_pool', json('PUT', { value: settings.anonymous_nickname_pool || '' })); settings.anonymous_nickname_pool = result.value; success.value = '匿名昵称池已保存，新分配的树洞身份会采用这份名单' }
async function backup() { await api('/admin/backups', json('POST')); success.value = '备份任务已进入后台队列'; await load() }
onMounted(() => run(load))
</script>

<template>
  <section>
    <header class="page-head"><h1>🛠️ 管理后台</h1><p>所有审核、限权、处罚和设置变更都会记录操作者与理由。</p></header>
    <p v-if="!auth.loading && !auth.canModerate" class="empty-state">当前账号没有审核权限。</p>
    <template v-else>
      <div class="stat-grid"><div v-for="(value, key) in overview" :key="key" class="stat"><b>{{ value }}</b><span>{{ key }}</span></div></div>
      <nav class="section-tabs" style="margin-top:16px"><button v-for="item in [['queue','审核队列'],['users','用户'],['appeals','申诉'],['feedback','反馈'],['games','游戏目录'],['courses','课程目录'],['services','校园服务'],['market','市场字典'],['announce','公告'],['credit','信用与匿名'],['ops','运维与审计']]" :key="item[0]" :class="{ active: tab === item[0] }" @click="tab = item[0]">{{ item[1] }}</button></nav>
      <p v-if="error" class="notice danger">{{ error }}</p><p v-if="success" class="notice success">{{ success }}</p>
      <div v-if="tab === 'queue'" class="stack"><article v-for="item in cases" :key="item.id" class="card"><div class="meta"><span class="badge yellow">{{ item.source }}</span><span>{{ item.entity_type }} #{{ item.entity_id }}</span></div><h3>{{ item.title }}</h3><p v-if="item.preview" class="notice info" style="white-space:pre-wrap">{{ item.preview }}</p><div v-for="report in item.reports" :key="report.id" class="notice danger"><strong>举报：{{ report.reason }}</strong><p>{{ report.detail || '未填写补充说明' }}</p><small>举报人 #{{ report.reporter_id }} · {{ new Date(report.created_at).toLocaleString() }}</small></div><p class="muted">{{ item.notes || '等待审核' }}</p><div class="actions"><button @click="run(() => decide(item,'approve'))">通过发布</button><button @click="run(() => decide(item,'reject'))">驳回</button><button @click="run(() => decide(item,'hide'))">隐藏</button></div></article><p v-if="!cases.length" class="empty-state">没有待审内容。</p>
      <div v-if="marketDisputes.length" class="stack"><h2>交易纠纷</h2><article v-for="item in marketDisputes" :key="item.id" class="card"><span class="badge yellow">交易 #{{ item.transaction_id }}</span><h3>{{ item.opened_by.nickname }} 发起纠纷</h3><p>{{ item.reason }}</p><div class="actions"><button @click="run(() => decideMarketDispute(item, 'completed'))">裁定已成交</button><button class="button danger" @click="run(() => decideMarketDispute(item, 'cancelled'))">裁定取消</button></div></article></div></div>
      <div v-else-if="tab === 'users'" class="card table-wrap"><table><thead><tr><th>ID</th><th>用户</th><th>身份</th><th>角色</th><th>状态</th><th>信用</th><th>操作</th></tr></thead><tbody><tr v-for="item in users" :key="item.id"><td>{{ item.id }}</td><td>{{ item.nickname }}<small class="muted"> {{ item.email }}</small></td><td><select v-model="item.campus_identity" :disabled="!auth.isAdmin"><option value="student">学生</option><option value="alumni">校友</option><option value="staff">教职工</option></select></td><td><select v-model="item.role" :disabled="!auth.isAdmin"><option value="user">用户</option><option value="moderator">审核员</option><option value="admin">管理员</option></select></td><td><select v-model="item.status" :disabled="!auth.isAdmin"><option value="active">正常</option><option value="restricted">限权</option><option value="disabled">停用</option></select></td><td><input v-model.number="item.credit" type="number" min="0" max="1000" style="width:82px" :disabled="!auth.isAdmin" /></td><td><button v-if="auth.isAdmin" class="button secondary small" @click="run(() => saveUser(item))">保存</button><button class="button danger small" @click="run(() => punish(item))">处罚</button></td></tr></tbody></table></div>
      <div v-else-if="tab === 'appeals'" class="stack"><article v-for="item in appeals" :key="item.id" class="card"><span class="badge yellow">{{ item.status }}</span><h3>处罚 #{{ item.penalty_id }} · 用户 #{{ item.user_id }}</h3><p>{{ item.reason }}</p><div v-if="item.status === 'pending'" class="actions"><button @click="run(() => decideAppeal(item,'approved'))">申诉成立</button><button @click="run(() => decideAppeal(item,'rejected'))">维持处理</button></div></article><p v-if="!appeals.length" class="empty-state">暂无申诉。</p></div>
      <div v-else-if="tab === 'feedback'" class="stack"><article v-for="item in feedback" :key="item.id" class="card"><div class="meta"><span class="badge">{{ item.type }}</span><span>{{ item.status }}</span></div><h3>{{ item.title }}</h3><p>{{ item.body }}</p><div v-if="item.status === 'pending'" class="actions"><button @click="run(() => decideFeedback(item,'accepted'))">采纳并奖励</button><button @click="run(() => decideFeedback(item,'rejected'))">拒绝</button></div></article><p v-if="!feedback.length" class="empty-state">暂无反馈。</p></div>
      <div v-else-if="tab === 'games'" class="stack"><div class="card"><h3>已收录游戏</h3><p v-for="game in teamGames" :key="game.id"><b>#{{ game.id }} {{ game.name }}</b><span class="muted"> · {{ game.aliases.join(' / ') }}</span></p></div><article v-for="item in gameSubmissions" :key="item.id" class="card"><span class="badge yellow">待审核</span><h3>{{ item.name }}</h3><p>别名：{{ item.aliases.join(' / ') || '未填写' }} · 提交者 #{{ item.submitter_id }}</p><div class="actions"><button @click="run(() => decideGame(item,'approve_new'))">作为新游戏收录</button><button @click="run(() => decideGame(item,'merge'))">合并到已有游戏</button><button @click="run(() => decideGame(item,'reject'))">驳回</button></div></article><p v-if="!gameSubmissions.length" class="empty-state">没有待审核的新游戏。</p></div>
      <form v-else-if="tab === 'courses'" class="card form-grid" @submit.prevent="run(createCourseOffering)"><h3 class="full">添加课程班次</h3><p class="muted full">课程名与教师组合会自动去重；学生只能评价这里已建立的班次。</p><label>课程名<input v-model="courseForm.name" required /></label><label>教师<input v-model="courseForm.teacher" required /></label><label>学期<input v-model="courseForm.semester" placeholder="2026秋" required /></label><label>班级/教学班<input v-model="courseForm.section" /></label><button class="button primary full">加入课程目录</button></form>
      <div v-else-if="tab === 'services'" class="stack"><form class="card form-grid" @submit.prevent="run(createCampusService)"><h3 class="full">添加校园服务评分项</h3><label>服务名称<input v-model="serviceForm.name" required /></label><label>分类<input v-model="serviceForm.category" required /></label><label class="full">服务管理者用户 ID（选填）<input v-model="serviceForm.manager_user_id" type="number" min="1" /></label><button class="button primary full">加入评分目录</button></form><div class="card"><h3>已收录服务</h3><p v-for="item in campusServices" :key="item.id"><b>{{ item.name }}</b><span class="muted"> · {{ item.category }} · {{ item.rating_count }} 条评价</span> <button class="text-button" @click="run(() => editCampusService(item))">指定管理者</button></p><p v-if="!campusServices.length" class="empty-state">尚未录入校园服务。</p></div></div>
      <div v-else-if="tab === 'market'" class="stack"><form class="card form-grid" @submit.prevent="run(createMarketOption)"><h3 class="full">添加市场分类或交易地点</h3><label>字典<select v-model="marketOptionForm.kind"><option value="categories">商品分类</option><option value="locations">交易地点</option></select></label><label>显示名称<input v-model.trim="marketOptionForm.name" maxlength="120" required /></label><label>稳定 slug<input v-model.trim="marketOptionForm.slug" pattern="[a-z0-9][a-z0-9-]{0,59}" maxlength="60" required /></label><label>排序<input v-model.number="marketOptionForm.sort_order" type="number" min="-10000" max="10000" required /></label><button class="button primary full">添加字典项</button></form><div v-for="group in [{ key: 'categories' as const, title: '商品分类', items: marketCategories }, { key: 'locations' as const, title: '交易地点', items: marketLocations }]" :key="group.key" class="card table-wrap"><h3>{{ group.title }}</h3><table><thead><tr><th>名称</th><th>slug</th><th>排序</th><th>启用</th><th>操作</th></tr></thead><tbody><tr v-for="item in group.items" :key="item.id"><td><input v-model.trim="item.name" maxlength="120" /></td><td><input v-model.trim="item.slug" maxlength="60" /></td><td><input v-model.number="item.sort_order" type="number" min="-10000" max="10000" style="width:90px" /></td><td><input v-model="item.active" type="checkbox" /></td><td><button class="button secondary small" @click="run(() => saveMarketOption(group.key, item))">保存</button><button v-if="item.active" class="button danger small" @click="run(() => disableMarketOption(group.key, item))">停用</button></td></tr></tbody></table></div></div>
      <form v-else-if="tab === 'announce'" class="card form-grid" @submit.prevent="run(publishAnnouncement)"><h3 class="full">发布公告</h3><label class="full">标题<input v-model="announcement.title" required /></label><label>级别<select v-model="announcement.level"><option value="normal">普通</option><option value="strong">强提醒</option></select></label><label>目标人群<select v-model="announcement.audience"><option value="all">所有用户</option><option value="student">学生</option><option value="staff">教职工</option></select></label><label class="full">正文<textarea v-model="announcement.body" rows="8" required /></label><button class="button primary full">发布公告</button></form>
      <div v-else-if="tab === 'credit'" class="stack"><template v-if="auth.isAdmin"><form class="card" @submit.prevent="run(saveCreditRules)"><div class="card-head"><div><h3>信用规则（满分 {{ creditRules.max_score }}）</h3><p class="muted">门槛变更立即生效；加减分只影响之后发生的事件。</p></div><button class="button primary">保存信用规则</button></div><div class="table-wrap"><table><thead><tr><th>类别</th><th>规则</th><th>说明</th><th>数值</th></tr></thead><tbody><tr v-for="rule in creditRules.rules" :key="rule.key"><td><span class="badge">{{ rule.kind }}</span></td><td><b>{{ rule.label }}</b><small class="muted"> {{ rule.key }}</small></td><td>{{ rule.description }}</td><td><input v-model.number="rule.value" type="number" :min="rule.kind === 'penalty' ? -1000 : 0" :max="rule.kind === 'penalty' ? 0 : 1000" style="width:90px" /></td></tr></tbody></table></div></form><form class="card form-stack" @submit.prevent="run(saveAnonymousPool)"><h3>树洞完全匿名昵称池</h3><p class="muted">一行一个昵称；保存时自动删除空行和重复项。已分配的历史匿名身份不会改变。</p><textarea v-model="settings.anonymous_nickname_pool" rows="16" maxlength="20000" placeholder="丹阳子&#10;小狐狸&#10;欧阳牛马" /><button class="button primary">保存昵称池</button></form></template><p v-else class="empty-state">信用规则和匿名昵称池仅管理员可修改。</p></div>
      <div v-else class="stack"><template v-if="auth.isAdmin"><div class="card health-panel"><div class="card-head"><div><h3>系统健康</h3><p class="muted">最后刷新：{{ formatHealthTime(systemHealthRefreshedAt?.toISOString()) }}</p></div><button class="button secondary" :disabled="systemHealthRefreshing" @click="run(refreshSystemHealth)">{{ systemHealthRefreshing ? '刷新中…' : '手动刷新' }}</button></div><div v-if="systemHealth" class="health-grid"><article v-for="item in healthCards" :key="item.key" class="health-card" :class="`health-${item.tone}`"><div class="health-card-title"><h4>{{ item.title }}</h4><span class="health-status">{{ item.status }}</span></div><p v-for="detail in item.details" :key="detail">{{ detail }}</p></article></div><p v-else class="empty-state">正在读取系统健康状态…</p><details v-if="systemHealth" class="health-raw"><summary>原始数据（排障用）</summary><pre>{{ JSON.stringify(systemHealth, null, 2) }}</pre></details></div><form class="card form-stack" @submit.prevent="run(saveSettings)"><h3>站点设置</h3><label v-for="key in siteSettingKeys" :key="key">{{ key }}<textarea v-model="settings[key]" rows="2" /></label><button class="button primary">保存设置</button></form><div class="card"><div class="card-head"><div><h3>备份</h3><p class="muted">备份写入异地 S3，保留 7 个每日、4 个每周和 12 个每月恢复点。</p></div><button class="button secondary" @click="run(backup)">生成备份</button></div><p v-for="item in backups" :key="item.id"><span class="badge">{{ item.status }}</span> #{{ item.id }} · {{ item.created_at }} <a v-if="item.download_url" class="button small secondary" :href="item.download_url">安全下载</a></p></div><div class="card table-wrap"><h3>审计日志</h3><table><thead><tr><th>时间</th><th>操作者</th><th>动作</th><th>对象</th><th>理由</th></tr></thead><tbody><tr v-for="item in auditRows" :key="item.id"><td>{{ new Date(item.created_at).toLocaleString() }}</td><td>{{ item.actor_id }}</td><td>{{ item.action }}</td><td>{{ item.target_type }} #{{ item.target_id }}</td><td>{{ item.reason }}</td></tr></tbody></table></div></template><p v-else class="empty-state">运维、设置和完整审计日志仅管理员可见。</p></div>
    </template>
  </section>
</template>

<style scoped>
.health-grid{display:grid;grid-template-columns:repeat(3,minmax(0,1fr));gap:12px;margin-top:16px}.health-card{border:1px solid var(--line);border-left-width:5px;border-radius:10px;padding:14px;background:var(--paper)}.health-card-title{display:flex;align-items:flex-start;justify-content:space-between;gap:10px}.health-card h4{margin:0}.health-card p{margin:5px 0 0;color:var(--muted);font-size:12px;overflow-wrap:anywhere}.health-status{border-radius:999px;padding:2px 8px;font-size:11px;white-space:nowrap}.health-ok{border-left-color:var(--green)}.health-ok .health-status{color:var(--green);background:var(--green-soft)}.health-warning{border-left-color:#b7791f}.health-warning .health-status{color:#7c4a00;background:var(--yellow)}.health-danger{border-left-color:var(--red)}.health-danger .health-status{color:#fff;background:var(--red)}.health-raw{margin-top:16px}.health-raw summary{cursor:pointer;color:var(--muted)}.health-raw pre{max-height:360px;overflow:auto;white-space:pre-wrap;font-size:12px}@media(max-width:900px){.health-grid{grid-template-columns:repeat(2,minmax(0,1fr))}}@media(max-width:600px){.health-grid{grid-template-columns:1fr}}
</style>

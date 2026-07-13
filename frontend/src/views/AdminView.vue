<script setup lang="ts">
import { onMounted, reactive, ref } from 'vue'
import { api, json } from '../api'
import { useAuthStore } from '../stores/auth'
import type { Page } from '../types'

const auth = useAuthStore()
const overview = ref<Record<string, number>>({}), users = ref<any[]>([]), cases = ref<any[]>([]), appeals = ref<any[]>([]), feedback = ref<any[]>([]), backups = ref<any[]>([]), auditRows = ref<any[]>([])
const settings = reactive<Record<string, string>>({}), announcement = reactive({ title: '', body: '', level: 'normal', audience: 'all' })
const courseForm = reactive({ name: '', teacher: '', semester: '', section: '' })
const tab = ref('queue'), error = ref(''), success = ref('')

async function load() {
  await auth.load()
  if (!auth.canModerate) return
  const tasks: Promise<any>[] = [
    api('/admin/overview'), api<Page<any>>('/admin/users?page_size=100'), api<Page<any>>('/admin/moderation-cases'),
    api<Page<any>>('/admin/appeals'), api<Page<any>>('/admin/feedback'),
  ]
  if (auth.isAdmin) tasks.push(api('/admin/settings'), api<Page<any>>('/admin/backups'), api<Page<any>>('/admin/audit-logs?page_size=100'))
  const values = await Promise.all(tasks)
  overview.value = values[0]; users.value = values[1].items; cases.value = values[2].items; appeals.value = values[3].items; feedback.value = values[4].items
  if (auth.isAdmin) { Object.assign(settings, values[5]); backups.value = values[6].items; auditRows.value = values[7].items }
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
  const credit_delta = Number(prompt('信用分变化（0 到 -100）：', '-5'))
  await api('/admin/penalties', json('POST', { user_id: item.id, violation_type, result, rule, credit_delta }))
  success.value = '处罚已记录、通知责任人并进入可申诉流程'; await load()
}
async function decideAppeal(item: any, status: string) { const note = prompt('处理说明：', '') ?? ''; await api(`/admin/appeals/${item.id}/decision`, json('POST', { status, note })); await load() }
async function decideFeedback(item: any, status: string) { const admin_note = prompt('给提交者的说明：', '') ?? ''; await api(`/admin/feedback/${item.id}/decision`, json('POST', { status, admin_note, reward_xp: status === 'accepted' ? 20 : 0, reward_credit: status === 'accepted' ? 1 : 0 })); await load() }
async function publishAnnouncement() { await api('/admin/announcements', json('POST', announcement)); Object.assign(announcement, { title: '', body: '', level: 'normal', audience: 'all' }); success.value = '公告已发布' }
async function createCourseOffering() { const course = await api<any>('/courses', json('POST', { name: courseForm.name, teacher: courseForm.teacher })); await api('/course-offerings', json('POST', { course_id: course.id, semester: courseForm.semester, section: courseForm.section })); Object.assign(courseForm, { name: '', teacher: '', semester: '', section: '' }); success.value = '课程与班次已加入目录' }
async function saveSettings() { for (const [key, value] of Object.entries(settings)) await api(`/admin/settings/${key}`, json('PUT', { value })); success.value = '站点设置已保存' }
async function backup() { await api('/admin/backups', json('POST')); success.value = '备份任务已进入后台队列'; await load() }
onMounted(() => run(load))
</script>

<template>
  <section>
    <header class="page-head"><h1>管理后台</h1><p>所有审核、限权、处罚和设置变更都会记录操作者与理由。</p></header>
    <p v-if="!auth.loading && !auth.canModerate" class="empty">当前账号没有审核权限。</p>
    <template v-else>
      <div class="stat-grid"><div v-for="(value, key) in overview" :key="key" class="stat"><b>{{ value }}</b><span>{{ key }}</span></div></div>
      <nav class="section-tabs" style="margin-top:16px"><button v-for="item in [['queue','审核队列'],['users','用户'],['appeals','申诉'],['feedback','反馈'],['courses','课程目录'],['announce','公告'],['ops','运维与审计']]" :key="item[0]" :class="{ active: tab === item[0] }" @click="tab = item[0]">{{ item[1] }}</button></nav>
      <p v-if="error" class="notice danger">{{ error }}</p><p v-if="success" class="notice success">{{ success }}</p>
      <div v-if="tab === 'queue'" class="stack"><article v-for="item in cases" :key="item.id" class="card"><div class="meta"><span class="badge yellow">{{ item.source }}</span><span>{{ item.entity_type }} #{{ item.entity_id }}</span></div><h3>{{ item.title }}</h3><p v-if="item.preview" class="notice info" style="white-space:pre-wrap">{{ item.preview }}</p><div v-for="report in item.reports" :key="report.id" class="notice danger"><strong>举报：{{ report.reason }}</strong><p>{{ report.detail || '未填写补充说明' }}</p><small>举报人 #{{ report.reporter_id }} · {{ new Date(report.created_at).toLocaleString() }}</small></div><p class="muted">{{ item.notes || '等待审核' }}</p><div class="actions"><button @click="run(() => decide(item,'approve'))">通过发布</button><button @click="run(() => decide(item,'reject'))">驳回</button><button @click="run(() => decide(item,'hide'))">隐藏</button></div></article><p v-if="!cases.length" class="empty">没有待审内容。</p></div>
      <div v-else-if="tab === 'users'" class="card table-wrap"><table><thead><tr><th>ID</th><th>用户</th><th>身份</th><th>角色</th><th>状态</th><th>信用</th><th>操作</th></tr></thead><tbody><tr v-for="item in users" :key="item.id"><td>{{ item.id }}</td><td>{{ item.nickname }}<small class="muted"> {{ item.email }}</small></td><td><select v-model="item.campus_identity" :disabled="!auth.isAdmin"><option value="student">学生</option><option value="alumni">校友</option><option value="staff">教职工</option></select></td><td><select v-model="item.role" :disabled="!auth.isAdmin"><option value="user">用户</option><option value="moderator">审核员</option><option value="admin">管理员</option></select></td><td><select v-model="item.status" :disabled="!auth.isAdmin"><option value="active">正常</option><option value="restricted">限权</option><option value="disabled">停用</option></select></td><td><input v-model.number="item.credit" type="number" min="0" max="100" style="width:70px" :disabled="!auth.isAdmin" /></td><td><button v-if="auth.isAdmin" class="button secondary small" @click="run(() => saveUser(item))">保存</button><button class="button danger small" @click="run(() => punish(item))">处罚</button></td></tr></tbody></table></div>
      <div v-else-if="tab === 'appeals'" class="stack"><article v-for="item in appeals" :key="item.id" class="card"><span class="badge yellow">{{ item.status }}</span><h3>处罚 #{{ item.penalty_id }} · 用户 #{{ item.user_id }}</h3><p>{{ item.reason }}</p><div v-if="item.status === 'pending'" class="actions"><button @click="run(() => decideAppeal(item,'approved'))">申诉成立</button><button @click="run(() => decideAppeal(item,'rejected'))">维持处理</button></div></article><p v-if="!appeals.length" class="empty">暂无申诉。</p></div>
      <div v-else-if="tab === 'feedback'" class="stack"><article v-for="item in feedback" :key="item.id" class="card"><div class="meta"><span class="badge">{{ item.type }}</span><span>{{ item.status }}</span></div><h3>{{ item.title }}</h3><p>{{ item.body }}</p><div v-if="item.status === 'pending'" class="actions"><button @click="run(() => decideFeedback(item,'accepted'))">采纳并奖励</button><button @click="run(() => decideFeedback(item,'rejected'))">拒绝</button></div></article><p v-if="!feedback.length" class="empty">暂无反馈。</p></div>
      <form v-else-if="tab === 'courses'" class="card form-grid" @submit.prevent="run(createCourseOffering)"><h3 class="full">添加课程班次</h3><p class="muted full">课程名与教师组合会自动去重；学生只能评价这里已建立的班次。</p><label>课程名<input v-model="courseForm.name" required /></label><label>教师<input v-model="courseForm.teacher" required /></label><label>学期<input v-model="courseForm.semester" placeholder="2026秋" required /></label><label>班级/教学班<input v-model="courseForm.section" /></label><button class="button primary full">加入课程目录</button></form>
      <form v-else-if="tab === 'announce'" class="card form-grid" @submit.prevent="run(publishAnnouncement)"><h3 class="full">发布公告</h3><label class="full">标题<input v-model="announcement.title" required /></label><label>级别<select v-model="announcement.level"><option value="normal">普通</option><option value="strong">强提醒</option></select></label><label>目标人群<select v-model="announcement.audience"><option value="all">所有用户</option><option value="student">学生</option><option value="staff">教职工</option></select></label><label class="full">正文<textarea v-model="announcement.body" rows="8" required /></label><button class="button primary full">发布公告</button></form>
      <div v-else class="stack"><template v-if="auth.isAdmin"><form class="card form-stack" @submit.prevent="run(saveSettings)"><h3>站点设置</h3><label v-for="(_, key) in settings" :key="key">{{ key }}<textarea v-model="settings[key]" rows="2" /></label><button class="button primary">保存设置</button></form><div class="card"><div class="card-head"><div><h3>备份</h3><p class="muted">备份包包含 PostgreSQL dump 与上传文件，服务器仅保留最近 7 份。</p></div><button class="button secondary" @click="run(backup)">生成备份</button></div><p v-for="item in backups" :key="item.id"><span class="badge">{{ item.status }}</span> #{{ item.id }} · {{ item.created_at }} <a v-if="item.download_url" class="button small secondary" :href="item.download_url">安全下载</a></p></div><div class="card table-wrap"><h3>审计日志</h3><table><thead><tr><th>时间</th><th>操作者</th><th>动作</th><th>对象</th><th>理由</th></tr></thead><tbody><tr v-for="item in auditRows" :key="item.id"><td>{{ new Date(item.created_at).toLocaleString() }}</td><td>{{ item.actor_id }}</td><td>{{ item.action }}</td><td>{{ item.target_type }} #{{ item.target_id }}</td><td>{{ item.reason }}</td></tr></tbody></table></div></template><p v-else class="empty">运维、设置和完整审计日志仅管理员可见。</p></div>
    </template>
  </section>
</template>

<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, reactive, ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { appConfig } from '../../config'
import BaseModal from '../../components/BaseModal.vue'
import { useAuthStore } from '../../stores/auth'
import { teamRunActionState } from '../../teamRun'
import type { Team, TeamGame } from '../../types'
import type { TeamRun } from '../../generated/sdk'
import { teamsApi } from './api'

const auth = useAuthStore()
const route = useRoute(), router = useRouter()
const teams = ref<Team[]>([])
const loading = ref(true)
// Disables the create button while the request is in flight (prevents duplicate teams).
const creating = ref(false)
const error = ref('')
const composer = ref(false)
const detailTeam = ref<Team | null>(null)
const teamActionBusy = ref<'check-in' | 'excuse' | 'leave' | null>(null)
const teamActionNotice = ref('')
const teamActionFailed = ref(false)
const runManager = ref<Team | null>(null)
const runs = ref<TeamRun[]>([])
const gameFilter = ref('all')
const teamGames = ref<TeamGame[]>([])
const gameSubmitOpen = ref(false)
const gameSubmit = reactive({ name: '', aliases: '' })
const submitMessage = ref('')
const now = ref(Date.now())
let clock: ReturnType<typeof setInterval> | null = null
const form = reactive({ game_id: null as number | null, game: '', mode: '', rank_requirement: '不限', capacity: 5, starts_at: '', recurrence: 'once', voice_name: 'KOOK', voice_link: '', notes: '', newbie_level: '欢迎新手，带练', vibe: '', reminder_minutes: 30, post_departure_retention_minutes: 120, reminder_channels: ['email', 'in_app'] as string[] })
const visibleTeams = computed(() => gameFilter.value === 'all' ? teams.value : teams.value.filter((team) => team.game === gameFilter.value))
const currentRunAction = computed(() => teamRunActionState(detailTeam.value, now.value))
const minStartsAt = computed(() => localDateTime(new Date(now.value + 10 * 60_000)))
const timeWarning = computed(() => {
  if (!form.starts_at) return ''
  const value = new Date(form.starts_at).getTime()
  if (!Number.isFinite(value)) return '请选择有效的发车时间。'
  if (value <= now.value) return '发车时间已早于当前时间，请重新选择。'
  if (value <= now.value + 10 * 60_000) return '发车时间至少需要比当前时间晚 10 分钟。'
  return ''
})

async function load() {
  loading.value = true
  error.value = ''
  try {
    const [teamPage, catalog] = await Promise.all([teamsApi.list(), teamsApi.games()])
    teams.value = teamPage.items
    teamGames.value = catalog.items
    if (!form.game_id && teamGames.value.length) form.game_id = teamGames.value[0].id
    if (detailTeam.value) detailTeam.value = teams.value.find((team) => team.id === detailTeam.value?.id) || null
    const routeId = Number(route.params.id)
    if (routeId) detailTeam.value = teams.value.find((team) => team.id === routeId) || null
  }
  catch (e) { error.value = e instanceof Error ? e.message : '车队加载失败' }
  finally { loading.value = false }
}

function createOpen() { if (auth.requireLogin()) { error.value = ''; composer.value = true } }
async function create() {
  if (timeWarning.value) { error.value = timeWarning.value; return }
  if (creating.value) return
  creating.value = true
  try {
    await teamsApi.create({ ...form, starts_at: new Date(form.starts_at).toISOString() })
  } catch (e) {
    error.value = e instanceof Error ? e.message : '创建车队失败'
    return
  } finally {
    creating.value = false
  }
  composer.value = false
  Object.assign(form, { game_id: teamGames.value[0]?.id || null, game: '', mode: '', rank_requirement: '不限', capacity: 5, starts_at: '', recurrence: 'once', voice_name: 'KOOK', voice_link: '', notes: '', newbie_level: '欢迎新手，带练', vibe: '', reminder_minutes: 30, post_departure_retention_minutes: 120, reminder_channels: ['email', 'in_app'] })
  await load()
}
async function join(team: Team) { if (auth.requireLogin()) { await teamsApi.join(team.id); await load() } }
function openDetail(team: Team) {
  error.value = ''
  detailTeam.value = team
  teamActionNotice.value = ''
  teamActionFailed.value = false
  if (String(route.params.id || '') !== String(team.id)) router.push({ path: `/teams/${team.id}`, query: route.query })
}
function closeDetail() {
  detailTeam.value = null
  teamActionNotice.value = ''
  teamActionFailed.value = false
  if (route.params.id) router.replace({ path: '/teams', query: route.query })
}
async function submitGame() {
  await teamsApi.submitGame({ name: gameSubmit.name, aliases: gameSubmit.aliases.split(/[/,，]/).map((x) => x.trim()).filter(Boolean) })
  submitMessage.value = '已提交审核，通过或合并后会通过站内通知告诉你。'
  Object.assign(gameSubmit, { name: '', aliases: '' })
}
function toggleReminder(channel: string) {
  const index = form.reminder_channels.indexOf(channel)
  if (index >= 0) {
    if (form.reminder_channels.length > 1) form.reminder_channels.splice(index, 1)
  } else form.reminder_channels.push(channel)
}
function quickTime(kind: string) {
  const date = new Date()
  if (kind === 'tonight') { date.setHours(20, 0, 0, 0); if (date.getTime() <= now.value + 10 * 60_000) date.setDate(date.getDate() + 1) }
  else if (kind === 'friday') { date.setDate(date.getDate() + ((5 - date.getDay() + 7) % 7 || 7)); date.setHours(19, 0, 0, 0) }
  else return
  form.starts_at = localDateTime(date)
}
async function runTeamAction(kind: 'check-in' | 'excuse' | 'leave', task: () => Promise<string>) {
  teamActionBusy.value = kind
  teamActionNotice.value = ''
  teamActionFailed.value = false
  try { teamActionNotice.value = await task() }
  catch (e) { teamActionFailed.value = true; teamActionNotice.value = e instanceof Error ? e.message : '操作失败，请稍后重试。' }
  finally { teamActionBusy.value = null }
}
async function leave(team: Team) {
  if (!confirm(`确定下车吗？临近发车且未请假会扣 ${Math.abs(auth.creditRule('penalty.team_late_leave'))} 信用。`)) return
  await runTeamAction('leave', async () => { await teamsApi.leave(team.id); await auth.load(); await load(); return '已退出车队。' })
}
async function excuse(team: Team) {
  if (!team.next_run) return
  await runTeamAction('excuse', async () => { await teamsApi.excuse(team.id, team.next_run!.id); await load(); return '本场请假已记录。' })
}
async function checkIn(team: Team) {
  if (!team.next_run) return
  await runTeamAction('check-in', async () => {
    const result = await teamsApi.checkIn(team.id, team.next_run!.id)
    await auth.load(); await load()
    return result.credit_delta > 0 ? `签到成功，信用 +${result.credit_delta}。` : '本场已签到，信用奖励未重复发放。'
  })
}
async function cancel(team: Team) { if (confirm('取消后所有成员都会收到通知，确定继续吗？')) { await teamsApi.cancel(team.id); await load() } }
async function editTeam(team: Team) {
  const mode = prompt('车队模式：', team.mode); if (mode === null) return
  const notes = prompt('车头注意事项：', team.notes); if (notes === null) return
  const rawCapacity = prompt('容量（2-99）：', String(team.capacity))
  if (rawCapacity === null) return
  const capacity = Number(rawCapacity)
  if (!Number.isInteger(capacity) || capacity < 2 || capacity > 99) { error.value = '容量需为 2 到 99 之间的整数'; return }
  try {
    await teamsApi.update(team.id, { mode, notes, capacity })
    await load()
  } catch (e) {
    error.value = e instanceof Error ? e.message : '保存车队信息失败'
  }
}
async function removeMember(team: Team, member: { id: number; nickname: string }) {
  if (!confirm(`确定移除 ${member.nickname}？`)) return
  await teamsApi.removeMember(team.id, member.id); await load()
}
async function transfer(team: Team, member: { id: number; nickname: string }) {
  if (!confirm(`确定将车头转让给 ${member.nickname}？`)) return
  await teamsApi.transfer(team.id, member.id); await load()
}
async function openRuns(team: Team) {
  error.value = ''
  teamActionNotice.value = ''
  runManager.value = team
  runs.value = (await teamsApi.runs(team.id)).items
}
// Only serializable timestamps may cross the API boundary.
function parseRunTime(value: string | null): string | null {
  if (value === null) return null
  const date = new Date(value.trim())
  if (Number.isNaN(date.getTime())) {
    error.value = '时间格式无法识别，请使用 2026-07-20 20:00 这样的格式。'
    return null
  }
  return date.toISOString()
}
async function createRun(team: Team) {
  const starts = parseRunTime(prompt('新场次发车时间（例如 2026-07-20 20:00）：', ''))
  if (!starts) return
  try {
    await teamsApi.createRun(team.id, starts)
    await openRuns(team); await load()
  } catch (e) {
    error.value = e instanceof Error ? e.message : '新增场次失败'
  }
}
async function editRun(team: Team, run: TeamRun) {
  const starts = parseRunTime(prompt('新的发车时间：', new Date(run.starts_at).toLocaleString()))
  if (!starts) return
  try {
    await teamsApi.updateRun(team.id, run.id, { starts_at: starts })
    await openRuns(team); await load()
  } catch (e) {
    error.value = e instanceof Error ? e.message : '修改场次失败'
  }
}
async function cancelRun(team: Team, run: TeamRun) {
  if (!confirm('只取消这个场次？车队本身会继续保留。')) return
  await teamsApi.updateRun(team.id, run.id, { status: 'cancelled' }); await openRuns(team); await load()
}
async function rate(team: Team, run: TeamRun, member: { id: number; nickname: string }) {
  const raw = prompt(`评价 ${member.nickname}（friendly / communication / skill / punctual，可逗号分隔）：`, 'friendly')
  if (!raw) return
  const tags = raw.split(/[,，]/).map((x) => x.trim()).filter(Boolean)
  try {
    await teamsApi.rate(team.id, run.id, { target_user_id: member.id, tags })
    teamActionFailed.value = false
    teamActionNotice.value = '评价已记录。'
  } catch (e) {
    teamActionFailed.value = true
    teamActionNotice.value = e instanceof Error ? e.message : '评价失败'
  }
}
function localDateTime(value: Date) { return new Date(value.getTime() - value.getTimezoneOffset() * 60000).toISOString().slice(0, 16) }
function time(value: string) { return new Date(value).toLocaleString('zh-CN', { month: 'numeric', day: 'numeric', hour: '2-digit', minute: '2-digit' }) }
function departed(team: Team) { return Boolean(team.next_run && new Date(team.next_run.starts_at).getTime() <= now.value) }
function departureText(team: Team) {
  if (!team.next_run) return '等待排期'
  const difference = new Date(team.next_run.starts_at).getTime() - now.value
  if (difference > 24 * 60 * 60_000) return time(team.next_run.starts_at)
  if (difference > 0) {
    const seconds = Math.floor(difference / 1000)
    const hour = String(Math.floor(seconds / 3600)).padStart(2, '0')
    const minute = String(Math.floor(seconds % 3600 / 60)).padStart(2, '0')
    const second = String(seconds % 60).padStart(2, '0')
    return `${hour}:${minute}:${second} 后发车`
  }
  const elapsedMinutes = Math.max(0, Math.floor(-difference / 60_000))
  return `已发车 ${Math.floor(elapsedMinutes / 60)}小时${elapsedMinutes % 60}分钟`
}
function ratingLabel(value: string) { return ({ friendly: '友善', communication: '沟通', skill: '技术', punctual: '准时' } as Record<string,string>)[value] || value }
function reminderLabel(value: string) { return ({ email: '📧 邮件', in_app: '🔔 站内', calendar: '📅 日历订阅' } as Record<string,string>)[value] || value }
async function consumeCreate() {
  if (route.query.create !== '1') return
  createOpen()
  const query = { ...route.query }
  delete query.create
  await router.replace({ path: route.path, query })
}
watch(() => route.query.create, consumeCreate)
watch(() => route.params.id, (value) => {
  const id = Number(value)
  detailTeam.value = id ? teams.value.find((team) => team.id === id) || null : null
})
onMounted(async () => { clock = setInterval(() => { now.value = Date.now() }, 1000); await load(); await consumeCreate() })
onBeforeUnmount(() => { if (clock) clearInterval(clock) })
</script>

<template>
  <section class="team-page-v4">
    <header class="page-head team-page-head">
      <div><h2>🎮 游戏车队大厅</h2><p>发车前 30 分钟自动提醒 · 邮件 / 站内 / 日历订阅</p></div>
      <button class="btn primary" @click="createOpen">+ 发布开车</button>
      <button class="btn ghost" @click="auth.requireLogin() && (gameSubmitOpen = true)">提交新游戏</button>
    </header>
    <div v-if="teamGames.length" class="game-filter">
      <button class="chip" :class="{ on: gameFilter === 'all' }" @click="gameFilter = 'all'">全部</button>
      <button v-for="game in teamGames" :key="game.id" class="chip" :class="{ on: gameFilter === game.name }" @click="gameFilter = game.name">{{ game.name }}</button>
    </div>
    <p class="muted page-tip">ⓘ 玩家提交的游戏名会由管理员合并重复项，例如「Valorant / 瓦 / 无畏契约」会归为同一标签。</p>
    <p v-if="error" class="notice danger">{{ error }}</p>
    <p v-if="loading" class="empty-state">正在寻找队友…</p>
    <div v-else class="ticket-list-v4">
      <p v-if="!visibleTeams.length" class="empty-state">当前筛选下没有待发车队，来当第一个车头。</p>
      <article v-for="team in visibleTeams" :key="team.id" class="ticket">
        <div class="t-left">
          <div class="ticket-heading-v4">
            <span class="t-game">{{ team.game }}</span>
            <span v-if="team.next_run" class="tag red">{{ departureText(team) }}</span>
            <span v-if="team.recurrence === 'weekly'" class="tag blue">长期固定队</span>
            <span v-if="team.newbie_level" class="tag green">{{ team.newbie_level }}</span>
            <span v-if="team.vibe" class="tag yellow">氛围：{{ team.vibe }}</span>
          </div>
          <div class="t-row">模式 <b>{{ team.mode }}</b> ｜ 段位 <b>{{ team.rank_requirement || '不限' }}</b> ｜ 语音 <b>{{ team.voice_name || '待车头通知' }}</b><a v-if="/^https?:\/\//i.test(team.voice_link || '')" :href="team.voice_link" target="_blank" rel="noopener noreferrer">（进入频道 ↗）</a></div>
          <div class="t-row">车头：<b>{{ team.owner.nickname }}</b> <span v-if="team.completion_rate != null" class="stamp-badge">发车率 {{ team.completion_rate }}%</span> 队友评价：<span v-for="(count, tag) in team.rating_tags" :key="tag" class="tag" :class="tag === 'skill' || tag === 'communication' ? 'blue' : 'green'">{{ ratingLabel(String(tag)) }} ×{{ count }}</span><span v-if="!Object.keys(team.rating_tags || {}).length" class="muted">暂无评价</span></div>
          <div class="t-row muted">提醒方式：{{ (team.reminder_channels || []).map(reminderLabel).join(' · ') }}（发车前 {{ team.reminder_minutes }} 分钟）</div>
        </div>
        <aside class="t-right">
          <div class="seats">{{ team.member_count }} / {{ team.capacity }}</div>
          <div class="seatdots"><i v-for="n in team.capacity" :key="n" :class="{ 'is-empty': n > team.member_count }" /></div>
          <div class="cd">{{ departureText(team) }}</div>
          <button class="btn primary sm" @click="openDetail(team)">{{ team.mine ? '管理车队' : team.joined || departed(team) ? '查看车队' : '上车' }}</button>
        </aside>
      </article>
    </div>
    <div class="rulebox team-rules-v4"><b>🎫 车队信用规则</b><ul><li>临近发车退出且不请假：信用 {{ auth.creditRule('penalty.team_late_leave') }}；准时到场：信用 +{{ auth.creditRule('reward.team_check_in') }}。</li><li>发车后队友可互评：<b>友善 / 沟通 / 技术 / 准时</b> 四维标签，累计展示在个人主页。</li><li>信用 &lt; {{ auth.creditRule('threshold.team_create') }} 无法创建车队；车队到期后从大厅隐藏但保留历史记录。</li></ul></div>

    <BaseModal v-if="detailTeam" :title="`🎮 ${detailTeam.game} · 车队详情`" wide @close="closeDetail">
      <p v-if="error" class="notice danger">{{ error }}</p>
      <div class="team-detail-v4">
        <div class="team-detail-summary">
          <div><span class="tag blue">{{ detailTeam.mode }}</span><span class="tag green">{{ detailTeam.newbie_level }}</span><span v-if="detailTeam.vibe" class="tag yellow">{{ detailTeam.vibe }}</span><h3>{{ detailTeam.member_count }} / {{ detailTeam.capacity }} 人 · {{ departureText(detailTeam) }}</h3><p>车头 {{ detailTeam.owner.nickname }} · 信用 {{ detailTeam.owner.credit }} · 段位要求 {{ detailTeam.rank_requirement || '不限' }}</p></div>
          <div class="seatdots detail-seatdots"><i v-for="n in detailTeam.capacity" :key="n" :class="{ 'is-empty': n > detailTeam.member_count }" /></div>
        </div>
        <div class="rulebox detail-reminders"><b>提醒：</b>{{ detailTeam.reminder_channels.map(reminderLabel).join(' · ') }}，发车前 {{ detailTeam.reminder_minutes }} 分钟发送。</div>
        <p v-if="detailTeam.joined" class="notice info team-run-hint">{{ currentRunAction.hint }}</p>
        <p v-if="teamActionNotice" class="notice" :class="teamActionFailed ? 'danger' : 'success'">{{ teamActionNotice }}</p>
        <div v-if="detailTeam.notes" class="team-detail-notes"><b>车头注意事项</b><p>{{ detailTeam.notes }}</p></div>
        <div class="team-member-list"><h3>队伍成员</h3><div v-for="member in detailTeam.members" :key="member.id" class="team-member-row"><span><b>{{ member.nickname }}</b><small>信用 {{ member.credit }}<template v-if="member.id === detailTeam.owner.id"> · 车头</template></small></span><div v-if="detailTeam.mine && member.id !== detailTeam.owner.id"><button class="btn ghost sm" @click="removeMember(detailTeam, member)">移除</button><button class="btn ghost sm" @click="transfer(detailTeam, member)">转让车头</button></div></div></div>
        <div class="modal-actions-v4">
          <button v-if="!detailTeam.joined && !departed(detailTeam)" class="btn primary" @click="join(detailTeam)">确认上车</button>
          <template v-else-if="detailTeam.joined"><button class="btn primary" :disabled="!currentRunAction.checkInEnabled || teamActionBusy !== null" @click="checkIn(detailTeam)">{{ teamActionBusy === 'check-in' ? '签到中…' : currentRunAction.checkInLabel }}</button><button class="btn ghost" :disabled="!currentRunAction.excuseEnabled || teamActionBusy !== null" @click="excuse(detailTeam)">{{ teamActionBusy === 'excuse' ? '请假中…' : currentRunAction.excuseLabel }}</button><button v-if="!detailTeam.mine" class="btn ghost" :disabled="teamActionBusy !== null" @click="leave(detailTeam)">{{ teamActionBusy === 'leave' ? '退出中…' : '退出车队' }}</button></template>
          <template v-if="detailTeam.mine"><button class="btn ghost" @click="editTeam(detailTeam)">编辑车队</button><button class="btn ghost" @click="createRun(detailTeam)">新增场次</button><button class="btn warn" @click="cancel(detailTeam)">取消车队</button></template>
          <button v-if="detailTeam.joined" class="btn ghost" @click="openRuns(detailTeam)">场次记录与评价</button>
          <a v-if="detailTeam.joined && (detailTeam.my_reminder_channels || []).includes('calendar')" class="btn ghost" :href="`${appConfig().api_prefix}/teams/${detailTeam.id}/calendar.ics`">下载日历</a>
        </div>
      </div>
    </BaseModal>
    <BaseModal v-if="composer" title="🚗 发布开车" wide @close="composer = false">
      <p class="modal-sub">需信用 ≥ {{ auth.creditRule('threshold.team_create') }} · 当前信用 {{ auth.user?.credit ?? '—' }}</p>
      <form class="form-grid" @submit.prevent="create"><label>游戏<select v-model.number="form.game_id" required><option v-for="game in teamGames" :key="game.id" :value="game.id">{{ game.name }}</option></select></label><label>模式<input v-model.trim="form.mode" required maxlength="80" placeholder="竞技排位 / 友人房 / 大乱斗" /></label><label>段位要求<input v-model.trim="form.rank_requirement" placeholder="黄金~铂金 / 不限" /></label><label>人数<select v-model.number="form.capacity"><option v-for="n in [2,3,4,5,6,8,10]" :key="n" :value="n">{{ n }}</option></select></label><div class="full"><label>时间机制</label><div class="option-chips"><button type="button" @click="quickTime('tonight')">今晚 8 点</button><button type="button" @click="quickTime('friday')">周五晚</button><button type="button" :class="{ active: form.recurrence === 'weekly' }" @click="form.recurrence = form.recurrence === 'weekly' ? 'once' : 'weekly'">长期固定队（每周重复）</button></div><input v-model="form.starts_at" type="datetime-local" :min="minStartsAt" required /><p v-if="timeWarning" class="notice danger team-time-warning">{{ timeWarning }}</p></div><label>发车后保留时间<select v-model.number="form.post_departure_retention_minutes"><option v-for="hour in 8" :key="hour" :value="hour * 60">{{ hour }} 小时</option></select><small class="muted">到期后从大厅隐藏，历史记录仍保留。</small></label><label>语音方式<select v-model="form.voice_name"><option v-for="name in ['KOOK','QQ 群语音','Discord','OOPZ','Teamspeak','飞书','不需要语音']" :key="name">{{ name }}</option></select></label><label>频道跳转链接（选填）<input v-model.trim="form.voice_link" type="url" placeholder="粘贴 QQ 群 / KOOK / TS 邀请链接" /></label><label>是否欢迎新手<select v-model="form.newbie_level"><option>欢迎新手，带练</option><option>欢迎新手</option><option>需要基础</option><option>仅限熟练</option></select></label><label>车队氛围<input v-model.trim="form.vibe" placeholder="娱乐为主不骂人 / 认真上分" /></label><label>发车前提醒<select v-model.number="form.reminder_minutes"><option :value="15">15 分钟前</option><option :value="30">30 分钟前</option><option :value="60">1 小时前</option><option :value="120">2 小时前</option></select></label><label class="full">附加信息 / 注意事项<textarea v-model="form.notes" rows="3" placeholder="上车后自动展示给车友" /></label><div class="full"><label>提醒方式（可多选）</label><div class="option-chips"><button v-for="channel in [['email','📧 邮件'],['in_app','🔔 站内通知'],['calendar','📅 日历订阅'] ]" :key="channel[0]" type="button" :class="{ active: form.reminder_channels.includes(channel[0]) }" @click="toggleReminder(channel[0])">{{ channel[1] }}</button><button v-for="label in ['QQ 群机器人','KOOK Bot','Discord Bot','飞书群提醒']" :key="label" type="button" disabled>{{ label }} · 待接入</button></div></div><p v-if="error" class="notice danger full">{{ error }}</p><button class="button primary full" :disabled="creating || Boolean(timeWarning)">{{ creating ? '发布中…' : '发车！' }}</button></form>
    </BaseModal>
    <BaseModal v-if="gameSubmitOpen" title="🕹️ 提交新游戏" @close="gameSubmitOpen = false"><p class="modal-sub">提交后进入管理员审核，重复名称会被合并。</p><form class="form-stack" @submit.prevent="submitGame"><label>游戏名称<input v-model.trim="gameSubmit.name" required maxlength="80" placeholder="官方名或常用简称均可" /></label><label>常见别名<input v-model.trim="gameSubmit.aliases" maxlength="500" placeholder="用 / 分隔，如：星穹铁道 / 星铁" /></label><p v-if="submitMessage" class="notice success">{{ submitMessage }}</p><button class="button primary">提交审核</button></form></BaseModal>
    <BaseModal v-if="runManager" title="场次记录与赛后评价" wide @close="runManager = null">
      <p v-if="error" class="notice danger">{{ error }}</p>
      <p v-if="teamActionNotice" class="notice" :class="teamActionFailed ? 'danger' : 'success'">{{ teamActionNotice }}</p>
      <div class="stack"><article v-for="run in runs" :key="run.id" class="card compact"><div class="card-head"><div><strong>{{ time(run.starts_at) }}</strong><p class="muted">{{ run.status }} · {{ run.member_count }} 名场次成员<span v-if="run.my_status"> · 我的状态 {{ run.my_status }}</span></p></div><div v-if="runManager.mine && run.status === 'scheduled'" class="actions"><button @click="editRun(runManager, run)">改时间</button><button @click="cancelRun(runManager, run)">取消本场</button></div></div><div v-if="new Date(run.starts_at).getTime() < Date.now()" class="actions"><button v-for="member in runManager.members.filter((x) => x.id !== auth.user?.id)" :key="member.id" @click="rate(runManager, run, member)">评价 {{ member.nickname }}</button></div></article><p v-if="!runs.length" class="empty-state">还没有场次记录。</p></div>
    </BaseModal>
  </section>
</template>

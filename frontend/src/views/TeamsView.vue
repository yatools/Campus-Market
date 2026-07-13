<script setup lang="ts">
import { onMounted, reactive, ref } from 'vue'
import { api, json } from '../api'
import BaseModal from '../components/BaseModal.vue'
import { useAuthStore } from '../stores/auth'
import type { Page, Team } from '../types'

const auth = useAuthStore()
const teams = ref<Team[]>([])
const loading = ref(true)
const error = ref('')
const composer = ref(false)
const runManager = ref<Team | null>(null)
const runs = ref<any[]>([])
const form = reactive({ game: '', mode: '', rank_requirement: '不限', capacity: 5, starts_at: '', recurrence: 'once', voice_name: '', voice_link: '', notes: '', reminder_minutes: 30 })

async function load() {
  loading.value = true
  try { teams.value = (await api<Page<Team>>('/teams?page_size=50')).items }
  catch (e) { error.value = e instanceof Error ? e.message : '车队加载失败' }
  finally { loading.value = false }
}

function createOpen() { if (auth.requireLogin()) composer.value = true }
async function create() {
  await api('/teams', json('POST', { ...form, starts_at: new Date(form.starts_at).toISOString() }))
  composer.value = false
  Object.assign(form, { game: '', mode: '', rank_requirement: '不限', capacity: 5, starts_at: '', recurrence: 'once', voice_name: '', voice_link: '', notes: '', reminder_minutes: 30 })
  await load()
}
async function join(team: Team) { if (auth.requireLogin()) { await api(`/teams/${team.id}/join`, json('POST')); await load() } }
async function leave(team: Team) { if (confirm('确定下车吗？临近发车且未请假会扣 3 信用。')) { await api(`/teams/${team.id}/leave`, json('POST')); await auth.load(); await load() } }
async function excuse(team: Team) { if (team.next_run) { await api(`/teams/${team.id}/runs/${team.next_run.id}/excuse`, json('POST')); await load() } }
async function checkIn(team: Team) { if (team.next_run) { await api(`/teams/${team.id}/runs/${team.next_run.id}/check-in`, json('POST')); await auth.load(); await load() } }
async function cancel(team: Team) { if (confirm('取消后所有成员都会收到通知，确定继续吗？')) { await api(`/teams/${team.id}/cancel`, json('POST')); await load() } }
async function editTeam(team: Team) {
  const mode = prompt('车队模式：', team.mode); if (mode === null) return
  const notes = prompt('车头注意事项：', team.notes); if (notes === null) return
  const capacity = Number(prompt('容量：', String(team.capacity))); if (!Number.isFinite(capacity)) return
  await api(`/teams/${team.id}`, json('PATCH', { mode, notes, capacity })); await load()
}
async function removeMember(team: Team, member: { id: number; nickname: string }) {
  if (!confirm(`确定移除 ${member.nickname}？`)) return
  await api(`/teams/${team.id}/members/${member.id}/remove`, json('POST')); await load()
}
async function transfer(team: Team, member: { id: number; nickname: string }) {
  if (!confirm(`确定将车头转让给 ${member.nickname}？`)) return
  await api(`/teams/${team.id}/transfer`, json('POST', { user_id: member.id })); await load()
}
async function openRuns(team: Team) {
  runManager.value = team
  runs.value = (await api<Page<any>>(`/teams/${team.id}/runs?page_size=100`)).items
}
async function createRun(team: Team) {
  const value = prompt('新场次发车时间（例如 2026-07-20 20:00）：', '')
  if (!value) return
  await api(`/teams/${team.id}/runs`, json('POST', { starts_at: new Date(value).toISOString() })); await openRuns(team); await load()
}
async function editRun(team: Team, run: any) {
  const value = prompt('新的发车时间：', new Date(run.starts_at).toLocaleString())
  if (!value) return
  await api(`/teams/${team.id}/runs/${run.id}`, json('PATCH', { starts_at: new Date(value).toISOString() })); await openRuns(team); await load()
}
async function cancelRun(team: Team, run: any) {
  if (!confirm('只取消这个场次？车队本身会继续保留。')) return
  await api(`/teams/${team.id}/runs/${run.id}`, json('PATCH', { status: 'cancelled' })); await openRuns(team); await load()
}
async function rate(team: Team, run: any, member: { id: number; nickname: string }) {
  const raw = prompt(`评价 ${member.nickname}（friendly / communication / skill / punctual，可逗号分隔）：`, 'friendly')
  if (!raw) return
  const tags = raw.split(/[,，]/).map((x) => x.trim()).filter(Boolean)
  await api(`/teams/${team.id}/runs/${run.id}/ratings`, json('POST', { target_user_id: member.id, tags }))
  error.value = '评价已记录。'
}
function time(value: string) { return new Date(value).toLocaleString('zh-CN', { month: 'numeric', day: 'numeric', hour: '2-digit', minute: '2-digit' }) }
onMounted(load)
</script>

<template>
  <section>
    <header class="page-head with-action"><div><h1>游戏车队</h1><p>按场次签到、请假和评价；语音链接只向当前成员展示。</p></div><button class="button primary" @click="createOpen">发布车队</button></header>
    <p v-if="error" class="notice danger">{{ error }}</p>
    <p v-if="loading" class="empty">正在寻找队友…</p>
    <div v-else class="stack">
      <p v-if="!teams.length" class="empty">暂时没有待发车队，来当第一个车头。</p>
      <article v-for="team in teams" :key="team.id" class="card ticket">
        <div class="card-head"><div><div class="meta"><span class="badge blue">{{ team.game }}</span><span v-if="team.recurrence === 'weekly'" class="badge">每周固定</span><span>{{ team.rank_requirement }}</span></div><h2>{{ team.mode }}</h2></div><div style="text-align:right"><strong>{{ team.member_count }}/{{ team.capacity }}</strong><div class="muted">车头 {{ team.owner.nickname }}</div></div></div>
        <p v-if="team.next_run"><strong>{{ time(team.next_run.starts_at) }}</strong> 发车 · 提前 {{ team.reminder_minutes }} 分钟提醒</p>
        <p v-if="team.notes" class="notice info">{{ team.notes }}</p>
        <p class="meta"><span>语音：{{ team.voice_name || '待车头通知' }}</span><a v-if="team.voice_link" :href="team.voice_link" target="_blank" rel="noopener">进入频道 ↗</a></p>
        <div class="seats"><i v-for="n in team.capacity" :key="n" :class="{ empty: n > team.member_count }" /></div>
        <div class="meta"><span v-for="member in team.members" :key="member.id">{{ member.nickname }}（{{ member.credit }}） <template v-if="team.mine && member.id !== team.owner.id"><button class="text-button" @click="removeMember(team, member)">移除</button><button class="text-button" @click="transfer(team, member)">转让</button></template></span></div>
        <div class="actions">
          <button v-if="!team.joined" @click="join(team)">确认上车</button>
          <template v-else-if="!team.mine"><button @click="checkIn(team)">场次签到</button><button @click="excuse(team)">本次请假</button><button @click="leave(team)">下车</button></template>
          <template v-if="team.mine"><button @click="editTeam(team)">编辑车队</button><button @click="createRun(team)">新增场次</button><button @click="cancel(team)">取消车队</button></template>
          <button v-if="team.joined" @click="openRuns(team)">场次记录</button>
        </div>
      </article>
    </div>
    <BaseModal v-if="composer" title="发布车队" wide @close="composer = false">
      <form class="form-grid" @submit.prevent="create"><label>游戏<input v-model.trim="form.game" required maxlength="60" /></label><label>模式<input v-model.trim="form.mode" required maxlength="80" /></label><label>段位要求<input v-model.trim="form.rank_requirement" /></label><label>容量<input v-model.number="form.capacity" type="number" min="2" max="99" /></label><label>发车时间<input v-model="form.starts_at" type="datetime-local" required /></label><label>重复<select v-model="form.recurrence"><option value="once">单次</option><option value="weekly">每周固定</option></select></label><label>语音工具<input v-model.trim="form.voice_name" placeholder="KOOK / QQ 群语音" /></label><label>频道链接<input v-model.trim="form.voice_link" type="url" /></label><label>提前提醒（分钟）<input v-model.number="form.reminder_minutes" type="number" min="5" max="1440" /></label><label class="full">车头注意事项<textarea v-model="form.notes" rows="4" /></label><button class="button primary full">发布并创建首场</button></form>
    </BaseModal>
    <BaseModal v-if="runManager" title="场次记录与赛后评价" wide @close="runManager = null">
      <div class="stack"><article v-for="run in runs" :key="run.id" class="card compact"><div class="card-head"><div><strong>{{ time(run.starts_at) }}</strong><p class="muted">{{ run.status }} · {{ run.member_count }} 名场次成员<span v-if="run.my_status"> · 我的状态 {{ run.my_status }}</span></p></div><div v-if="runManager.mine && run.status === 'scheduled'" class="actions"><button @click="editRun(runManager, run)">改时间</button><button @click="cancelRun(runManager, run)">取消本场</button></div></div><div v-if="new Date(run.starts_at).getTime() < Date.now()" class="actions"><button v-for="member in runManager.members.filter((x) => x.id !== auth.user?.id)" :key="member.id" @click="rate(runManager, run, member)">评价 {{ member.nickname }}</button></div></article><p v-if="!runs.length" class="empty">还没有场次记录。</p></div>
    </BaseModal>
  </section>
</template>

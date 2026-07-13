<script setup lang="ts">
import { computed, onMounted, reactive, ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { api, json, uploadImage } from '../api'
import BaseModal from '../components/BaseModal.vue'
import CommentThread from '../components/CommentThread.vue'
import RichText from '../components/RichText.vue'
import { useAuthStore } from '../stores/auth'
import type { Page } from '../types'

const route = useRoute(), router = useRouter(), auth = useAuthStore()
const sections = [
  ['questions', '问答'], ['handbook', '生存手册'], ['courses', '课程评价'], ['listings', '二手集市'],
  ['activities', '校园活动'], ['lost', '失物招领'], ['observe', '文明观察台'], ['governance', '治理公示'], ['announcements', '公告'],
]
const section = computed(() => sections.some((x) => x[0] === route.params.section) ? String(route.params.section) : 'questions')
const items = ref<any[]>([])
const currentPage = ref(1), pageSize = ref(20), total = ref(0)
const loading = ref(true), error = ref(''), modal = ref(false), action = ref('create'), target = ref<any>(null), comments = ref<number | null>(null)
const form = reactive<any>({})

const endpoint: Record<string, string> = {
  questions: '/questions?page_size=50', handbook: '/handbook?page_size=50', courses: '/course-offerings', listings: '/listings?page_size=50',
  activities: '/activities', lost: '/lost-items', observe: '/observe-posts?page_size=50', governance: '/penalties', announcements: '/announcements',
}

function resetForm() { Object.keys(form).forEach((key) => delete form[key]); form.attachments = [] }
async function load() {
  loading.value = true; error.value = ''
  try {
    const base = endpoint[section.value]
    const response = await api<Page<any>>(`${base}${base.includes('?') ? '&' : '?'}page=${currentPage.value}`)
    items.value = response.items; pageSize.value = response.page_size; total.value = response.total
    if (section.value === 'questions') {
      items.value = await Promise.all(items.value.map((item) => api(`/questions/${item.id}`)))
    }
  }
  catch (e) { error.value = e instanceof Error ? e.message : '加载失败' }
  finally { loading.value = false }
}
function navigate(name: string) { currentPage.value = 1; router.push(`/explore/${name}`) }
function openCreate() {
  if (!auth.requireLogin()) return
  resetForm(); action.value = 'create'; target.value = null
  Object.assign(form, section.value === 'questions' ? { category: '其他', bounty_xp: 0, tags: '' }
    : section.value === 'handbook' ? { category: '新生入学指南', draft: false }
    : section.value === 'courses' ? { offering_id: items.value[0]?.id, rating: 5, tags: '' }
    : section.value === 'listings' ? { category: '数码', price: 0, condition: '九成新', location: '校内面交' }
    : section.value === 'activities' ? { category: '找搭子', capacity: 10 }
    : section.value === 'lost' ? { kind: 'lost' }
    : section.value === 'observe' ? {} : {})
  modal.value = true
}
function openAnswer(item: any) { if (auth.requireLogin()) { resetForm(); action.value = 'answer'; target.value = item; modal.value = true } }
function openClaim(item: any) { if (auth.requireLogin()) { resetForm(); action.value = 'claim'; target.value = item; modal.value = true } }
async function openClaims(item: any) { if (auth.requireLogin()) { resetForm(); target.value = item; target.value.claims = (await api<Page<any>>(`/lost-items/${item.id}/claims`)).items; action.value = 'claims'; modal.value = true } }
function openAppeal(item: any) { if (auth.requireLogin()) { resetForm(); action.value = 'appeal'; target.value = item; modal.value = true } }
function openResponse(item: any) { if (auth.requireLogin()) { resetForm(); action.value = 'respond'; target.value = item; modal.value = true } }
function openListingEdit(item: any) { if (auth.requireLogin()) { resetForm(); action.value = 'edit_listing'; target.value = item; Object.assign(form, { category: item.category, title: item.title, description: item.description, price: item.price, condition: item.condition, location: item.location, attachments: [] }); modal.value = true } }
function inputDate(value: string | null) { if (!value) return ''; const date = new Date(value); return new Date(date.getTime() - date.getTimezoneOffset() * 60000).toISOString().slice(0, 16) }
function openEdit(item: any) {
  if (!auth.requireLogin()) return
  resetForm(); action.value = 'edit'; target.value = item
  if (section.value === 'questions') Object.assign(form, { title: item.title, body: item.body, category: item.category, tags: item.tags?.join(',') || '' })
  else if (section.value === 'handbook') Object.assign(form, { title: item.title, body: item.body, category: item.category })
  else if (section.value === 'activities') Object.assign(form, { title: item.title, body: item.body, category: item.category, location: item.location, capacity: item.capacity, starts_at: inputDate(item.starts_at), ends_at: inputDate(item.ends_at) })
  else if (section.value === 'lost') Object.assign(form, { kind: item.kind, item_name: item.item_name, description: item.description, location: item.location, happened_at: inputDate(item.happened_at) })
  modal.value = true
}

async function images(event: Event) {
  const input = event.target as HTMLInputElement
  for (const file of [...(input.files || [])].slice(0, 9)) form.attachments.push(await uploadImage(file))
}

async function submit() {
  try {
    if (action.value === 'answer') await api(`/questions/${target.value.id}/answers`, json('POST', { body: form.body }))
    else if (action.value === 'claim') await api(`/lost-items/${target.value.id}/claims`, json('POST', { message: form.message }))
    else if (action.value === 'appeal') await api(`/penalties/${target.value.id}/appeals`, json('POST', { reason: form.reason }))
    else if (action.value === 'respond') await api(`/observe-posts/${target.value.id}/response`, json('POST', { body: form.body }))
    else if (action.value === 'edit_listing') await api(`/listings/${target.value.id}`, json('PATCH', { ...form, price: Number(form.price), attachment_ids: form.attachments.map((x: any) => x.id) }))
    else if (action.value === 'edit' && section.value === 'questions') await api(`/questions/${target.value.id}`, json('PATCH', { ...form, tags: String(form.tags || '').split(',').filter(Boolean) }))
    else if (action.value === 'edit' && section.value === 'handbook') await api(`/handbook/${target.value.id}`, json('PATCH', { ...form, attachment_ids: form.attachments.map((x: any) => x.id) }))
    else if (action.value === 'edit' && section.value === 'activities') await api(`/activities/${target.value.id}`, json('PATCH', { ...form, capacity: form.capacity ? Number(form.capacity) : null, starts_at: new Date(form.starts_at).toISOString(), ends_at: form.ends_at ? new Date(form.ends_at).toISOString() : null }))
    else if (action.value === 'edit' && section.value === 'lost') await api(`/lost-items/${target.value.id}`, json('PATCH', { ...form, happened_at: form.happened_at ? new Date(form.happened_at).toISOString() : null, attachment_ids: form.attachments.map((x: any) => x.id) }))
    else if (section.value === 'questions') await api('/questions', json('POST', { ...form, tags: String(form.tags || '').split(',').filter(Boolean), bounty_xp: Number(form.bounty_xp) }))
    else if (section.value === 'handbook') await api('/handbook', json('POST', { ...form, attachment_ids: form.attachments.map((x: any) => x.id) }))
    else if (section.value === 'courses') await api('/course-reviews', json('POST', { ...form, offering_id: Number(form.offering_id), rating: Number(form.rating), tags: String(form.tags || '').split(',').filter(Boolean) }))
    else if (section.value === 'listings') await api('/listings', json('POST', { ...form, price: Number(form.price), attachment_ids: form.attachments.map((x: any) => x.id) }))
    else if (section.value === 'activities') await api('/activities', json('POST', { ...form, capacity: form.capacity ? Number(form.capacity) : null, starts_at: new Date(form.starts_at).toISOString(), ends_at: form.ends_at ? new Date(form.ends_at).toISOString() : null }))
    else if (section.value === 'lost') await api('/lost-items', json('POST', { ...form, happened_at: form.happened_at ? new Date(form.happened_at).toISOString() : null, attachment_ids: form.attachments.map((x: any) => x.id) }))
    else if (section.value === 'observe') await api('/observe-posts', json('POST', form))
    modal.value = false; await auth.load(); await load()
  } catch (e) { error.value = e instanceof Error ? e.message : '提交失败' }
}
async function accept(question: any, answer: any) { await api(`/answers/${answer.id}/accept`, json('POST')); await load() }
async function favorite(item: any) { if (auth.requireLogin()) { await api(`/entities/${item.id}/favorite`, { method: 'PUT' }); await load() } }
async function activityJoin(item: any) { if (auth.requireLogin()) { await api(`/activities/${item.id}/membership`, { method: item.joined ? 'DELETE' : 'PUT' }); await load() } }
async function cancelActivity(item: any) { if (confirm('确定取消活动并通知所有成员？')) { await api(`/activities/${item.id}/cancel`, json('POST')); await load() } }
async function markRead(item: any) { if (auth.requireLogin()) { await api(`/announcements/${item.id}/read`, { method: 'PUT' }); item.read = true } }
async function messageSeller(item: any) {
  if (!auth.requireLogin()) return
  const text = prompt('给卖家发送第一条消息：', `你好，请问“${item.title}”还在吗？`)
  if (!text) return
  const result = await api<any>('/conversations', json('POST', { recipient_id: item.seller.id, context_type: 'listing', context_id: item.id, first_message: text }))
  router.push(`/messages/${result.conversation.id}`)
}
async function listingStatus(item: any, status: string) { await api(`/listings/${item.id}/status`, json('PATCH', { status })); await load() }
async function decideClaim(item: any, approve: boolean) { await api(`/lost-items/${target.value.id}/claims/${item.id}/decision`, json('POST', { approve })); await openClaims(target.value); await load() }
function local(value: string) { return value ? new Date(value).toLocaleString() : '—' }
watch(section, load)
onMounted(load)
</script>

<template>
  <section>
    <header class="page-head with-action"><div><h1>校园广场</h1><p>问答、经验、课程、交易和活动，都有清楚的发布与治理边界。</p></div><button v-if="!['governance','announcements'].includes(section)" class="button primary" @click="openCreate">发布</button></header>
    <nav class="section-tabs"><button v-for="tab in sections" :key="tab[0]" :class="{ active: section === tab[0] }" @click="navigate(tab[0])">{{ tab[1] }}</button></nav>
    <p v-if="error" class="notice danger">{{ error }}</p><p v-if="loading" class="empty">正在加载…</p><p v-else-if="!items.length" class="empty">这个板块还没有内容。</p>
    <div v-else class="stack">
      <template v-if="section === 'questions'">
        <article v-for="item in items" :key="item.id" class="card"><div class="meta"><span class="badge blue">{{ item.category }}</span><span v-if="item.bounty_xp" class="badge yellow">悬赏 {{ item.bounty_xp }} XP</span><span v-if="item.accepted_answer_id" class="badge">已采纳</span></div><h2>{{ item.title }}</h2><RichText :content="item.body" /><p class="meta">{{ item.author }} · {{ item.answer_count }} 个回答</p><div v-for="answer in item.answers || []" :key="answer.id" class="notice info" style="margin-top:8px"><strong>{{ answer.author }}</strong><RichText :content="answer.body" /><button v-if="item.mine && !item.accepted_answer_id" class="button small secondary" @click="accept(item, answer)">采纳</button></div><div class="actions"><button @click="openAnswer(item)">回答</button><button v-if="item.mine && !item.accepted_answer_id" @click="openEdit(item)">编辑</button><button @click="comments = comments === item.id ? null : item.id">讨论</button></div><CommentThread v-if="comments === item.id" :entity-id="item.id" /></article>
      </template>
      <template v-else-if="section === 'handbook'">
        <article v-for="item in items" :key="item.id" class="card"><div class="meta"><span class="badge">{{ item.category }}</span><span v-if="item.featured" class="badge red">精华</span><span>收藏 {{ item.favorite_count }}</span></div><h2>{{ item.title }}</h2><RichText :content="item.body" /><div class="media-grid"><img v-for="file in item.attachments" :key="file.id" :src="file.thumbnail_url" /></div><div class="actions"><button @click="favorite(item)">收藏</button><button v-if="item.mine" @click="openEdit(item)">编辑</button><button @click="comments = comments === item.id ? null : item.id">回帖</button></div><CommentThread v-if="comments === item.id" :entity-id="item.id" /></article>
      </template>
      <template v-else-if="section === 'courses'">
        <article v-for="item in items" :key="item.id" class="card"><div class="card-head"><div><h2>{{ item.course }}</h2><p class="meta">{{ item.teacher }} · {{ item.semester }} {{ item.section }}</p></div><div><strong v-if="item.score" style="font-size:28px;color:var(--green)">{{ item.score }}</strong><span v-else class="badge yellow">{{ item.score_hidden_reason }}</span></div></div><div v-for="review in item.reviews" :key="review.id" class="notice info" style="margin-top:8px"><strong>{{ '★'.repeat(review.rating) }}</strong> {{ review.body }}<p v-if="review.correction" class="muted">教职工更正：{{ review.correction }}</p></div></article>
      </template>
      <template v-else-if="section === 'listings'">
        <article v-for="item in items" :key="item.id" class="card"><div class="card-head"><div><span class="badge blue">{{ item.category }}</span><h2>{{ item.title }}</h2></div><strong style="font-size:24px;color:var(--red)">¥{{ item.price }}</strong></div><RichText :content="item.description" /><div class="media-grid"><img v-for="file in item.attachments" :key="file.id" :src="file.thumbnail_url" /></div><p class="meta">{{ item.condition }} · {{ item.location }} · 卖家信用 {{ item.seller.credit }} · 仅校内线下面交</p><div class="actions"><button v-if="!item.mine" @click="messageSeller(item)">私信卖家</button><button @click="favorite(item)">收藏 {{ item.favorite_count }}</button><button @click="comments = comments === item.id ? null : item.id">回帖</button><template v-if="item.mine"><button @click="openListingEdit(item)">编辑</button><button @click="listingStatus(item,'reserved')">设为预留</button><button @click="listingStatus(item,'sold')">确认成交</button><button @click="listingStatus(item,'offline')">下架</button></template></div><CommentThread v-if="comments === item.id" :entity-id="item.id" /></article>
      </template>
      <template v-else-if="section === 'activities'">
        <article v-for="item in items" :key="item.id" class="card"><div class="meta"><span class="badge blue">{{ item.category }}</span><span>{{ local(item.starts_at) }}</span></div><h2>{{ item.title }}</h2><RichText :content="item.body" /><p class="meta">地点 {{ item.location }} · {{ item.member_count }}<template v-if="item.capacity">/{{ item.capacity }}</template> 人</p><div class="actions"><button v-if="!item.mine" @click="activityJoin(item)">{{ item.joined ? '退出活动' : '加入活动' }}</button><template v-if="item.mine"><button @click="openEdit(item)">编辑</button><button @click="cancelActivity(item)">取消活动</button></template><button @click="comments = comments === item.id ? null : item.id">回帖</button></div><CommentThread v-if="comments === item.id" :entity-id="item.id" /></article>
      </template>
      <template v-else-if="section === 'lost'">
        <article v-for="item in items" :key="item.id" class="card"><div class="meta"><span class="badge" :class="item.kind === 'lost' ? 'red' : ''">{{ item.kind === 'lost' ? '丢失' : '捡到' }}</span><span>{{ item.status }}</span></div><h2>{{ item.item_name }}</h2><RichText :content="item.description" /><p class="meta">地点 {{ item.location }} · {{ item.claim_count }} 个认领申请</p><div class="media-grid"><img v-for="file in item.attachments" :key="file.id" :src="file.thumbnail_url" /></div><div class="actions"><button v-if="!item.mine && item.status === 'open'" @click="openClaim(item)">提交认领线索</button><template v-if="item.mine"><button v-if="item.status === 'open'" @click="openEdit(item)">编辑</button><button v-if="item.claim_count" @click="openClaims(item)">处理认领申请</button></template><button @click="comments = comments === item.id ? null : item.id">回帖</button></div><CommentThread v-if="comments === item.id" :entity-id="item.id" /></article>
      </template>
      <template v-else-if="section === 'observe'">
        <article v-for="item in items" :key="item.id" class="card"><div class="meta"><span class="badge" :class="item.status === 'published' ? '' : 'yellow'">{{ item.status }}</span><span>默认隐私打码</span></div><h2>{{ item.title }}</h2><RichText :content="item.body" /><div v-if="item.response" class="notice info"><strong>指定回应方：</strong>{{ item.response }}</div><p v-if="item.admin_note" class="muted">审核备注：{{ item.admin_note }}</p><div v-if="item.status === 'published'" class="actions"><button v-if="item.respondent" @click="openResponse(item)">提交回应</button><button @click="comments = comments === item.id ? null : item.id">回帖</button></div><CommentThread v-if="comments === item.id" :entity-id="item.id" /></article>
      </template>
      <template v-else-if="section === 'governance'">
        <article v-for="item in items" :key="item.id" class="card"><div class="meta"><span class="badge red">治理公示</span><span>{{ local(item.created_at) }}</span></div><h2>{{ item.violation_type }}</h2><p>{{ item.user }} · {{ item.result }}</p><p class="muted">依据：{{ item.rule }}</p><div class="actions"><button @click="openAppeal(item)">申诉（仅责任人）</button></div></article>
      </template>
      <template v-else>
        <article v-for="item in items" :key="item.id" class="card"><div class="meta"><span class="badge" :class="item.level === 'strong' ? 'red' : ''">{{ item.level }}</span><span>{{ local(item.published_at) }}</span></div><h2>{{ item.title }}</h2><RichText :content="item.body" /><div class="actions"><button v-if="!item.read" @click="markRead(item)">确认已读</button><span v-else class="muted">已阅读 · 共 {{ item.read_count }} 人确认</span></div></article>
      </template>
    </div>
    <div v-if="total > pageSize" class="actions"><button :disabled="currentPage === 1" @click="currentPage--; load()">上一页</button><span>第 {{ currentPage }} 页 · 共 {{ total }} 条</span><button :disabled="currentPage * pageSize >= total" @click="currentPage++; load()">下一页</button></div>

    <BaseModal v-if="modal" :title="action === 'answer' ? '回答问题' : action === 'claim' ? '提交认领线索' : action === 'claims' ? '处理认领申请' : action === 'appeal' ? '提交申诉' : action === 'respond' ? '提交指定回应' : action === 'edit_listing' ? '编辑商品' : action === 'edit' ? '编辑内容' : '发布内容'" wide @close="modal = false">
      <form class="form-grid" @submit.prevent="submit">
        <template v-if="action === 'answer'"><label class="full">回答<textarea v-model="form.body" rows="8" required /></label></template>
        <template v-else-if="action === 'claim'"><label class="full">能帮助发布者核验的线索<textarea v-model="form.message" rows="6" required minlength="5" /></label></template>
        <template v-else-if="action === 'claims'"><div v-for="item in target.claims" :key="item.id" class="card compact full"><strong>{{ item.claimant }}</strong><p>{{ item.message }}</p><div v-if="item.status === 'pending'" class="actions"><button type="button" @click="decideClaim(item, true)">确认认领完成</button><button type="button" @click="decideClaim(item, false)">不通过</button></div><span v-else class="badge">{{ item.status }}</span></div><p v-if="!target.claims.length" class="empty full">暂无认领申请。</p></template>
        <template v-else-if="action === 'appeal'"><label class="full">申诉理由与证据<textarea v-model="form.reason" rows="8" required minlength="10" /></label></template>
        <template v-else-if="action === 'respond'"><label class="full">回应正文<textarea v-model="form.body" rows="8" required minlength="2" /></label></template>
        <template v-else-if="section === 'questions'"><label class="full">问题标题<input v-model="form.title" required /></label><label>分类<input v-model="form.category" /></label><label>悬赏 XP<input v-model.number="form.bounty_xp" type="number" min="0" max="500" /></label><label class="full">标签（逗号分隔）<input v-model="form.tags" /></label><label class="full">补充说明<textarea v-model="form.body" rows="6" /></label></template>
        <template v-else-if="section === 'handbook'"><label>分类<input v-model="form.category" required /></label><label class="check"><input v-model="form.draft" type="checkbox" /> 保存为草稿</label><label class="full">标题<input v-model="form.title" required /></label><label class="full">正文<textarea v-model="form.body" rows="10" required minlength="20" /></label><label class="full">图片<input type="file" accept="image/jpeg,image/png,image/webp" multiple @change="images" /></label></template>
        <template v-else-if="section === 'courses'"><label>课程班次<select v-model="form.offering_id"><option v-for="item in items" :key="item.id" :value="item.id">{{ item.course }} · {{ item.teacher }} · {{ item.semester }}</option></select></label><label>评分<input v-model.number="form.rating" type="number" min="1" max="5" /></label><label class="full">标签（逗号分隔）<input v-model="form.tags" /></label><label class="full">评价<textarea v-model="form.body" rows="7" required minlength="5" /></label></template>
        <template v-else-if="section === 'listings'"><label>分类<input v-model="form.category" required /></label><label>价格<input v-model.number="form.price" type="number" min="0" step="0.01" required /></label><label class="full">商品标题<input v-model="form.title" required /></label><label>成色<input v-model="form.condition" required /></label><label>面交地点<input v-model="form.location" required /></label><label class="full">详细描述<textarea v-model="form.description" rows="7" required /></label><label class="full">商品图片<input type="file" accept="image/jpeg,image/png,image/webp" multiple @change="images" /></label></template>
        <template v-else-if="section === 'activities'"><label>分类<input v-model="form.category" required /></label><label>人数上限<input v-model.number="form.capacity" type="number" min="2" /></label><label class="full">标题<input v-model="form.title" required /></label><label>开始时间<input v-model="form.starts_at" type="datetime-local" required /></label><label>结束时间<input v-model="form.ends_at" type="datetime-local" /></label><label class="full">地点<input v-model="form.location" required /></label><label class="full">详情<textarea v-model="form.body" rows="6" /></label></template>
        <template v-else-if="section === 'lost'"><label>类型<select v-model="form.kind"><option value="lost">我丢失了</option><option value="found">我捡到了</option></select></label><label>发生时间<input v-model="form.happened_at" type="datetime-local" /></label><label class="full">物品名称<input v-model="form.item_name" required /></label><label class="full">地点<input v-model="form.location" required /></label><label class="full">特征说明<textarea v-model="form.description" rows="6" /></label><label class="full">图片<input type="file" accept="image/jpeg,image/png,image/webp" multiple @change="images" /></label></template>
        <template v-else-if="section === 'observe'"><p class="notice info full">观察帖默认先审后发。请只描述事件，不公开可识别个人信息。</p><label class="full">事件标题<input v-model="form.title" required /></label><label class="full">事件描述<textarea v-model="form.body" rows="9" required minlength="10" /></label></template>
        <button v-if="action !== 'claims'" class="button primary full">提交</button>
      </form>
    </BaseModal>
  </section>
</template>

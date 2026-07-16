<script setup lang="ts">
import { computed, onMounted, reactive, ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { api, json, uploadImage } from '../api'
import AttachmentGrid from '../components/AttachmentGrid.vue'
import BaseModal from '../components/BaseModal.vue'
import CommentThread from '../components/CommentThread.vue'
import RichEditor from '../components/RichEditor.vue'
import RichText from '../components/RichText.vue'
import ExploreAnnouncements from '../features/explore/ExploreAnnouncements.vue'
import { exploreEndpoints, exploreSectionInfo, isExploreSection, type ExploreSection } from '../features/explore/registry'
import { useAuthStore } from '../stores/auth'
import type { Announcement, CampusService, CampusServiceRating, MarketListing, MarketOptions, MarketTransaction, Page } from '../types'
import { formatPrice, marketTransactionActions } from '../market'

const route = useRoute()
const router = useRouter()
const auth = useAuthStore()

const handbookCategories = [
  ['🎒', '新生入学指南'], ['🗓️', '选课指南'], ['🛏️', '宿舍避坑'], ['🍜', '食堂/外卖评价'],
  ['🗺️', '校园地图与隐藏地点'], ['🧪', '实验室/办事流程'], ['🏆', '奖学金/竞赛/保研/考研'],
  ['🎭', '社团体验'], ['🖨️', '打印/维修/快递'], ['🏥', '校医院攻略'], ['🎓', '毕业手续指南'], ['⭐', '校园服务评分'],
] as const
const activityCategories = ['全部', '社团招新', '讲座信息', '比赛组队', '拼车/拼单', '自习搭子', '运动搭子', '饭搭子', '实验招募', '问卷互填']

const section = computed<ExploreSection>(() => {
  const value = String(route.params.section)
  return isExploreSection(value) ? value : 'questions'
})
const currentInfo = computed(() => {
  const info = exploreSectionInfo[section.value]
  if (section.value === 'courses') return { ...info, subtitle: `基于课程体验 · 同一课程同一学期限评一次 · 需信用 ≥ ${auth.creditRule('threshold.course_review')}` }
  if (section.value === 'observe') return { ...info, subtitle: `只描述事件，不曝光个人 · 涉及具体个人/组织必须先人工审核 · 发帖需信用 ≥ ${auth.creditRule('threshold.observe_publish')}` }
  return info
})
const items = ref<any[]>([])
const currentPage = ref(1)
const pageSize = ref(20)
const total = ref(0)
const loading = ref(true)
const error = ref('')
const modal = ref(false)
const action = ref('create')
const target = ref<any>(null)
const detailItem = ref<any | null>(null)
const comments = ref<number | null>(null)
const form = reactive<any>({})
const handbookFilter = ref('')
const activityFilter = ref('全部')
const campusServices = ref<CampusService[]>([])
const serviceDetail = ref<CampusService | null>(null)
const serviceForm = reactive({ rating: 5, body: '' })
const marketOptions = ref<MarketOptions>({ categories: [], locations: [], conditions: [] })
const marketTransactions = ref<MarketTransaction[] | null>(null)
const transactionTitle = ref('我的交易')
const observeConfirmed = ref(false)

const handbookItems = computed(() => items.value.filter((item) => item.category === handbookFilter.value))
const activityItems = computed(() => activityFilter.value === '全部' ? items.value : items.value.filter((item) => item.category === activityFilter.value))
const observeThreshold = computed(() => auth.creditRule('threshold.observe_publish'))
const canPublishObserve = computed(() => Boolean(auth.user && auth.user.credit >= observeThreshold.value))
const observeCreditGap = computed(() => Math.max(0, observeThreshold.value - (auth.user?.credit || 0)))
const observePublishLabel = computed(() => !auth.user ? '+ 登录后发布' : canPublishObserve.value ? '+ 发布观察' : `信用不足（${auth.user.credit}/${observeThreshold.value}）`)
const modalTitle = computed(() => action.value === 'answer' ? '回答问题'
  : action.value === 'claim' ? '提交认领线索'
    : action.value === 'claims' ? '处理认领申请'
      : action.value === 'appeal' ? '提交申诉'
        : action.value === 'respond' ? '提交指定回应'
          : action.value === 'edit_listing' ? '编辑商品'
            : action.value === 'edit' ? '编辑内容' : '发布内容')

function resetForm() {
  Object.keys(form).forEach((key) => delete form[key])
  form.attachments = []
  form.body = ''
  form.description = ''
}

async function load() {
  loading.value = true
  error.value = ''
  try {
    const base = exploreEndpoints[section.value]
    const [response, services] = await Promise.all([
      api<Page<any>>(`${base}${base.includes('?') ? '&' : '?'}page=${currentPage.value}`),
      section.value === 'handbook' ? api<{ items: CampusService[] }>('/campus-services') : Promise.resolve({ items: [] as CampusService[] }),
    ])
    items.value = response.items
    pageSize.value = response.page_size
    total.value = response.total
    campusServices.value = services.items
    if (section.value === 'listings') marketOptions.value = await api<MarketOptions>('/market/options')
    if (detailItem.value) detailItem.value = items.value.find((item) => item.id === detailItem.value?.id) || null
  } catch (e) {
    error.value = e instanceof Error ? e.message : '加载失败'
  } finally {
    loading.value = false
  }
}

function openCreate() {
  if (!auth.requireLogin()) return
  if (section.value === 'observe' && !canPublishObserve.value) {
    error.value = `当前信用 ${auth.user?.credit || 0}，发布观察帖需要 ${observeThreshold.value}，还差 ${observeCreditGap.value} 分。`
    return
  }
  resetForm()
  observeConfirmed.value = false
  action.value = 'create'
  target.value = null
  Object.assign(form, section.value === 'questions' ? { category: '其他', bounty_xp: 0, tags: '' }
    : section.value === 'handbook' ? { category: handbookFilter.value || '新生入学指南', draft: false }
      : section.value === 'courses' ? { offering_id: items.value[0]?.id, rating: 5, tags: '' }
        : section.value === 'listings' ? { category_id: marketOptions.value.categories[0]?.id, price_yuan: 0, condition: 'excellent', negotiable: true, purchased_at: '', location_id: marketOptions.value.locations[0]?.id }
          : section.value === 'activities' ? { category: '找搭子', capacity: 10 }
            : section.value === 'lost' ? { kind: 'lost' }
              : {})
  modal.value = true
}

async function openDetail(item: any) { detailItem.value = section.value === 'questions' ? await api(`/questions/${item.id}`) : item; comments.value = null }
function closeDetail() { detailItem.value = null; comments.value = null }
function openAnswer(item: any) { if (auth.requireLogin()) { resetForm(); action.value = 'answer'; target.value = item; modal.value = true } }
function openClaim(item: any) { if (auth.requireLogin()) { resetForm(); action.value = 'claim'; target.value = item; modal.value = true } }
async function openClaims(item: any) {
  if (!auth.requireLogin()) return
  resetForm()
  target.value = item
  target.value.claims = (await api<Page<any>>(`/lost-items/${item.id}/claims`)).items
  action.value = 'claims'
  modal.value = true
}
function openAppeal(item: any) { if (auth.requireLogin()) { resetForm(); action.value = 'appeal'; target.value = item; modal.value = true } }
function openResponse(item: any) { if (auth.requireLogin()) { resetForm(); action.value = 'respond'; target.value = item; modal.value = true } }
function openListingEdit(item: any) {
  if (!auth.requireLogin()) return
  resetForm()
  action.value = 'edit_listing'
  target.value = item
  Object.assign(form, { category_id: item.category.id, title: item.title, description: item.description, price_yuan: item.price_cents / 100, condition: item.condition, negotiable: item.negotiable, purchased_at: item.purchased_at || '', location_id: item.location.id, attachments: [] })
  modal.value = true
}
function inputDate(value: string | null) {
  if (!value) return ''
  const date = new Date(value)
  return new Date(date.getTime() - date.getTimezoneOffset() * 60000).toISOString().slice(0, 16)
}
function openEdit(item: any) {
  if (!auth.requireLogin()) return
  resetForm()
  action.value = 'edit'
  target.value = item
  if (section.value === 'questions') Object.assign(form, { title: item.title, body: item.body, category: item.category, tags: item.tags?.join(',') || '' })
  else if (section.value === 'handbook') Object.assign(form, { title: item.title, body: item.body, category: item.category })
  else if (section.value === 'activities') Object.assign(form, { title: item.title, body: item.body, category: item.category, location: item.location, capacity: item.capacity, starts_at: inputDate(item.starts_at), ends_at: inputDate(item.ends_at) })
  else if (section.value === 'lost') Object.assign(form, { kind: item.kind, item_name: item.item_name, description: item.description, location: item.location, happened_at: inputDate(item.happened_at) })
  modal.value = true
}

async function submit() {
  try {
    const attachmentIds = form.attachments.map((item: any) => item.id)
    if (action.value === 'answer') await api(`/questions/${target.value.id}/answers`, json('POST', { body: form.body, attachment_ids: attachmentIds }))
    else if (action.value === 'claim') await api(`/lost-items/${target.value.id}/claims`, json('POST', { message: form.message }))
    else if (action.value === 'appeal') await api(`/penalties/${target.value.id}/appeals`, json('POST', { reason: form.reason }))
    else if (action.value === 'respond') await api(`/observe-posts/${target.value.id}/response`, json('POST', { body: form.body, attachment_ids: attachmentIds }))
    else if (action.value === 'edit_listing') await api(`/listings/${target.value.id}`, json('PATCH', { ...form, price_cents: Math.round(Number(form.price_yuan) * 100), price_yuan: undefined, purchased_at: form.purchased_at || null, attachment_ids: attachmentIds }))
    else if (action.value === 'edit' && section.value === 'questions') await api(`/questions/${target.value.id}`, json('PATCH', { ...form, tags: String(form.tags || '').split(',').filter(Boolean), attachment_ids: attachmentIds }))
    else if (action.value === 'edit' && section.value === 'handbook') await api(`/handbook/${target.value.id}`, json('PATCH', { ...form, attachment_ids: attachmentIds }))
    else if (action.value === 'edit' && section.value === 'activities') await api(`/activities/${target.value.id}`, json('PATCH', { ...form, capacity: form.capacity ? Number(form.capacity) : null, starts_at: new Date(form.starts_at).toISOString(), ends_at: form.ends_at ? new Date(form.ends_at).toISOString() : null, attachment_ids: attachmentIds }))
    else if (action.value === 'edit' && section.value === 'lost') await api(`/lost-items/${target.value.id}`, json('PATCH', { ...form, happened_at: form.happened_at ? new Date(form.happened_at).toISOString() : null, attachment_ids: attachmentIds }))
    else if (section.value === 'questions') await api('/questions', json('POST', { ...form, tags: String(form.tags || '').split(',').filter(Boolean), bounty_xp: Number(form.bounty_xp), attachment_ids: attachmentIds }))
    else if (section.value === 'handbook') await api('/handbook', json('POST', { ...form, attachment_ids: attachmentIds }))
    else if (section.value === 'courses') await api('/course-reviews', json('POST', { ...form, offering_id: Number(form.offering_id), rating: Number(form.rating), tags: String(form.tags || '').split(',').filter(Boolean), attachment_ids: attachmentIds }))
    else if (section.value === 'listings') await api('/listings', json('POST', { ...form, price_cents: Math.round(Number(form.price_yuan) * 100), price_yuan: undefined, purchased_at: form.purchased_at || null, attachment_ids: attachmentIds }))
    else if (section.value === 'activities') await api('/activities', json('POST', { ...form, capacity: form.capacity ? Number(form.capacity) : null, starts_at: new Date(form.starts_at).toISOString(), ends_at: form.ends_at ? new Date(form.ends_at).toISOString() : null, attachment_ids: attachmentIds }))
    else if (section.value === 'lost') await api('/lost-items', json('POST', { ...form, happened_at: form.happened_at ? new Date(form.happened_at).toISOString() : null, attachment_ids: attachmentIds }))
    else if (section.value === 'observe') await api('/observe-posts', json('POST', { ...form, attachment_ids: attachmentIds }))
    modal.value = false
    await auth.load()
    await load()
  } catch (e) {
    error.value = e instanceof Error ? e.message : '提交失败'
  }
}

async function accept(answer: any) { await api(`/answers/${answer.id}/accept`, json('POST')); await load() }
async function favorite(item: any) { if (auth.requireLogin()) { await api(`/entities/${item.id}/favorite`, { method: 'PUT' }); await load() } }
async function activityJoin(item: any) { if (auth.requireLogin()) { await api(`/activities/${item.id}/membership`, { method: item.joined ? 'DELETE' : 'PUT' }); await load() } }
async function cancelActivity(item: any) { if (confirm('确定取消活动并通知所有成员？')) { await api(`/activities/${item.id}/cancel`, json('POST')); await load() } }
async function markRead(item: Announcement) { if (auth.requireLogin()) { await api(`/announcements/${item.id}/read`, { method: 'PUT' }); item.read = true } }
async function messageSeller(item: any) {
  if (!auth.requireLogin()) return
  const text = prompt('给卖家发送第一条消息：', `你好，请问“${item.title}”还在吗？`)
  if (!text) return
  const result = await api<any>('/conversations', json('POST', { recipient_id: item.seller.id, context_type: 'listing', context_id: item.id, first_message: text }))
  router.push(`/messages/${result.conversation.id}`)
}
function transactionActions(item: MarketTransaction) { return marketTransactionActions(item, auth.user?.id || 0) }
function transactionLeft(value: string | null) { if (!value) return ''; const ms = new Date(value).getTime() - Date.now(); if (ms <= 0) return '已超时'; const hours = Math.floor(ms / 3600000); const minutes = Math.floor((ms % 3600000) / 60000); return `${hours} 小时 ${minutes} 分` }
async function requestReservation(item: MarketListing) { if (!auth.requireLogin()) return; const message = prompt('给卖家的预订留言：', `希望预订“${item.title}”，可校内面交。`) ?? ''; await api(`/listings/${item.id}/transactions`, json('POST', { message })); await showMyTransactions() }
async function showListingTransactions(item: MarketListing) { transactionTitle.value = `“${item.title}”的预订申请`; marketTransactions.value = (await api<Page<MarketTransaction>>(`/listings/${item.id}/transactions`)).items }
async function showMyTransactions() { if (!auth.requireLogin()) return; transactionTitle.value = '我的买卖记录'; marketTransactions.value = (await api<Page<MarketTransaction>>('/me/market-transactions')).items }
async function transactionAction(item: MarketTransaction, action: 'accept' | 'reject' | 'confirm' | 'cancel') { const body = action === 'cancel' ? { reason: prompt('取消原因：', '') ?? '' } : undefined; await api(`/market-transactions/${item.id}/${action}`, json('POST', body)); await showMyTransactions(); await load() }
async function disputeTransaction(item: MarketTransaction) { const reason = prompt('请说明纠纷经过（至少 5 个字）：', '') ?? ''; if (!reason) return; const files = Array.from((document.querySelector('#market-evidence') as HTMLInputElement | null)?.files || []); const uploaded = await Promise.all(files.slice(0, 9).map((file) => uploadImage(file, 'market_dispute'))); await api(`/market-transactions/${item.id}/disputes`, json('POST', { reason, attachment_ids: uploaded.map((entry) => entry.id) })); await showMyTransactions() }
async function reviewTransaction(item: MarketTransaction) { const rating = Number(prompt('评分（1-5）：', '5')); const body = prompt('评价（选填）：', '') ?? ''; await api(`/market-transactions/${item.id}/reviews`, json('POST', { rating, body })); await showMyTransactions() }
async function cancelListing(item: MarketListing) { if (!confirm('确定取消这件商品？完成交易后不能重新上架。')) return; await api(`/listings/${item.id}/cancel`, json('POST', { reason: '卖家主动取消' })); detailItem.value = null; await load() }
async function decideClaim(item: any, approve: boolean) { await api(`/lost-items/${target.value.id}/claims/${item.id}/decision`, json('POST', { approve })); await openClaims(target.value); await load() }

function selectHandbookCategory(name: string) {
  if (name === '校园服务评分') {
    document.querySelector('#campus-service-panel')?.scrollIntoView({ behavior: 'smooth', block: 'center' })
    return
  }
  handbookFilter.value = name
}
async function openService(service: CampusService) {
  serviceDetail.value = await api<CampusService>(`/campus-services/${service.id}`)
  Object.assign(serviceForm, { rating: 5, body: '' })
}
async function rateService() {
  if (!serviceDetail.value || !auth.requireLogin()) return
  try {
    await api(`/campus-services/${serviceDetail.value.id}/ratings`, json('POST', serviceForm))
    await load()
    await openService(serviceDetail.value)
  } catch (e) {
    error.value = e instanceof Error ? e.message : '评价失败'
  }
}
async function respondService(rating: CampusServiceRating) {
  const body = prompt('填写服务方公开回应：', '')
  if (!body) return
  await api(`/campus-service-ratings/${rating.id}/response`, json('POST', { body }))
  if (serviceDetail.value) await openService(serviceDetail.value)
}

function local(value: string) { return value ? new Date(value).toLocaleString('zh-CN') : '—' }
function shortDate(value: string) { return value ? new Date(value).toLocaleDateString('zh-CN', { month: '2-digit', day: '2-digit' }) : '—' }
function categoryIcon(value: string) { return Object.fromEntries(handbookCategories.map(([icon, name]) => [name, icon]))[value] || '📌' }
function statusLabel(value: string) { return ({ open: '待认领', claimed: '已认领', published: '审核通过', pending: '人工审核中' } as Record<string, string>)[value] || value }

async function consumeCreate() {
  if (route.query.create !== '1' || ['governance', 'announcements'].includes(section.value)) return
  openCreate()
  const query = { ...route.query }
  delete query.create
  await router.replace({ path: route.path, query })
}

watch(section, async () => {
  handbookFilter.value = ''
  detailItem.value = null
  activityFilter.value = '全部'
  currentPage.value = 1
  await load()
  await consumeCreate()
})
watch(() => route.query.create, consumeCreate)
onMounted(async () => { await load(); await consumeCreate() })
</script>

<template>
  <section class="explore-page-v4" :class="`section-${section}`">
    <header v-if="section === 'listings'" class="page-head v4-page-head-action"><div><h2>🛒 二手集市</h2><p>{{ currentInfo.subtitle }}</p></div><div class="actions"><button class="btn ghost" @click="showMyTransactions">我的买卖</button><button class="btn primary" @click="openCreate">+ 发布出售</button></div></header>
    <header v-else-if="section === 'lost'" class="page-head v4-page-head-action"><div><h2>🧣 失物招领</h2><p>{{ currentInfo.subtitle }}</p></div><button class="btn primary" @click="openCreate">+ 登记</button></header>
    <header v-else-if="section === 'observe'" class="page-head v4-page-head-action"><div><h2>{{ currentInfo.icon }} {{ currentInfo.title }}</h2><p>{{ currentInfo.subtitle }}</p></div><div class="observe-publish-action"><button class="btn primary" :disabled="Boolean(auth.user) && !canPublishObserve" @click="openCreate">{{ observePublishLabel }}</button><small v-if="auth.user && !canPublishObserve" class="muted">还差 {{ observeCreditGap }} 信用分</small></div></header>
    <header v-else class="page-head"><h2>{{ currentInfo.icon }} {{ currentInfo.title }}</h2><p>{{ currentInfo.subtitle }}</p></header>

    <p v-if="error" class="notice danger">{{ error }}</p>
    <p v-if="loading" class="empty-state">正在加载…</p>

    <template v-else-if="section === 'questions'">
      <p v-if="!items.length" class="empty-state">还没有问题，使用顶部“发帖”提出第一个问题。</p>
      <article v-for="item in items" :key="item.id" class="card qa-card-v4">
        <div class="v4-tag-row"><span v-if="item.bounty_xp" class="tag red">悬赏 {{ item.bounty_xp }} 分</span><span class="tag gray">{{ item.category }}</span><span v-for="tag in item.tags || []" :key="tag" class="tag gray">{{ tag }}</span><span v-if="item.accepted_answer_id" class="tag green">✅ 已采纳</span><span v-else class="tag yellow">等待回答 · {{ item.answer_count }} 个回答</span></div>
        <button class="v4-title-button" @click="openDetail(item)">{{ item.title }}</button>
        <div v-for="answer in (item.answers || []).filter((row: any) => row.id === item.accepted_answer_id)" :key="answer.id" class="best-answer-v4"><b>✅ 最佳答案</b>（{{ answer.author }}）：<RichText :content="answer.body" /><div class="muted">采纳 +20 经验 · 高价值内容可转入生存手册</div></div>
        <button class="v4-detail-link" @click="openDetail(item)">查看问题与全部回答 →</button>
      </article>
      <div class="rulebox"><b>问答区机制：</b>问题 = 标题 + 标签 + 分类（学院/校区/课程/宿舍/行政事务）+ 悬赏积分。回答被采纳 +20 经验；累计 10 次采纳解锁「答疑达人」头衔。</div>
    </template>

    <template v-else-if="section === 'handbook'">
      <template v-if="!handbookFilter">
        <div class="hb-grid">
          <button v-for="entry in handbookCategories" :key="entry[1]" class="hb-item" @click="selectHandbookCategory(entry[1])"><span class="ico">{{ entry[0] }}</span>{{ entry[1] }}</button>
        </div>
        <div class="card handbook-reward-v4"><h3>🏅 贡献奖励体系</h3><p><span v-for="tag in ['经验值','贡献者头衔','精华作者','学院向导','新生导师','答疑达人','年度校园百科贡献者']" :key="tag" class="tag yellow">{{ tag }}</span></p><p class="muted">防灌水机制：单纯发帖不加经验。经验只来自 <b>被收藏 +2 / 回答被采纳 +20 / 管理员加精 +50 / 内容存续满一年且仍被访问 +30</b>。</p></div>
        <div id="campus-service-panel" class="card campus-service-panel"><h3>⭐ 校园服务评分（防恶意差评设计）</h3><div v-if="campusServices.length" class="campus-service-inline"><button v-for="service in campusServices" :key="service.id" @click="openService(service)"><span>{{ service.name }}</span><b v-if="service.score !== null" class="mono">{{ service.score }}</b><small>（{{ service.rating_count }} 条）</small></button></div><p v-else class="muted">管理员尚未录入可评分的校园服务。</p><p class="muted">同一用户对同一服务 30 天内只能评一次；低分需附具体事由；服务方可回应但不可删评。</p></div>
      </template>
      <template v-else>
        <div class="handbook-drill-head"><button class="btn ghost sm" @click="handbookFilter = ''">← 返回分类</button><h3>{{ categoryIcon(handbookFilter) }} {{ handbookFilter }}</h3><button class="btn primary sm" @click="openCreate">+ 投稿</button></div>
        <p v-if="!handbookItems.length" class="empty-state">这个分类还没有公开经验，欢迎投稿。</p>
        <article v-for="item in handbookItems" :key="item.id" class="post handbook-entry-v4" @click="openDetail(item)"><div class="p-head"><span class="tag green">{{ item.category }}</span><span v-if="item.featured" class="stamp-badge">精华</span><span class="p-time">⭐ 收藏 {{ item.favorite_count }}</span></div><div class="p-title">{{ item.title }}</div><div class="p-body handbook-preview"><RichText :content="item.body" /></div></article>
      </template>
    </template>

    <template v-else-if="section === 'courses'">
      <p v-if="!items.length" class="empty-state">还没有可评价的课程班次。</p>
      <article v-for="item in items" :key="item.id" class="card course-summary-v4" @click="openDetail(item)"><div><b>{{ item.course }}（{{ item.teacher }}班）</b><p class="muted">{{ item.semester }} · {{ item.section }} · {{ item.review_count }} 条评价</p><p v-if="item.tags?.length" class="course-tags-v4">高频标签：<span v-for="tag in item.tags" :key="tag" class="tag green">{{ tag }}</span></p></div><div class="course-score-v4"><div v-if="item.score" class="score-big">{{ item.score }}</div><div v-if="item.score" class="muted">综合评分</div><span v-else class="score-hidden">{{ item.score_hidden_reason }}</span></div></article>
      <div class="rulebox"><b>课评规则：</b><ul><li>禁止人身攻击、恶意造谣；禁止公开老师/助教私人联系方式。</li><li>评价必须基于课程体验，同一课程同一学期只能评价一次。</li><li>老师/助教可提交「事实更正」，附在原评论下方，但不能直接删评。</li></ul></div>
    </template>

    <template v-else-if="section === 'listings'">
      <div class="dangerbox">🚫 <b>禁售清单：</b>烟酒、处方药、管制刀具、活体动物、代写代考服务、账号买卖及其他法律法规禁止物品。平台不经手资金、不收取费用，也不提供担保。</div>
      <p v-if="!items.length" class="empty-state">还没有在售物品。</p>
      <article v-for="item in items" :key="item.id" class="card market-card-v4">
        <div class="market-media-v4"><img v-if="item.attachments?.length" :src="item.attachments[0].thumbnail_url" :alt="item.title" loading="lazy" /><span v-else>📦</span></div>
        <div class="market-main-v4"><div class="v4-tag-row"><b class="market-title-v4">{{ item.title }}</b><span v-if="item.negotiable" class="tag green">可小刀</span><span class="tag blue">{{ item.category.name }}</span><span class="tag" :class="item.trade_status === 'available' ? 'green' : 'yellow'">{{ item.trade_status === 'available' ? '在售' : '已预留' }}</span></div><div class="market-price-v4">¥{{ formatPrice(item.price_cents) }}</div><table class="market-facts-v4"><tbody><tr><td>成色</td><td>{{ item.condition_label }}</td></tr><tr><td>购买时间</td><td>{{ item.purchased_at || '未填写' }}</td></tr><tr><td>瑕疵/说明</td><td><span class="market-description-clamp">{{ item.description }}</span></td></tr><tr><td>交付地点</td><td>{{ item.location.name }}</td></tr></tbody></table></div>
        <aside class="market-seller-v4"><b>卖家信用</b><div class="market-seller-name"><span>{{ item.seller.nickname.slice(0, 1) }}</span><strong>{{ item.seller.nickname }}</strong></div><p>✅ {{ item.seller.verified ? '校园身份已认证' : '身份待核验' }}</p><p>🪪 信用 <b class="mono">{{ item.seller.credit }}</b></p><p>📦 真实成交 <b class="mono">{{ item.seller.completed_sales }}</b> 单</p><p>⭐ {{ item.seller.rating_count ? `${item.seller.rating_average.toFixed(1)}（${item.seller.rating_count}）` : '暂无交易评价' }}</p><button v-if="!item.mine && item.trade_status === 'available'" class="btn primary sm" @click="requestReservation(item)">申请预订</button><button v-if="!item.mine" class="btn ghost sm" @click="messageSeller(item)">先私信</button><button v-else class="btn ghost sm" @click="openDetail(item)">管理商品</button></aside>
      </article>
      <div class="rulebox"><b>线下交易提醒：</b>请在校内公共场所当面验货后交易；平台不保管资金、不收取服务费。遇到纠纷可举报并提交站内聊天记录。</div>
    </template>

    <template v-else-if="section === 'activities'">
      <div class="activity-filter-v4"><button v-for="category in activityCategories" :key="category" class="chip" :class="{ on: activityFilter === category }" @click="activityFilter = category">{{ category }}</button></div>
      <p v-if="!activityItems.length" class="empty-state">这个分类还没有活动。</p>
      <article v-for="item in activityItems" :key="item.id" class="post activity-post-v4" @click="openDetail(item)"><div class="p-head"><div class="p-avatar">{{ item.title.slice(0, 1) }}</div><span class="p-name">{{ item.author || '校园同学' }}</span><span class="tag blue">{{ item.category }}</span><span class="p-time">{{ shortDate(item.starts_at) }}</span></div><div class="p-title">{{ item.title }}</div><div class="p-body">{{ item.location }} · {{ item.member_count }}<template v-if="item.capacity">/{{ item.capacity }}</template> 人</div><div class="p-foot"><span>🙋 {{ item.joined ? '已加入' : '加入' }}</span><span>💬 回帖</span></div></article>
    </template>

    <template v-else-if="section === 'lost'">
      <p v-if="!items.length" class="empty-state">还没有失物登记。</p>
      <article v-for="item in items" :key="item.id" class="post lost-post-v4" @click="openDetail(item)"><div class="p-head"><span class="tag" :class="item.kind === 'lost' ? 'red' : 'green'">{{ item.kind === 'lost' ? '丢失' : '捡到' }}</span><span class="tag" :class="item.status === 'open' ? 'yellow' : 'blue'">{{ statusLabel(item.status) }}</span><span class="p-time">{{ local(item.happened_at || item.created_at) }}</span></div><div class="p-title">{{ item.kind === 'lost' ? '🪪' : '🎧' }} {{ item.item_name }}</div><div class="p-body">地点：{{ item.location }} ｜ {{ item.description }}</div></article>
    </template>

    <template v-else-if="section === 'observe'">
      <div class="dangerbox observe-rules-v4"><b>📋 本区须知（发帖前必读并勾选确认）</b><ul><li>只允许描述事件，<b>禁止</b>公开真实姓名、手机号、寝室、学号、照片、聊天记录中的个人信息。</li><li>涉及具体个人或组织的帖子，必须先进入人工审核。</li><li>被指认方拥有申诉与回应入口，回应将并列展示。</li><li>所有曝光内容默认打码；禁止「求扩散」「全校避雷某某人」等煽动性表达。</li><li>管理员只公示处理结果和规则依据，不公示隐私细节。</li></ul></div>
      <p v-if="!items.length" class="empty-state">还没有可见的观察记录。</p>
      <article v-for="item in items" :key="item.id" class="post observe-post-v4" @click="openDetail(item)"><div class="p-head"><div class="p-avatar">匿</div><span class="p-name">匿名同学</span><span class="tag" :class="item.status === 'published' ? 'green' : 'yellow'">{{ item.status === 'published' ? '✅ 审核通过' : '⏳ 人工审核中' }}</span><span v-if="item.response" class="tag blue">被指认方已回应</span><span class="p-time">{{ local(item.created_at) }}</span></div><div class="p-title">{{ item.title }}</div><div class="p-body"><RichText :content="item.body" /><template v-if="item.response"><b>指定回应：</b><RichText :content="item.response" /></template></div><div v-if="item.status !== 'published'" class="p-foot"><span class="muted">审核通过前仅发帖人可见</span></div></article>
    </template>

    <template v-else-if="section === 'governance'">
      <div class="card governance-card-v4"><div class="table-wrap"><table class="gov governance-table"><thead><tr><th>匿名化账号</th><th>违规类型</th><th>处理结果</th><th>规则依据</th><th>时间</th><th>申诉</th></tr></thead><tbody><tr v-for="item in items" :key="item.id"><td class="mono">{{ item.user }}</td><td>{{ item.violation_type }}</td><td>{{ item.result }}</td><td>{{ item.rule }}</td><td class="mono">{{ shortDate(item.created_at) }}</td><td><button class="tag" :class="item.appeal_status === 'pending' ? 'yellow' : 'green'" @click="openAppeal(item)">{{ item.appeal_status === 'pending' ? '申诉中' : '可申诉' }}</button></td></tr></tbody></table></div><p class="muted governance-footnote">📚 判例库：每条处罚附案例说明，供全体用户查阅引用，做到同案同判。</p><p v-if="!items.length" class="empty-state">暂无公开处罚记录。</p></div>
    </template>

    <template v-else-if="section === 'announcements'">
      <ExploreAnnouncements :items="items" :format-date="local" @read="markRead" />
    </template>

    <div v-if="total > pageSize" class="v4-pagination"><button class="btn ghost sm" :disabled="currentPage === 1" @click="currentPage--; load()">上一页</button><span>第 {{ currentPage }} 页 · 共 {{ total }} 条</span><button class="btn ghost sm" :disabled="currentPage * pageSize >= total" @click="currentPage++; load()">下一页</button></div>

    <BaseModal v-if="detailItem" :title="detailItem.title || detailItem.item_name || detailItem.course || '详情'" wide @close="closeDetail">
      <article class="v4-detail-content">
        <template v-if="section === 'questions'"><div class="v4-tag-row"><span class="tag red">悬赏 {{ detailItem.bounty_xp }} 分</span><span class="tag gray">{{ detailItem.category }}</span></div><RichText :content="detailItem.body" /><AttachmentGrid :content="detailItem.body" :attachments="detailItem.attachments" /><h3>回答</h3><div v-for="answer in detailItem.answers || []" :key="answer.id" class="answer-box" :class="{ accepted: detailItem.accepted_answer_id === answer.id }"><strong>{{ detailItem.accepted_answer_id === answer.id ? '✅ 最佳答案 · ' : '' }}{{ answer.author }}</strong><RichText :content="answer.body" /><AttachmentGrid :content="answer.body" :attachments="answer.attachments" /><button v-if="detailItem.mine && !detailItem.accepted_answer_id" class="btn ghost sm" @click="accept(answer)">采纳</button></div><div class="modal-actions-v4"><button class="btn primary" @click="openAnswer(detailItem)">回答问题</button><button v-if="detailItem.mine && !detailItem.accepted_answer_id" class="btn ghost" @click="openEdit(detailItem)">编辑</button></div><CommentThread :entity-id="detailItem.id" /></template>
        <template v-else-if="section === 'handbook'"><div class="v4-tag-row"><span class="tag green">{{ detailItem.category }}</span><span v-if="detailItem.featured" class="stamp-badge">精华</span><span class="muted">⭐ 收藏 {{ detailItem.favorite_count }}</span></div><RichText :content="detailItem.body" /><AttachmentGrid :content="detailItem.body" :attachments="detailItem.attachments" /><div class="modal-actions-v4"><button class="btn ghost" @click="favorite(detailItem)">收藏</button><button v-if="detailItem.mine" class="btn ghost" @click="openEdit(detailItem)">编辑</button></div><CommentThread :entity-id="detailItem.id" /></template>
        <template v-else-if="section === 'courses'"><div class="course-detail-score"><strong v-if="detailItem.score" class="score-big">{{ detailItem.score }}</strong><span v-else class="score-hidden">{{ detailItem.score_hidden_reason }}</span><p>{{ detailItem.teacher }} · {{ detailItem.semester }} {{ detailItem.section }}</p></div><div v-for="review in detailItem.reviews || []" :key="review.id" class="review-box"><strong>{{ '★'.repeat(review.rating) }}</strong><RichText :content="review.body" /><AttachmentGrid :content="review.body" :attachments="review.attachments" /><p v-if="review.correction" class="muted">教职工更正：{{ review.correction }}</p></div><div class="modal-actions-v4"><button class="btn primary" @click="openCreate">撰写课程评价</button></div></template>
        <template v-else-if="section === 'listings'"><div class="v4-tag-row"><span class="tag blue">{{ detailItem.category.name }}</span><span class="tag yellow">{{ detailItem.condition_label }}</span><span class="tag" :class="detailItem.moderation_status === 'approved' ? 'green' : 'yellow'">审核：{{ detailItem.moderation_status }}</span></div><strong class="market-price-v4">¥{{ formatPrice(detailItem.price_cents) }}</strong><p>{{ detailItem.location.name }} · {{ detailItem.trade_status }}</p><RichText :content="detailItem.description" /><AttachmentGrid :content="detailItem.description" :attachments="detailItem.attachments" /><p class="rulebox">仅支持校内线下面交；平台不经手资金、不收费、不提供担保。成交必须由买卖双方分别确认。</p><div class="modal-actions-v4"><template v-if="!detailItem.mine"><button v-if="detailItem.trade_status === 'available'" class="btn primary" @click="requestReservation(detailItem)">申请预订</button><button class="btn ghost" @click="messageSeller(detailItem)">私信卖家</button></template><template v-else><button v-if="detailItem.trade_status === 'available'" class="btn ghost" @click="openListingEdit(detailItem)">编辑</button><button class="btn primary" @click="showListingTransactions(detailItem)">处理预订申请</button><button v-if="detailItem.trade_status === 'available'" class="btn warn" @click="cancelListing(detailItem)">取消商品</button></template></div><CommentThread :entity-id="detailItem.id" /></template>
        <template v-else-if="section === 'activities'"><div class="v4-tag-row"><span class="tag blue">{{ detailItem.category }}</span><span>{{ local(detailItem.starts_at) }} · {{ detailItem.location }}</span></div><RichText :content="detailItem.body" /><AttachmentGrid :content="detailItem.body" :attachments="detailItem.attachments" /><div class="modal-actions-v4"><button v-if="!detailItem.mine" class="btn primary" @click="activityJoin(detailItem)">{{ detailItem.joined ? '退出活动' : '加入活动' }}</button><template v-else><button class="btn ghost" @click="openEdit(detailItem)">编辑</button><button class="btn warn" @click="cancelActivity(detailItem)">取消活动</button></template></div><CommentThread :entity-id="detailItem.id" /></template>
        <template v-else-if="section === 'lost'"><div class="v4-tag-row"><span class="tag" :class="detailItem.kind === 'lost' ? 'red' : 'green'">{{ detailItem.kind === 'lost' ? '丢失' : '捡到' }}</span><span class="tag yellow">{{ statusLabel(detailItem.status) }}</span></div><RichText :content="detailItem.description" /><AttachmentGrid :content="detailItem.description" :attachments="detailItem.attachments" /><p>地点：{{ detailItem.location }} · {{ local(detailItem.happened_at) }}</p><div class="modal-actions-v4"><button v-if="!detailItem.mine && detailItem.status === 'open'" class="btn primary" @click="openClaim(detailItem)">提交认领线索</button><template v-if="detailItem.mine"><button v-if="detailItem.status === 'open'" class="btn ghost" @click="openEdit(detailItem)">编辑</button><button v-if="detailItem.claim_count" class="btn primary" @click="openClaims(detailItem)">处理认领申请</button></template></div><CommentThread :entity-id="detailItem.id" /></template>
        <template v-else-if="section === 'observe'"><RichText :content="detailItem.body" /><AttachmentGrid :content="`${detailItem.body}\n${detailItem.response || ''}`" :attachments="detailItem.attachments" /><div v-if="detailItem.response" class="best-answer-v4"><b>指定回应方：</b><RichText :content="detailItem.response" /></div><p v-if="detailItem.admin_note" class="muted">管理员备注：{{ detailItem.admin_note }}</p><div class="modal-actions-v4"><button v-if="detailItem.respondent" class="btn primary" @click="openResponse(detailItem)">提交回应</button></div><CommentThread v-if="detailItem.status === 'published'" :entity-id="detailItem.id" /></template>
      </article>
    </BaseModal>

    <BaseModal v-if="marketTransactions" :title="transactionTitle" wide @close="marketTransactions = null">
      <div class="service-review-list">
        <article v-for="transaction in marketTransactions" :key="transaction.id" class="card compact">
          <div><b>{{ transaction.listing.title }}</b><span class="tag" :class="transaction.status === 'completed' ? 'green' : transaction.status === 'disputed' ? 'red' : 'yellow'">{{ transaction.status }}</span></div>
          <p>买家：{{ transaction.buyer.nickname }} · 卖家：{{ transaction.seller.nickname }} · ¥{{ formatPrice(transaction.listing.price_cents) }}</p>
          <p v-if="transaction.message" class="muted">留言：{{ transaction.message }}</p>
          <p v-if="transaction.reserved_until">预留剩余：{{ transactionLeft(transaction.reserved_until) }} · 买家确认 {{ transaction.buyer_confirmed_at ? '✓' : '—' }} · 卖家确认 {{ transaction.seller_confirmed_at ? '✓' : '—' }}</p>
          <div class="actions">
            <button v-if="transactionActions(transaction).includes('accept')" class="btn primary sm" @click="transactionAction(transaction, 'accept')">接受申请</button><button v-if="transactionActions(transaction).includes('reject')" class="btn ghost sm" @click="transactionAction(transaction, 'reject')">拒绝</button>
            <button v-if="transactionActions(transaction).includes('cancel')" class="btn ghost sm" @click="transactionAction(transaction, 'cancel')">{{ transaction.status === 'requested' ? '撤回申请' : '取消' }}</button>
            <button v-if="transactionActions(transaction).includes('confirm')" class="btn primary sm" @click="transactionAction(transaction, 'confirm')">我已完成面交</button><button v-if="transactionActions(transaction).includes('dispute')" class="btn warn sm" @click="disputeTransaction(transaction)">发起纠纷</button>
            <button v-if="transactionActions(transaction).includes('review')" class="btn ghost sm" @click="reviewTransaction(transaction)">提交双盲评价</button>
          </div>
        </article>
        <label class="notice info">纠纷证据（可选，最多 9 张）<input id="market-evidence" type="file" accept="image/jpeg,image/png,image/webp" multiple /></label>
        <p v-if="!marketTransactions.length" class="empty-state">暂无交易记录。</p>
      </div>
    </BaseModal>

    <BaseModal v-if="serviceDetail" :title="`⭐ ${serviceDetail.name}`" wide @close="serviceDetail = null">
      <div class="service-detail-v4"><div class="service-score-head"><strong>{{ serviceDetail.score ?? '—' }}</strong><span>{{ serviceDetail.rating_count }} 条真实评价 · 同一用户 30 天内限评一次</span></div><form class="service-rate-form" @submit.prevent="rateService"><label>评分<select v-model.number="serviceForm.rating"><option v-for="n in [5,4,3,2,1]" :key="n" :value="n">{{ n }} 星</option></select></label><label>具体体验（低于 3 星必填至少 10 个字）<textarea v-model.trim="serviceForm.body" rows="3" maxlength="2000" /></label><button class="btn primary">提交评价</button></form><div class="service-review-list"><article v-for="rating in serviceDetail.ratings || []" :key="rating.id"><div><b>{{ '★'.repeat(rating.rating) }}</b><span>{{ rating.author }} · {{ local(rating.created_at) }}</span></div><p>{{ rating.body || '未填写文字评价' }}</p><blockquote v-if="rating.response"><b>{{ rating.responder }} 回应：</b>{{ rating.response }}</blockquote><button v-else-if="serviceDetail.managed_by_me" class="btn ghost sm" @click="respondService(rating)">公开回应</button></article><p v-if="!serviceDetail.ratings?.length" class="empty-state">还没有评价。</p></div></div>
    </BaseModal>

    <BaseModal v-if="modal" :title="modalTitle" wide @close="modal = false">
      <form class="form-grid" @submit.prevent="submit">
        <template v-if="action === 'answer'"><div class="editor-field full"><span class="editor-field-label">回答</span><RichEditor v-model="form.body" v-model:attachments="form.attachments" aria-label="回答正文" :max-images="6" /></div></template>
        <template v-else-if="action === 'claim'"><label class="full">能帮助发布者核验的线索<textarea v-model="form.message" rows="6" required minlength="5" /></label></template>
        <template v-else-if="action === 'claims'"><div v-for="item in target.claims" :key="item.id" class="card compact full"><strong>{{ item.claimant }}</strong><p>{{ item.message }}</p><div v-if="item.status === 'pending'" class="actions"><button type="button" @click="decideClaim(item, true)">确认认领完成</button><button type="button" @click="decideClaim(item, false)">不通过</button></div><span v-else class="tag green">{{ item.status }}</span></div><p v-if="!target.claims.length" class="empty-state full">暂无认领申请。</p></template>
        <template v-else-if="action === 'appeal'"><label class="full">申诉理由与证据<textarea v-model="form.reason" rows="8" required minlength="10" /></label></template>
        <template v-else-if="action === 'respond'"><div class="editor-field full"><span class="editor-field-label">回应正文</span><RichEditor v-model="form.body" v-model:attachments="form.attachments" aria-label="回应正文" :max-images="6" /></div></template>
        <template v-else-if="section === 'questions'"><label class="full">问题标题<input v-model="form.title" required /></label><label>分类<input v-model="form.category" /></label><label>悬赏 XP<input v-model.number="form.bounty_xp" type="number" min="0" max="500" /></label><label class="full">标签（逗号分隔）<input v-model="form.tags" /></label><div class="editor-field full"><span class="editor-field-label">补充说明</span><RichEditor v-model="form.body" v-model:attachments="form.attachments" aria-label="问题补充说明" /></div></template>
        <template v-else-if="section === 'handbook'"><label>分类<select v-model="form.category"><option v-for="entry in handbookCategories.filter((entry) => entry[1] !== '校园服务评分')" :key="entry[1]" :value="entry[1]">{{ entry[1] }}</option></select></label><label class="check"><input v-model="form.draft" type="checkbox" /> 保存为草稿</label><label class="full">标题<input v-model="form.title" required /></label><div class="editor-field full"><span class="editor-field-label">正文</span><RichEditor v-model="form.body" v-model:attachments="form.attachments" aria-label="手册正文" :max-length="30000" /></div></template>
        <template v-else-if="section === 'courses'"><label>课程班次<select v-model="form.offering_id"><option v-for="item in items" :key="item.id" :value="item.id">{{ item.course }} · {{ item.teacher }} · {{ item.semester }}</option></select></label><label>评分<input v-model.number="form.rating" type="number" min="1" max="5" /></label><label class="full">标签（逗号分隔）<input v-model="form.tags" /></label><div class="editor-field full"><span class="editor-field-label">评价</span><RichEditor v-model="form.body" v-model:attachments="form.attachments" aria-label="课程评价正文" :max-length="5000" :max-images="6" /></div></template>
        <template v-else-if="section === 'listings'"><label>分类<select v-model.number="form.category_id" required><option v-for="option in marketOptions.categories" :key="option.id" :value="option.id">{{ option.name }}</option></select></label><label>价格（¥）<input v-model.number="form.price_yuan" type="number" min="0" max="1000000" step="0.01" required /></label><label class="full">物品标题<input v-model="form.title" required minlength="3" maxlength="160" placeholder="品牌 + 型号 + 关键信息" /></label><label>成色<select v-model="form.condition"><option v-for="option in marketOptions.conditions" :key="option.code" :value="option.code">{{ option.name }}</option></select></label><label>购买日期（选填）<input v-model="form.purchased_at" type="date" :max="new Date().toISOString().slice(0, 10)" /></label><label>是否可刀<select v-model="form.negotiable"><option :value="true">可小刀</option><option :value="false">一口价</option></select></label><label>交付地点<select v-model.number="form.location_id" required><option v-for="option in marketOptions.locations" :key="option.id" :value="option.id">{{ option.name }}</option></select></label><div class="editor-field full"><span class="editor-field-label">瑕疵与详细说明</span><RichEditor v-model="form.description" v-model:attachments="form.attachments" aria-label="商品详细说明" :max-length="10000" :max-images="9" /></div><p class="notice info full">平台不经手资金、不收取费用、不提供担保。买卖双方通过预订流程约定校内线下面交。</p></template>
        <template v-else-if="section === 'activities'"><label>分类<input v-model="form.category" required /></label><label>人数上限<input v-model.number="form.capacity" type="number" min="2" /></label><label class="full">标题<input v-model="form.title" required /></label><label>开始时间<input v-model="form.starts_at" type="datetime-local" required /></label><label>结束时间<input v-model="form.ends_at" type="datetime-local" /></label><label class="full">地点<input v-model="form.location" required /></label><div class="editor-field full"><span class="editor-field-label">详情</span><RichEditor v-model="form.body" v-model:attachments="form.attachments" aria-label="活动详情" /></div></template>
        <template v-else-if="section === 'lost'"><label>类型<select v-model="form.kind"><option value="lost">我丢失了</option><option value="found">我捡到了</option></select></label><label>发生时间<input v-model="form.happened_at" type="datetime-local" /></label><label class="full">物品名称<input v-model="form.item_name" required /></label><label class="full">地点<input v-model="form.location" required /></label><div class="editor-field full"><span class="editor-field-label">特征说明</span><RichEditor v-model="form.description" v-model:attachments="form.attachments" aria-label="失物特征说明" :max-length="5000" :max-images="6" /></div></template>
        <template v-else-if="section === 'observe'"><p class="notice info full">观察帖默认先审后发。请只描述事件，不公开可识别个人信息。</p><label class="full">事件标题<input v-model="form.title" required minlength="4" maxlength="160" /></label><div class="editor-field full"><span class="editor-field-label">事件描述</span><RichEditor v-model="form.body" v-model:attachments="form.attachments" aria-label="事件描述" :max-length="10000" /></div><label class="check full observe-confirm"><input v-model="observeConfirmed" type="checkbox" required /> 我已阅读并同意本区须知，确认内容不包含可识别个人隐私。</label></template>
        <button v-if="action !== 'claims'" class="btn primary full" :disabled="section === 'observe' && action === 'create' && !observeConfirmed">提交</button>
      </form>
    </BaseModal>
  </section>
</template>

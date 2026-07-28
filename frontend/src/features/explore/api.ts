import { contractData } from '../../contract-client'
import {
  acceptAnswer,
  acceptMarketTransaction,
  acceptObserveAgreement,
  appealPenalty,
  cancelActivity,
  cancelListing,
  cancelMarketTransaction,
  confirmMarketTransaction,
  createActivity,
  createAnswer,
  createConversation,
  createCourseReview,
  createHandbookArticle,
  createListing,
  createLostClaim,
  createLostItem,
  createMarketReview,
  createObservePost,
  createQuestion,
  decideLostClaim,
  favoriteEntity,
  getCampusService,
  getMarketOptions,
  getQuestion,
  joinActivity,
  leaveActivity,
  listActivities,
  listAnnouncements,
  listCampusServices,
  listCourseOfferings,
  listHandbookArticles,
  listListingTransactions,
  listListings,
  listLostClaims,
  listLostItems,
  listMyMarketTransactions,
  listObservePosts,
  listPenalties,
  listQuestions,
  openMarketDispute,
  rateCampusService,
  readAnnouncement,
  rejectMarketTransaction,
  requestListingTransaction,
  respondToCampusServiceRating,
  respondToObservePost,
  revealObservePost,
  updateActivity,
  updateHandbookArticle,
  updateListing,
  updateLostItem,
  updateQuestion,
  uploadImage as uploadImageOperation,
} from '../../generated/sdk'
import type {
  CampusServiceRatingCreate,
  CampusServiceResponseCreate,
  MarketTransaction,
} from '../../generated/sdk'
import { buildMarketListingRequest } from '../../market'
import { buildExploreRequest } from './requests'
import type { ExploreSection } from './registry'
import { toExploreItem, type ExploreForm, type ExploreItemDto, type ExploreItemViewModel } from './model'

export interface ExplorePageViewModel {
  items: ExploreItemViewModel[]
  page: number
  page_size: number
  total: number
}

function page<T extends ExploreItemDto>(
  response: { items: T[]; page: number; page_size: number; total: number },
): ExplorePageViewModel {
  return { ...response, items: response.items.map(toExploreItem) }
}

function targetId(value: number | null): number {
  if (value === null) throw new Error('当前操作缺少目标内容')
  return value
}

export type ExploreSubmitAction = 'create' | 'edit' | 'edit_listing' | 'answer' | 'claim' | 'appeal' | 'respond'

export interface ExploreSubmitInput {
  action: ExploreSubmitAction
  section: ExploreSection
  targetId: number | null
  form: ExploreForm
}

export const exploreApi = {
  async list(section: ExploreSection, currentPage: number): Promise<ExplorePageViewModel> {
    const query = { page: currentPage, page_size: 50 }
    switch (section) {
      case 'questions':
        return page(contractData(await listQuestions({ query })))
      case 'handbook':
        return page(contractData(await listHandbookArticles({ query })))
      case 'courses':
        return page(contractData(await listCourseOfferings({ query })))
      case 'listings':
        return page(contractData(await listListings({ query })))
      case 'activities':
        return page(contractData(await listActivities({ query })))
      case 'lost':
        return page(contractData(await listLostItems({ query })))
      case 'observe':
        return page(contractData(await listObservePosts({ query })))
      case 'governance':
        return page(contractData(await listPenalties({ query })))
      case 'announcements':
        return page(contractData(await listAnnouncements({ query })))
    }
  },
  async campusServices() {
    return contractData(await listCampusServices())
  },
  async marketOptions() {
    return contractData(await getMarketOptions())
  },
  async question(questionId: number) {
    return toExploreItem(contractData(await getQuestion({ path: { question_id: questionId } })))
  },
  async claims(itemId: number) {
    return contractData(await listLostClaims({ path: { item_id: itemId } }))
  },
  async submit(input: ExploreSubmitInput) {
    const attachmentIds = input.form.attachments.map((item) => item.id)
    const form = { ...input.form }
    const id = input.targetId
    if (input.action === 'answer') {
      return contractData(await createAnswer({
        path: { question_id: targetId(id) },
        body: { body: input.form.body, attachment_ids: attachmentIds },
      }))
    }
    if (input.action === 'claim') {
      return contractData(await createLostClaim({
        path: { item_id: targetId(id) },
        body: { message: input.form.message },
      }))
    }
    if (input.action === 'appeal') {
      return contractData(await appealPenalty({
        path: { penalty_id: targetId(id) },
        body: { reason: input.form.reason },
      }))
    }
    if (input.action === 'respond') {
      return contractData(await respondToObservePost({
        path: { observe_id: targetId(id) },
        body: { body: input.form.body, attachment_ids: attachmentIds },
      }))
    }
    if (input.action === 'edit_listing') {
      return contractData(await updateListing({
        path: { listing_id: targetId(id) },
        body: buildMarketListingRequest(form, attachmentIds),
      }))
    }
    if (input.action === 'edit') {
      switch (input.section) {
        case 'questions':
          return contractData(await updateQuestion({ path: { question_id: targetId(id) }, body: buildExploreRequest('question.update', form, attachmentIds) }))
        case 'handbook':
          return contractData(await updateHandbookArticle({ path: { article_id: targetId(id) }, body: buildExploreRequest('article.update', form, attachmentIds) }))
        case 'activities':
          return contractData(await updateActivity({ path: { activity_id: targetId(id) }, body: buildExploreRequest('activity.update', form, attachmentIds) }))
        case 'lost':
          return contractData(await updateLostItem({ path: { item_id: targetId(id) }, body: buildExploreRequest('lost.update', form, attachmentIds) }))
        default:
          throw new Error('当前内容类型不支持编辑')
      }
    }
    switch (input.section) {
      case 'questions':
        return contractData(await createQuestion({ body: buildExploreRequest('question.create', form, attachmentIds) }))
      case 'handbook':
        return contractData(await createHandbookArticle({ body: buildExploreRequest('article.create', form, attachmentIds) }))
      case 'courses':
        return contractData(await createCourseReview({ body: buildExploreRequest('review.create', form, attachmentIds) }))
      case 'listings':
        return contractData(await createListing({ body: buildMarketListingRequest(form, attachmentIds) }))
      case 'activities':
        return contractData(await createActivity({ body: buildExploreRequest('activity.create', form, attachmentIds) }))
      case 'lost':
        return contractData(await createLostItem({ body: buildExploreRequest('lost.create', form, attachmentIds) }))
      case 'observe':
        return contractData(await createObservePost({ body: buildExploreRequest('observe.create', form, attachmentIds) }))
      default:
        throw new Error('当前内容类型不支持发布')
    }
  },
  async acceptAnswer(answerId: number) {
    return contractData(await acceptAnswer({ path: { answer_id: answerId } }))
  },
  async favorite(entityId: number) {
    return contractData(await favoriteEntity({ path: { entity_id: entityId } }))
  },
  async setActivityMembership(activityId: number, joined: boolean) {
    return joined
      ? contractData(await leaveActivity({ path: { activity_id: activityId } }))
      : contractData(await joinActivity({ path: { activity_id: activityId } }))
  },
  async cancelActivity(activityId: number) {
    return contractData(await cancelActivity({ path: { activity_id: activityId } }))
  },
  async readAnnouncement(announcementId: number) {
    return contractData(await readAnnouncement({ path: { announcement_id: announcementId } }))
  },
  async messageSeller(recipientId: number, listingId: number, firstMessage: string) {
    return contractData(await createConversation({
      body: { recipient_id: recipientId, context_type: 'listing', context_id: listingId, first_message: firstMessage },
    }))
  },
  async requestReservation(listingId: number, message: string) {
    return contractData(await requestListingTransaction({ path: { listing_id: listingId }, body: { message } }))
  },
  async listingTransactions(listingId: number) {
    return contractData(await listListingTransactions({ path: { listing_id: listingId } }))
  },
  async myTransactions() {
    return contractData(await listMyMarketTransactions())
  },
  async transactionAction(item: MarketTransaction, action: 'accept' | 'reject' | 'confirm' | 'cancel', reason = '') {
    const path = { transaction_id: item.id }
    switch (action) {
      case 'accept':
        return contractData(await acceptMarketTransaction({ path }))
      case 'reject':
        return contractData(await rejectMarketTransaction({ path }))
      case 'confirm':
        return contractData(await confirmMarketTransaction({ path }))
      case 'cancel':
        return contractData(await cancelMarketTransaction({ path, body: { reason } }))
    }
  },
  async upload(file: File, scope: 'public' | 'market_dispute' = 'public') {
    return contractData(await uploadImageOperation({ body: { file, scope } }))
  },
  async openDispute(transactionId: number, reason: string, attachmentIds: number[]) {
    return contractData(await openMarketDispute({
      path: { transaction_id: transactionId },
      body: { reason, attachment_ids: attachmentIds },
    }))
  },
  async reviewTransaction(transactionId: number, rating: number, body: string) {
    return contractData(await createMarketReview({
      path: { transaction_id: transactionId },
      body: { rating, body },
    }))
  },
  async acceptObserveAgreement() {
    return contractData(await acceptObserveAgreement())
  },
  async revealObserve(observeId: number) {
    return contractData(await revealObservePost({ path: { observe_id: observeId } }))
  },
  async cancelListing(listingId: number) {
    return contractData(await cancelListing({ path: { listing_id: listingId }, body: { reason: '卖家主动取消' } }))
  },
  async decideClaim(itemId: number, claimId: number, approve: boolean) {
    return contractData(await decideLostClaim({
      path: { item_id: itemId, claim_id: claimId },
      body: { approve },
    }))
  },
  async campusService(serviceId: number) {
    return contractData(await getCampusService({ path: { service_id: serviceId } }))
  },
  async rateCampusService(serviceId: number, body: CampusServiceRatingCreate) {
    return contractData(await rateCampusService({ path: { service_id: serviceId }, body }))
  },
  async respondToCampusServiceRating(ratingId: number, body: CampusServiceResponseCreate) {
    return contractData(await respondToCampusServiceRating({ path: { rating_id: ratingId }, body }))
  },
}

import { contractData } from '../../contract-client'
import {
  createAnnouncement,
  createCampusService,
  createCourse,
  createCourseOffering,
  createMarketCategory,
  createMarketLocation,
  createPenalty,
  decideAppeal,
  decideFeedback,
  decideGameSubmission,
  decideMarketDispute,
  decideModerationCase,
  deleteMarketCategory,
  deleteMarketLocation,
  getAdminCreditRules,
  getAdminOverview,
  getAdminSettings,
  getAdminSystemHealth,
  listAdminAppeals,
  listAdminUsers,
  listAuditLogs,
  listBackups,
  listCampusServices,
  listFeedback,
  listGameSubmissions,
  listMarketCategories,
  listMarketDisputes,
  listMarketLocations,
  listModerationCases,
  listTeamGames,
  requestBackup,
  updateAdminCreditRules,
  updateAdminSetting,
  updateAdminUser,
  updateCampusService,
  updateMarketCategory,
  updateMarketLocation,
} from '../../generated/sdk'
import type {
  AnnouncementCreate,
  AppealDecision,
  CampusServiceCreate,
  CampusServiceUpdate,
  CreditRulesUpdate,
  FeedbackDecision,
  GameSubmissionDecision,
  MarketDisputeDecision,
  MarketOptionInput,
  ModerationDecision,
  PenaltyCreate,
  UserAdminUpdate,
} from '../../generated/sdk'

export type MarketOptionKind = 'categories' | 'locations'

export const adminApi = {
  async moderationWorkspace() {
    const [overview, users, cases, appeals, feedback, marketDisputes] = await Promise.all([
      getAdminOverview(),
      listAdminUsers({ query: { page_size: 100 } }),
      listModerationCases(),
      listAdminAppeals(),
      listFeedback(),
      listMarketDisputes(),
    ])
    return {
      overview: contractData(overview),
      users: contractData(users),
      cases: contractData(cases),
      appeals: contractData(appeals),
      feedback: contractData(feedback),
      marketDisputes: contractData(marketDisputes),
    }
  },
  async administratorWorkspace() {
    const [settings, backups, auditLogs, creditRules, health, games, submissions, services, categories, locations] = await Promise.all([
      getAdminSettings(),
      listBackups(),
      listAuditLogs({ query: { page_size: 100 } }),
      getAdminCreditRules(),
      getAdminSystemHealth(),
      listTeamGames(),
      listGameSubmissions(),
      listCampusServices(),
      listMarketCategories(),
      listMarketLocations(),
    ])
    return {
      settings: contractData(settings),
      backups: contractData(backups),
      auditLogs: contractData(auditLogs),
      creditRules: contractData(creditRules),
      health: contractData(health),
      games: contractData(games),
      submissions: contractData(submissions),
      services: contractData(services),
      categories: contractData(categories),
      locations: contractData(locations),
    }
  },
  async health() {
    return contractData(await getAdminSystemHealth())
  },
  async decideModeration(caseId: number, body: ModerationDecision) {
    return contractData(await decideModerationCase({ path: { case_id: caseId }, body }))
  },
  async updateUser(userId: number, body: UserAdminUpdate) {
    return contractData(await updateAdminUser({ path: { user_id: userId }, body }))
  },
  async createPenalty(body: PenaltyCreate) {
    return contractData(await createPenalty({ body }))
  },
  async decideAppeal(appealId: number, body: AppealDecision) {
    return contractData(await decideAppeal({ path: { appeal_id: appealId }, body }))
  },
  async decideFeedback(feedbackId: number, body: FeedbackDecision) {
    return contractData(await decideFeedback({ path: { feedback_id: feedbackId }, body }))
  },
  async decideDispute(disputeId: number, body: MarketDisputeDecision) {
    return contractData(await decideMarketDispute({ path: { dispute_id: disputeId }, body }))
  },
  async publishAnnouncement(body: AnnouncementCreate) {
    return contractData(await createAnnouncement({ body }))
  },
  async createCourseOffering(name: string, teacher: string, semester: string, section: string) {
    const course = contractData(await createCourse({ body: { name, teacher } }))
    return contractData(await createCourseOffering({
      body: { course_id: course.id, semester, section },
    }))
  },
  async createCampusService(body: CampusServiceCreate) {
    return contractData(await createCampusService({ body }))
  },
  async updateCampusService(serviceId: number, body: CampusServiceUpdate) {
    return contractData(await updateCampusService({ path: { service_id: serviceId }, body }))
  },
  async createMarketOption(kind: MarketOptionKind, body: MarketOptionInput) {
    return kind === 'categories'
      ? contractData(await createMarketCategory({ body }))
      : contractData(await createMarketLocation({ body }))
  },
  async updateMarketOption(kind: MarketOptionKind, optionId: number, body: MarketOptionInput) {
    return kind === 'categories'
      ? contractData(await updateMarketCategory({ path: { option_id: optionId }, body }))
      : contractData(await updateMarketLocation({ path: { option_id: optionId }, body }))
  },
  async deleteMarketOption(kind: MarketOptionKind, optionId: number) {
    return kind === 'categories'
      ? contractData(await deleteMarketCategory({ path: { option_id: optionId } }))
      : contractData(await deleteMarketLocation({ path: { option_id: optionId } }))
  },
  async decideGame(submissionId: number, body: GameSubmissionDecision) {
    return contractData(await decideGameSubmission({ path: { submission_id: submissionId }, body }))
  },
  async saveSetting(key: string, value: string) {
    return contractData(await updateAdminSetting({ path: { key }, body: { value } }))
  },
  async saveCreditRules(body: CreditRulesUpdate) {
    return contractData(await updateAdminCreditRules({ body }))
  },
  async requestBackup() {
    return contractData(await requestBackup())
  },
}

import { contractData } from '../../contract-client'
import {
  changeEmail,
  changePassword,
  deactivateAccount,
  deleteEntity,
  listMyAppeals,
  listMyContent,
  listMyFavorites,
  listMyReports,
  listNotifications,
  listSessions,
  publishHandbookArticle,
  readNotification,
  requestVerificationCode,
  revokeSession,
  updatePrivacy,
  updateProfile,
  uploadImage,
} from '../../generated/sdk'
import type {
  DeactivateRequest,
  EmailChange,
  PasswordChange,
  PrivacyUpdate,
  ProfileUpdate,
} from '../../generated/sdk'

export interface MyContentFilter {
  page: number
  type?: string
  status?: string
}

export const meApi = {
  async overview() {
    const [favorites, notifications, sessions, reports, appeals] = await Promise.all([
      listMyFavorites({ query: { page_size: 100 } }),
      listNotifications({ query: { page_size: 100 } }),
      listSessions(),
      listMyReports({ query: { page_size: 100 } }),
      listMyAppeals({ query: { page_size: 100 } }),
    ])
    return {
      favorites: contractData(favorites),
      notifications: contractData(notifications),
      sessions: contractData(sessions),
      reports: contractData(reports),
      appeals: contractData(appeals),
    }
  },
  async content(filter: MyContentFilter) {
    return contractData(await listMyContent({
      query: {
        page: filter.page,
        page_size: 20,
        type: filter.type || undefined,
        status: filter.status || undefined,
      },
    }))
  },
  async saveProfile(profile: ProfileUpdate, privacy: PrivacyUpdate) {
    await Promise.all([
      updateProfile({ body: profile }),
      updatePrivacy({ body: privacy }),
    ]).then((results) => results.map(contractData))
  },
  async uploadAvatar(file: File) {
    return contractData(await uploadImage({ body: { file } }))
  },
  async setAvatar(attachmentId: number) {
    return contractData(await updateProfile({ body: { avatar_attachment_id: attachmentId } }))
  },
  async changePassword(body: PasswordChange) {
    return contractData(await changePassword({ body }))
  },
  async requestEmailCode(email: string) {
    return contractData(await requestVerificationCode({ body: { email, purpose: 'change_email' } }))
  },
  async changeEmail(body: EmailChange) {
    return contractData(await changeEmail({ body }))
  },
  async removeContent(entityId: number) {
    return contractData(await deleteEntity({ path: { entity_id: entityId } }))
  },
  async publishDraft(articleId: number) {
    return contractData(await publishHandbookArticle({ path: { article_id: articleId } }))
  },
  async readNotification(notificationId: number) {
    return contractData(await readNotification({ path: { notification_id: notificationId } }))
  },
  async revokeSession(sessionId: number) {
    return contractData(await revokeSession({ path: { session_id: sessionId } }))
  },
  async deactivate(body: DeactivateRequest) {
    return contractData(await deactivateAccount({ body }))
  },
}

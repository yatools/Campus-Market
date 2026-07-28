import type {
  ActivityCreate,
  ActivityUpdate,
  ArticleCreate,
  ArticleUpdate,
  LostCreate,
  LostUpdate,
  ObserveCreate,
  QuestionCreate,
  QuestionUpdate,
  ReviewCreate,
} from '../../generated/sdk'

type ExploreRequest =
  | QuestionCreate
  | QuestionUpdate
  | ArticleCreate
  | ArticleUpdate
  | ReviewCreate
  | ActivityCreate
  | ActivityUpdate
  | LostCreate
  | LostUpdate
  | ObserveCreate

export type ExploreRequestKind =
  | 'question.create'
  | 'question.update'
  | 'article.create'
  | 'article.update'
  | 'review.create'
  | 'activity.create'
  | 'activity.update'
  | 'lost.create'
  | 'lost.update'
  | 'observe.create'

function text(value: unknown): string {
  return String(value || '')
}

function tags(value: unknown): string[] {
  return text(value).split(',').map((item) => item.trim()).filter(Boolean)
}

function optionalDateTime(value: unknown): string | null {
  return value ? new Date(String(value)).toISOString() : null
}

export function buildExploreRequest(kind: 'question.create', form: Record<string, unknown>, attachmentIds: number[]): QuestionCreate
export function buildExploreRequest(kind: 'question.update', form: Record<string, unknown>, attachmentIds: number[]): QuestionUpdate
export function buildExploreRequest(kind: 'article.create', form: Record<string, unknown>, attachmentIds: number[]): ArticleCreate
export function buildExploreRequest(kind: 'article.update', form: Record<string, unknown>, attachmentIds: number[]): ArticleUpdate
export function buildExploreRequest(kind: 'review.create', form: Record<string, unknown>, attachmentIds: number[]): ReviewCreate
export function buildExploreRequest(kind: 'activity.create', form: Record<string, unknown>, attachmentIds: number[]): ActivityCreate
export function buildExploreRequest(kind: 'activity.update', form: Record<string, unknown>, attachmentIds: number[]): ActivityUpdate
export function buildExploreRequest(kind: 'lost.create', form: Record<string, unknown>, attachmentIds: number[]): LostCreate
export function buildExploreRequest(kind: 'lost.update', form: Record<string, unknown>, attachmentIds: number[]): LostUpdate
export function buildExploreRequest(kind: 'observe.create', form: Record<string, unknown>, attachmentIds: number[]): ObserveCreate
export function buildExploreRequest(kind: ExploreRequestKind, form: Record<string, unknown>, attachmentIds: number[]): ExploreRequest
export function buildExploreRequest(
  kind: ExploreRequestKind,
  form: Record<string, unknown>,
  attachmentIds: number[],
): ExploreRequest {
  switch (kind) {
    case 'question.create':
      return {
        title: text(form.title),
        body: text(form.body),
        category: text(form.category),
        tags: tags(form.tags),
        bounty_xp: Number(form.bounty_xp),
        attachment_ids: attachmentIds,
      }
    case 'question.update':
      return {
        title: text(form.title),
        body: text(form.body),
        category: text(form.category),
        tags: tags(form.tags),
        attachment_ids: attachmentIds,
      }
    case 'article.create':
      return {
        category: text(form.category),
        title: text(form.title),
        body: text(form.body),
        draft: Boolean(form.draft),
        attachment_ids: attachmentIds,
      }
    case 'article.update':
      return {
        category: text(form.category),
        title: text(form.title),
        body: text(form.body),
        attachment_ids: attachmentIds,
      }
    case 'review.create':
      return {
        offering_id: Number(form.offering_id),
        rating: Number(form.rating),
        tags: tags(form.tags),
        body: text(form.body),
        attachment_ids: attachmentIds,
      }
    case 'activity.create':
    case 'activity.update':
      return {
        category: text(form.category),
        title: text(form.title),
        body: text(form.body),
        location: text(form.location),
        capacity: form.capacity ? Number(form.capacity) : null,
        starts_at: optionalDateTime(form.starts_at),
        ends_at: optionalDateTime(form.ends_at),
        attachment_ids: attachmentIds,
      }
    case 'lost.create':
      return {
        kind: text(form.kind),
        item_name: text(form.item_name),
        description: text(form.description),
        location: text(form.location),
        happened_at: optionalDateTime(form.happened_at),
        attachment_ids: attachmentIds,
      }
    case 'lost.update':
      return {
        item_name: text(form.item_name),
        description: text(form.description),
        location: text(form.location),
        happened_at: optionalDateTime(form.happened_at),
        attachment_ids: attachmentIds,
      }
    case 'observe.create':
      return {
        title: text(form.title),
        body: text(form.body),
        attachment_ids: attachmentIds,
      }
  }
}

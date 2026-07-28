import type {
  Activity,
  Announcement,
  CourseOffering,
  CourseReview,
  HandbookArticle,
  LostClaim,
  LostItem,
  MarketListing,
  MarketOption,
  MarketSeller,
  ObservePost,
  Penalty,
  PublicAttachment,
  Question,
  QuestionAnswer,
} from '../../generated/sdk'
import type { Attachment } from '../../types'

export type ExploreItemDto =
  | Question
  | HandbookArticle
  | CourseOffering
  | MarketListing
  | Activity
  | LostItem
  | ObservePost
  | Penalty
  | Announcement

export interface ExploreItemViewModel {
  id: number
  title: string
  body: string
  author: string
  status: string
  created_at: string
  updated_at: string
  attachments: PublicAttachment[]
  category: string | MarketOption
  tags: string[]
  bounty_xp: number
  accepted_answer_id: number | null
  answer_count: number
  answers: QuestionAnswer[]
  mine: boolean
  featured: boolean
  favorite_count: number
  course: string
  teacher: string
  semester: string
  section: string
  review_count: number
  score: number | null
  score_hidden_reason: string
  reviews: CourseReview[]
  location: string | MarketOption
  starts_at: string
  ends_at: string
  capacity: number
  member_count: number
  joined: boolean
  kind: string
  item_name: string
  description: string
  happened_at: string
  claim_count: number
  claims: LostClaim[]
  response: string
  admin_note: string
  respondent: boolean
  can_unmask: boolean
  unmasked: boolean
  unmask_threshold: number
  user: string
  violation_type: string
  result: string
  rule: string
  appeal_status: string
  seller: MarketSeller
  price_cents: number
  condition: MarketListing['condition']
  condition_label: string
  negotiable: boolean
  purchased_at: string | null
  trade_status: MarketListing['trade_status']
  moderation_status: MarketListing['moderation_status']
  level: Announcement['level']
  audience: string
  read: boolean
  read_count: number
  published_at: string
}

const emptySeller: MarketSeller = {
  id: 0,
  nickname: '',
  credit: 0,
  verified: false,
  completed_sales: 0,
  rating_average: 0,
  rating_count: 0,
}

function emptyExploreItem(): ExploreItemViewModel {
  return {
    id: 0,
    title: '',
    body: '',
    author: '',
    status: '',
    created_at: '',
    updated_at: '',
    attachments: [],
    category: '',
    tags: [],
    bounty_xp: 0,
    accepted_answer_id: null,
    answer_count: 0,
    answers: [],
    mine: false,
    featured: false,
    favorite_count: 0,
    course: '',
    teacher: '',
    semester: '',
    section: '',
    review_count: 0,
    score: null,
    score_hidden_reason: '',
    reviews: [],
    location: '',
    starts_at: '',
    ends_at: '',
    capacity: 0,
    member_count: 0,
    joined: false,
    kind: '',
    item_name: '',
    description: '',
    happened_at: '',
    claim_count: 0,
    claims: [],
    response: '',
    admin_note: '',
    respondent: false,
    can_unmask: false,
    unmasked: false,
    unmask_threshold: 0,
    user: '',
    violation_type: '',
    result: '',
    rule: '',
    appeal_status: '',
    seller: emptySeller,
    price_cents: 0,
    condition: 'good',
    condition_label: '',
    negotiable: false,
    purchased_at: null,
    trade_status: 'available',
    moderation_status: 'pending',
    level: 'normal',
    audience: '',
    read: false,
    read_count: 0,
    published_at: '',
  }
}

export function toExploreItem(dto: ExploreItemDto): ExploreItemViewModel {
  const optionalFields = {
    answers: 'answers' in dto ? dto.answers ?? [] : [],
    unmasked: 'unmasked' in dto ? dto.unmasked ?? false : false,
    unmask_threshold: 'unmask_threshold' in dto ? dto.unmask_threshold ?? 0 : 0,
    purchased_at: 'purchased_at' in dto ? dto.purchased_at ?? null : null,
  }
  return {
    ...emptyExploreItem(),
    ...dto,
    ...optionalFields,
  }
}

export function isMarketItem(item: ExploreItemViewModel): item is ExploreItemViewModel & {
  category: MarketOption
  location: MarketOption
} {
  return typeof item.category === 'object' && item.category !== null
    && typeof item.location === 'object' && item.location !== null
}

export interface ExploreForm {
  attachments: Attachment[]
  body: string
  description: string
  title: string
  category: string
  tags: string
  kind: string
  item_name: string
  location: string
  starts_at: string
  ends_at: string
  happened_at: string
  message: string
  reason: string
  condition: MarketListing['condition']
  purchased_at: string
  bounty_xp: number
  capacity: number
  offering_id: number
  category_id: number
  location_id: number
  rating: number
  price_yuan: number
  draft: boolean
  negotiable: boolean
}

export function emptyExploreForm(): ExploreForm {
  return {
    attachments: [],
    body: '',
    description: '',
    title: '',
    category: '',
    tags: '',
    kind: 'lost',
    item_name: '',
    location: '',
    starts_at: '',
    ends_at: '',
    happened_at: '',
    message: '',
    reason: '',
    condition: 'excellent',
    purchased_at: '',
    bounty_xp: 0,
    capacity: 0,
    offering_id: 0,
    category_id: 0,
    location_id: 0,
    rating: 5,
    price_yuan: 0,
    draft: false,
    negotiable: true,
  }
}

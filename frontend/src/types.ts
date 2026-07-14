import type { components } from './generated/api'

export type RegisterRequest = components['schemas']['RegisterRequest']
export type VerificationCodeRequest = components['schemas']['CodeRequest']
export type PostCreateRequest = components['schemas']['PostCreate']
export type TeamRunCreateRequest = components['schemas']['TeamRunCreate']

export interface Page<T> {
  items: T[]
  page: number
  page_size: number
  total: number
}

export interface User {
  id: number
  email: string | null
  nickname: string
  alias: string
  campus_identity: 'student' | 'alumni' | 'staff'
  role: 'user' | 'moderator' | 'admin'
  status: 'active' | 'restricted' | 'disabled' | 'deleted'
  credit: number
  xp: number
  avatar_url: string | null
  dm_stranger_off: boolean
  hide_online: boolean
  unread_notifications?: number
}

export interface Attachment {
  id: number
  url: string
  thumbnail_url: string
  width?: number
  height?: number
}

export interface Comment {
  id: number
  target_entity_id: number
  parent_id: number | null
  body: string
  author: string
  identity_mode: string
  status: string
  mine: boolean
  likes: number
  liked?: boolean
  created_at: string
  updated_at?: string
  attachments: Attachment[]
  replies: Comment[]
}

export interface Post {
  id: number
  title: string
  body: string
  author: string
  identity_mode: string
  status: string
  allow_comments: boolean
  views: number
  likes: number
  favorites: number
  comments: number
  liked?: boolean
  favorited?: boolean
  mine: boolean
  attachments: Attachment[]
  created_at: string
  expires_at: string | null
}

export interface Team {
  id: number
  game: string
  game_id: number | null
  mode: string
  rank_requirement: string
  capacity: number
  member_count: number
  members: Array<{ id: number; nickname: string; credit: number }>
  owner: { id: number; nickname: string; credit: number; verified: boolean }
  completion_rate: number | null
  rating_tags: Record<string, number>
  voice_name: string
  voice_link: string
  notes: string
  newbie_level: string
  vibe: string
  reminder_channels: string[]
  my_reminder_channels: string[]
  recurrence: string
  reminder_minutes: number
  post_departure_retention_minutes: number
  status: string
  joined: boolean
  mine: boolean
  next_run: { id: number; starts_at: string; expires_at: string; status: string } | null
}

export interface CreditRule {
  key: string
  label: string
  kind: 'baseline' | 'threshold' | 'reward' | 'penalty'
  value: number
  description: string
  updated_at?: string
}

export interface CreditRuleSet {
  max_score: number
  initial_score: number
  values: Record<string, number>
  rules: CreditRule[]
}

export interface TeamGame {
  id: number
  name: string
  aliases: string[]
  active: boolean
}

export interface GameSubmission {
  id: number
  submitter_id: number
  name: string
  aliases: string[]
  status: 'pending' | 'approved' | 'merged' | 'rejected'
  resolved_game_id: number | null
  admin_note: string
  created_at: string
}

export interface CampusServiceRating {
  id: number
  rating: number
  body: string
  author: string
  response: string
  responder: string
  created_at: string
  responded_at: string | null
}

export interface CampusService {
  id: number
  name: string
  category: string
  score: number | null
  rating_count: number
  managed_by_me: boolean
  next_rating_at: string | null
  ratings?: CampusServiceRating[]
}

export interface FeedItem {
  id: number
  type: string
  title: string
  body: string
  author: string
  route: string
  meta: Record<string, any>
  attachments: Attachment[]
  likes: number
  favorites: number
  comments: number
  liked?: boolean
  favorited?: boolean
  created_at: string
  updated_at: string
}

export interface HotItem {
  id: number
  type: 'post' | 'question' | 'listing' | 'activity' | string
  title: string
  score: number
  likes: number
  favorites: number
  comments: number
}

export interface Announcement {
  id: number
  title: string
  body: string
  level: 'normal' | 'strong'
  audience: string
  read: boolean
  read_count: number
  published_at: string
}

export interface SearchResult {
  id: number
  type: 'post' | 'question' | 'handbook' | 'listing' | 'activity' | 'lost' | string
  title: string
  summary: string
}

export type AuthMode = 'login' | 'register' | 'reset'

export interface ApiErrorBody {
  code: string
  message: string
  field_errors: Record<string, string>
  request_id: string
}

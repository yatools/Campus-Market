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
  mode: string
  rank_requirement: string
  capacity: number
  member_count: number
  members: Array<{ id: number; nickname: string; credit: number }>
  owner: { id: number; nickname: string }
  voice_name: string
  voice_link: string
  notes: string
  recurrence: string
  reminder_minutes: number
  status: string
  joined: boolean
  mine: boolean
  next_run: { id: number; starts_at: string; status: string } | null
}

export interface ApiErrorBody {
  code: string
  message: string
  field_errors: Record<string, string>
  request_id: string
}

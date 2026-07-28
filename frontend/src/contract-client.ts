import { ApiError } from './api'
import type { ApiErrorBody } from './types'

type ContractResult<T> = {
  data: T | undefined
  error: unknown
  response?: Response
}

function errorBody(value: unknown): Partial<ApiErrorBody> {
  if (!value || typeof value !== 'object') return {}
  const source = value as Record<string, unknown>
  const fields = source.field_errors
  const fieldErrors = fields && typeof fields === 'object'
    ? Object.fromEntries(Object.entries(fields).filter((entry): entry is [string, string] => typeof entry[1] === 'string'))
    : undefined
  return {
    code: typeof source.code === 'string' ? source.code : undefined,
    message: typeof source.message === 'string' ? source.message : undefined,
    field_errors: fieldErrors,
    request_id: typeof source.request_id === 'string' ? source.request_id : undefined,
  }
}

/** Converts generated SDK field-style responses into the application's error model. */
export function contractData<T>(result: ContractResult<T>): T {
  if (result.error instanceof Error) throw result.error
  if (result.error !== undefined) throw new ApiError(result.response?.status ?? 0, errorBody(result.error))
  if (result.data === undefined) {
    throw new ApiError(result.response?.status ?? 0, { code: 'EMPTY_RESPONSE', message: '接口未返回约定的数据' })
  }
  return result.data
}

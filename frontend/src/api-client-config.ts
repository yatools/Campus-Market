import type { CreateClientConfig } from './generated/sdk/client.gen'
import { sdkFetch } from './api'
import { appConfig } from './config'

function contractBaseUrl(): string {
  const configured = appConfig().api_prefix.replace(/\/api\/v1\/?$/, '')
  if (/^https?:\/\//.test(configured)) return configured
  const origin = globalThis.location?.origin || 'http://localhost'
  return configured ? new URL(configured, origin).toString().replace(/\/$/, '') : origin
}

export const createClientConfig: CreateClientConfig = (config) => ({
  ...config,
  baseUrl: contractBaseUrl(),
  credentials: 'include',
  fetch: sdkFetch,
})

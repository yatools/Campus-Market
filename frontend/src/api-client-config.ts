import type { CreateClientConfig } from './generated/sdk/client.gen'
import { sdkFetch } from './api'
import { appConfig } from './config'

export const createClientConfig: CreateClientConfig = (config) => ({
  ...config,
  baseUrl: appConfig().api_prefix.replace(/\/api\/v1\/?$/, ''),
  credentials: 'include',
  fetch: sdkFetch,
})

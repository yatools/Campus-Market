export interface AppConfig {
  api_prefix: string
  csrf_cookie_name: string
}

let current: AppConfig = { api_prefix: '/api/v1', csrf_cookie_name: 'wutong_csrf' }

export async function loadAppConfig(): Promise<AppConfig> {
  const response = await fetch('/app-config.json', { credentials: 'same-origin', cache: 'no-store' })
  if (!response.ok) throw new Error(`无法加载运行时配置（${response.status}）`)
  const value: unknown = await response.json()
  if (!value || typeof value !== 'object') throw new Error('运行时配置格式无效')
  const candidate = value as Partial<AppConfig>
  if (!candidate.api_prefix?.startsWith('/') || !candidate.csrf_cookie_name) throw new Error('运行时配置缺少必要字段')
  current = { api_prefix: candidate.api_prefix.replace(/\/$/, ''), csrf_cookie_name: candidate.csrf_cookie_name }
  return current
}

export function appConfig(): AppConfig { return current }

export function setAppConfigForTest(value: AppConfig): void { current = value }

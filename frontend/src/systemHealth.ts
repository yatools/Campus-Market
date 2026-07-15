import type { AdminSystemHealth } from './types'

export type HealthTone = 'ok' | 'warning' | 'danger'

export interface HealthCard {
  key: string
  title: string
  status: string
  tone: HealthTone
  details: string[]
}

export function formatBytes(bytes: number): string {
  if (!Number.isFinite(bytes) || bytes < 0) return '未知'
  const gib = bytes / 1024 ** 3
  if (gib >= 1) return `${gib.toFixed(gib >= 10 ? 1 : 2)} GiB`
  return `${(bytes / 1024 ** 2).toFixed(bytes >= 100 * 1024 ** 2 ? 0 : 1)} MiB`
}

export function formatHealthTime(value?: string | null): string {
  if (!value) return '暂无记录'
  const date = new Date(value)
  return Number.isNaN(date.getTime()) ? '时间无效' : date.toLocaleString('zh-CN')
}

export function buildHealthCards(health: AdminSystemHealth): HealthCard[] {
  const workerFailed = health.worker.stale || Boolean(health.worker.last_error)
  const backupMissing = !health.backup.status && !health.backup.finished_at
  const backupFailed = health.backup.status === 'failed'

  return [
    {
      key: 'worker',
      title: '后台 Worker',
      status: health.worker.stale ? '失联' : health.worker.last_error ? '最近任务失败' : '运行正常',
      tone: workerFailed ? 'danger' : 'ok',
      details: [
        `最后心跳：${formatHealthTime(health.worker.last_seen_at)}`,
        `最近成功：${formatHealthTime(health.worker.last_success_at)}`,
        ...(health.worker.last_error ? [`错误：${health.worker.last_error}`] : []),
      ],
    },
    {
      key: 'object-storage',
      title: '对象存储',
      status: health.object_storage.ok ? '连接正常' : '连接异常',
      tone: health.object_storage.ok ? 'ok' : 'danger',
      details: [health.object_storage.error ? `错误：${health.object_storage.error}` : 'Public / Private / Backup 存储探测通过'],
    },
    {
      key: 'database',
      title: 'PostgreSQL',
      status: '连接正常',
      tone: 'ok',
      details: [`当前数据库大小：${formatBytes(health.database.size_bytes)}`],
    },
    {
      key: 'disk',
      title: '本地磁盘',
      status: health.disk.ok ? '空间可用' : '探测异常',
      tone: health.disk.ok ? 'ok' : 'danger',
      details: [
        `临时目录剩余：${formatBytes(health.disk.temp_free_bytes)}`,
        ...(health.disk.error ? [`错误：${health.disk.error}`] : []),
      ],
    },
    {
      key: 'email',
      title: '邮件队列',
      status: health.email.failed > 0 ? `${health.email.failed} 封失败` : '队列正常',
      tone: health.email.failed > 0 ? 'danger' : 'ok',
      details: [`待发送：${health.email.pending} 封`, `发送失败：${health.email.failed} 封`],
    },
    {
      key: 'backup',
      title: '最近备份',
      status: backupMissing ? '未生成' : backupFailed ? '备份失败' : health.backup.status === 'ready' ? '备份完成' : `状态：${health.backup.status}`,
      tone: backupMissing ? 'warning' : backupFailed ? 'danger' : health.backup.status === 'ready' ? 'ok' : 'warning',
      details: backupMissing
        ? ['当前还没有备份记录，不代表其他依赖故障']
        : [`完成时间：${formatHealthTime(health.backup.finished_at)}`, ...(health.backup.object_key ? [`对象：${health.backup.object_key}`] : [])],
    },
  ]
}

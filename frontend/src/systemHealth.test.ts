import { describe, expect, it } from 'vitest'
import { buildHealthCards, formatBytes, formatHealthTime } from './systemHealth'
import type { AdminSystemHealth } from './types'

function health(overrides: Partial<AdminSystemHealth> = {}): AdminSystemHealth {
  return {
    worker: { stale: false, last_seen_at: '2026-07-15T12:00:00Z', last_success_at: '2026-07-15T12:00:00Z', last_error: '' },
    object_storage: { ok: true },
    database: { size_bytes: 12 * 1024 ** 2 },
    disk: { ok: true, temp_free_bytes: 3 * 1024 ** 3 },
    email: { pending: 0, failed: 0 },
    backup: { status: 'ready', object_key: 'backup/demo.tar', finished_at: '2026-07-15T11:00:00Z' },
    ...overrides,
  }
}

describe('system health presentation', () => {
  it('formats storage sizes and missing times for people', () => {
    expect(formatBytes(12 * 1024 ** 2)).toBe('12.0 MiB')
    expect(formatBytes(3 * 1024 ** 3)).toBe('3.00 GiB')
    expect(formatHealthTime(null)).toBe('暂无记录')
  })

  it('marks healthy dependencies green and a missing backup yellow', () => {
    const cards = buildHealthCards(health({ backup: { status: '', object_key: '', finished_at: null } }))
    expect(cards.find((item) => item.key === 'worker')?.tone).toBe('ok')
    expect(cards.find((item) => item.key === 'backup')).toMatchObject({ tone: 'warning', status: '未生成' })
  })

  it('marks worker, storage, disk and failed mail red', () => {
    const cards = buildHealthCards(health({
      worker: { stale: true, last_seen_at: null, last_success_at: null, last_error: 'worker stopped' },
      object_storage: { ok: false, error: 'timeout' },
      disk: { ok: false, temp_free_bytes: 0, error: 'probe failed' },
      email: { pending: 2, failed: 1 },
    }))
    for (const key of ['worker', 'object-storage', 'disk', 'email']) expect(cards.find((item) => item.key === key)?.tone).toBe('danger')
  })
})

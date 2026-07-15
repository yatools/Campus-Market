import { describe, expect, it } from 'vitest'
import { teamRunActionState } from './teamRun'
import type { Team, TeamRunSummary } from './types'

const startsAt = Date.parse('2026-07-15T12:00:00Z')

function team(run: Partial<TeamRunSummary> = {}): Team {
  return {
    joined: true,
    mine: false,
    next_run: {
      id: 1,
      starts_at: new Date(startsAt).toISOString(),
      expires_at: null,
      status: 'scheduled',
      my_status: 'joined',
      checked_in: false,
      excused: false,
      member_count: 2,
      ...run,
    },
  } as Team
}

describe('team run action availability', () => {
  it('explains when check-in is too early while still allowing an excuse', () => {
    const state = teamRunActionState(team(), startsAt - 31 * 60_000)
    expect(state.checkInEnabled).toBe(false)
    expect(state.excuseEnabled).toBe(true)
    expect(state.hint).toContain('签到将在')
  })

  it('allows both actions at the opening boundary and only check-in after departure', () => {
    expect(teamRunActionState(team(), startsAt - 30 * 60_000)).toMatchObject({ checkInEnabled: true, excuseEnabled: true })
    expect(teamRunActionState(team(), startsAt + 1)).toMatchObject({ checkInEnabled: true, excuseEnabled: false, excuseLabel: '请假已截止' })
  })

  it('shows immediate terminal states for check-in, excuse and expiry', () => {
    expect(teamRunActionState(team({ checked_in: true, my_status: 'checked_in' }), startsAt).checkInLabel).toBe('本场已签到')
    expect(teamRunActionState(team({ excused: true, my_status: 'excused' }), startsAt).excuseLabel).toBe('本场已请假')
    expect(teamRunActionState(team(), startsAt + 30 * 60_000 + 1)).toMatchObject({ checkInLabel: '签到已截止', excuseLabel: '请假已截止' })
  })
})

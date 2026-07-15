import type { Team } from './types'

export interface TeamRunActionState {
  checkInEnabled: boolean
  checkInLabel: string
  excuseEnabled: boolean
  excuseLabel: string
  hint: string
}

function localTime(value: number): string {
  return new Date(value).toLocaleString('zh-CN', { month: 'numeric', day: 'numeric', hour: '2-digit', minute: '2-digit' })
}

export function teamRunActionState(team: Team | null, now: number): TeamRunActionState {
  const run = team?.next_run
  if (!team?.joined || !run) {
    return { checkInEnabled: false, checkInLabel: '暂无可签到场次', excuseEnabled: false, excuseLabel: '暂无可请假场次', hint: '车队尚未安排下一场。' }
  }
  if (run.checked_in || run.my_status === 'checked_in') {
    return { checkInEnabled: false, checkInLabel: '本场已签到', excuseEnabled: false, excuseLabel: '已签到不可请假', hint: '签到已完成，信用奖励只会发放一次。' }
  }
  if (run.excused || run.my_status === 'excused') {
    return { checkInEnabled: false, checkInLabel: '本场已请假', excuseEnabled: false, excuseLabel: '本场已请假', hint: '本场请假已记录。' }
  }
  if (run.my_status !== 'joined') {
    return { checkInEnabled: false, checkInLabel: '不可签到', excuseEnabled: false, excuseLabel: '不可请假', hint: '你已不在本场成员名单中。' }
  }

  const startsAt = new Date(run.starts_at).getTime()
  if (!Number.isFinite(startsAt)) {
    return { checkInEnabled: false, checkInLabel: '时间异常', excuseEnabled: false, excuseLabel: '时间异常', hint: '场次时间无效，请联系车头。' }
  }
  const opensAt = startsAt - 30 * 60_000
  const closesAt = startsAt + 30 * 60_000
  if (now < opensAt) {
    return { checkInEnabled: false, checkInLabel: '场次签到', excuseEnabled: true, excuseLabel: '本次请假', hint: `签到将在 ${localTime(opensAt)} 开放。` }
  }
  if (now <= startsAt) {
    return { checkInEnabled: true, checkInLabel: '场次签到', excuseEnabled: true, excuseLabel: '本次请假', hint: '签到已开放；发车前仍可请假。' }
  }
  if (now <= closesAt) {
    return { checkInEnabled: true, checkInLabel: '场次签到', excuseEnabled: false, excuseLabel: '请假已截止', hint: '已发车，只能在发车后 30 分钟内补签到。' }
  }
  return { checkInEnabled: false, checkInLabel: '签到已截止', excuseEnabled: false, excuseLabel: '请假已截止', hint: '本场签到与请假时间均已结束。' }
}

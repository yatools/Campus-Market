import { contractData } from '../../contract-client'
import {
  cancelTeam,
  checkInTeamRun,
  createTeam,
  createTeamRun,
  excuseTeamRun,
  joinTeam,
  leaveTeam,
  listTeamGames,
  listTeamRuns,
  listTeams,
  rateTeamMember,
  removeTeamMember,
  submitGame,
  transferTeamOwnership,
  updateTeam,
  updateTeamRun,
} from '../../generated/sdk'
import type {
  GameSubmissionCreate,
  RatingRequest,
  TeamCreate,
  Team as TeamDto,
  TeamRunUpdate,
  TeamUpdate,
} from '../../generated/sdk'
import type { Team, TeamRunSummary } from '../../types'

function runStatus(value: string | null | undefined): TeamRunSummary['my_status'] {
  switch (value) {
    case 'joined':
    case 'checked_in':
    case 'excused':
    case 'left':
    case 'removed':
      return value
    default:
      return null
  }
}

function toTeam(dto: TeamDto): Team {
  const run = dto.next_run
  return {
    ...dto,
    completion_rate: dto.completion_rate ?? null,
    next_run: run
      ? {
          ...run,
          my_status: runStatus(run.my_status),
          checked_in: run.checked_in ?? false,
          excused: run.excused ?? false,
        }
      : null,
  }
}

export const teamsApi = {
  async list() {
    const page = contractData(await listTeams({ query: { page_size: 50 } }))
    return { ...page, items: page.items.map(toTeam) }
  },
  async games() {
    return contractData(await listTeamGames())
  },
  async create(body: TeamCreate) {
    return contractData(await createTeam({ body }))
  },
  async join(teamId: number) {
    return contractData(await joinTeam({
      path: { team_id: teamId },
      body: { reminder_channels: ['email', 'in_app'] },
    }))
  },
  async submitGame(body: GameSubmissionCreate) {
    return contractData(await submitGame({ body }))
  },
  async leave(teamId: number) {
    return contractData(await leaveTeam({ path: { team_id: teamId } }))
  },
  async excuse(teamId: number, runId: number) {
    return contractData(await excuseTeamRun({ path: { team_id: teamId, run_id: runId } }))
  },
  async checkIn(teamId: number, runId: number) {
    return contractData(await checkInTeamRun({ path: { team_id: teamId, run_id: runId } }))
  },
  async cancel(teamId: number) {
    return contractData(await cancelTeam({ path: { team_id: teamId } }))
  },
  async update(teamId: number, body: TeamUpdate) {
    return contractData(await updateTeam({ path: { team_id: teamId }, body }))
  },
  async removeMember(teamId: number, memberId: number) {
    return contractData(await removeTeamMember({ path: { team_id: teamId, member_id: memberId } }))
  },
  async transfer(teamId: number, userId: number) {
    return contractData(await transferTeamOwnership({ path: { team_id: teamId }, body: { user_id: userId } }))
  },
  async runs(teamId: number) {
    return contractData(await listTeamRuns({ path: { team_id: teamId }, query: { page_size: 100 } }))
  },
  async createRun(teamId: number, startsAt: string) {
    return contractData(await createTeamRun({ path: { team_id: teamId }, body: { starts_at: startsAt } }))
  },
  async updateRun(teamId: number, runId: number, body: TeamRunUpdate) {
    return contractData(await updateTeamRun({ path: { team_id: teamId, run_id: runId }, body }))
  },
  async rate(teamId: number, runId: number, body: RatingRequest) {
    return contractData(await rateTeamMember({ path: { team_id: teamId, run_id: runId }, body }))
  },
}

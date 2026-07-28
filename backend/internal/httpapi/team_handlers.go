package httpapi

import (
	"errors"
	"net/http"
	"strings"

	teamapp "github.com/yatools/wutong-campus-wall/backend/internal/team"
)

func (s *Server) joinTeam(w http.ResponseWriter, r *http.Request) error {
	teamID, err := pathID(r, "teamID")
	if err != nil {
		return err
	}
	user, _, err := s.participatingUser(w, r)
	if err != nil {
		return err
	}
	var body struct {
		Channels []string `json:"reminder_channels"`
	}
	if r.ContentLength > 0 {
		if apiErr := decodeBody(r, &body); apiErr != nil {
			return apiErr
		}
	}
	if len(body.Channels) == 0 {
		body.Channels = []string{"email", "in_app"}
	}
	body.Channels = uniqueStrings(body.Channels)
	if !validChannels(body.Channels) {
		return validation("reminder_channels", "提醒渠道仅支持邮件、站内和日历")
	}
	if err := s.advanceTeams(r.Context(), &teamID); err != nil {
		return err
	}
	if _, err := s.Team.Join(
		r.Context(),
		teamapp.Actor{ID: user.ID, Role: user.Role, Nickname: user.Nickname},
		teamID,
		body.Channels,
	); err != nil {
		return teamServiceError(err)
	}
	return s.writeTeamPayload(w, r, teamID, &user)
}

func (s *Server) leaveTeam(w http.ResponseWriter, r *http.Request) error {
	teamID, err := pathID(r, "teamID")
	if err != nil {
		return err
	}
	user, _, err := s.currentUser(w, r, true)
	if err != nil {
		return err
	}
	result, err := s.Team.Leave(
		r.Context(),
		teamapp.Actor{ID: user.ID, Role: user.Role, Nickname: user.Nickname},
		teamID,
	)
	if err != nil {
		return teamServiceError(err)
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "credit_delta": result.CreditDelta})
	return nil
}

func (s *Server) excuseTeamRun(w http.ResponseWriter, r *http.Request) error {
	teamID, err := pathID(r, "teamID")
	if err != nil {
		return err
	}
	runID, err := pathID(r, "runID")
	if err != nil {
		return err
	}
	user, _, err := s.currentUser(w, r, true)
	if err != nil {
		return err
	}
	result, err := s.Team.Excuse(
		r.Context(),
		teamapp.Actor{ID: user.ID, Role: user.Role, Nickname: user.Nickname},
		teamID,
		runID,
	)
	if err != nil {
		return teamServiceError(err)
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "excused_at": result.ExcusedAt})
	return nil
}

func (s *Server) checkInTeamRun(w http.ResponseWriter, r *http.Request) error {
	teamID, err := pathID(r, "teamID")
	if err != nil {
		return err
	}
	runID, err := pathID(r, "runID")
	if err != nil {
		return err
	}
	user, _, err := s.currentUser(w, r, true)
	if err != nil {
		return err
	}
	result, err := s.Team.CheckIn(
		r.Context(),
		teamapp.Actor{ID: user.ID, Role: user.Role, Nickname: user.Nickname},
		teamID,
		runID,
	)
	if err != nil {
		return teamServiceError(err)
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"ok": true, "credit_delta": result.CreditDelta, "checked_in_at": result.CheckedAt,
	})
	return nil
}

func (s *Server) transferTeam(w http.ResponseWriter, r *http.Request) error {
	teamID, err := pathID(r, "teamID")
	if err != nil {
		return err
	}
	user, _, err := s.currentUser(w, r, true)
	if err != nil {
		return err
	}
	var body struct {
		UserID int64 `json:"user_id"`
	}
	if apiErr := decodeBody(r, &body); apiErr != nil {
		return apiErr
	}
	if _, err := s.Team.Transfer(
		r.Context(),
		teamapp.Actor{ID: user.ID, Role: user.Role, Nickname: user.Nickname},
		teamID,
		body.UserID,
		requestID(r.Context()),
	); err != nil {
		return teamServiceError(err)
	}
	return s.writeTeamPayload(w, r, teamID, &user)
}

func (s *Server) removeTeamMember(w http.ResponseWriter, r *http.Request) error {
	teamID, err := pathID(r, "teamID")
	if err != nil {
		return err
	}
	memberID, err := pathID(r, "memberID")
	if err != nil {
		return err
	}
	user, _, err := s.currentUser(w, r, true)
	if err != nil {
		return err
	}
	if _, err := s.Team.RemoveMember(
		r.Context(),
		teamapp.Actor{ID: user.ID, Role: user.Role, Nickname: user.Nickname},
		teamID,
		memberID,
	); err != nil {
		return teamServiceError(err)
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
	return nil
}

func (s *Server) cancelTeam(w http.ResponseWriter, r *http.Request) error {
	teamID, err := pathID(r, "teamID")
	if err != nil {
		return err
	}
	user, _, err := s.currentUser(w, r, true)
	if err != nil {
		return err
	}
	if _, err := s.Team.Cancel(
		r.Context(),
		teamapp.Actor{ID: user.ID, Role: user.Role, Nickname: user.Nickname},
		teamID,
		requestID(r.Context()),
	); err != nil {
		return teamServiceError(err)
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
	return nil
}

func (s *Server) rateTeamMember(w http.ResponseWriter, r *http.Request) error {
	teamID, err := pathID(r, "teamID")
	if err != nil {
		return err
	}
	runID, err := pathID(r, "runID")
	if err != nil {
		return err
	}
	user, _, err := s.participatingUser(w, r)
	if err != nil {
		return err
	}
	var body struct {
		Target int64    `json:"target_user_id"`
		Tags   []string `json:"tags"`
	}
	if apiErr := decodeBody(r, &body); apiErr != nil {
		return apiErr
	}
	valid := map[string]bool{"friendly": true, "communication": true, "skill": true, "punctual": true}
	body.Tags = uniqueStrings(body.Tags)
	if len(body.Tags) < 1 || len(body.Tags) > 4 {
		return validation("tags", "评价标签应为 1 到 4 项")
	}
	for _, tag := range body.Tags {
		if !valid[tag] {
			return apiError(http.StatusBadRequest, "INVALID_RATING_TAG", "评价标签无效")
		}
	}
	if body.Target == user.ID {
		return apiError(http.StatusBadRequest, "SELF_RATING", "不能评价自己")
	}
	if _, err := s.Team.Rate(
		r.Context(),
		teamapp.Actor{ID: user.ID, Role: user.Role, Nickname: user.Nickname},
		teamID,
		runID,
		body.Target,
		body.Tags,
	); err != nil {
		return teamServiceError(err)
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
	return nil
}

func teamServiceError(err error) error {
	var domainErr *teamapp.RuleError
	if !errors.As(err, &domainErr) {
		return err
	}
	type definition struct {
		status  int
		message string
	}
	definitions := map[string]definition{
		"TEAM_NOT_FOUND":         {http.StatusNotFound, "车队不存在或已关闭"},
		"TEAM_FULL":              {http.StatusConflict, "车队已满员"},
		"TEAM_ALREADY_DEPARTED":  {http.StatusConflict, "车队已经发车，不能继续上车"},
		"OWNER_CANNOT_LEAVE":     {http.StatusBadRequest, "车头需先转让或取消车队"},
		"RUN_MEMBER_REQUIRED":    {http.StatusForbidden, "只有本场成员可以执行此操作"},
		"RUN_STARTED":            {http.StatusBadRequest, "发车后不能请假"},
		"RUN_NOT_FOUND":          {http.StatusNotFound, "发车场次不存在"},
		"RUN_NOT_ACTIVE":         {http.StatusConflict, "该场次已取消或结束"},
		"TEAM_NOT_ACTIVE":        {http.StatusConflict, "车队已解散或取消"},
		"OUTSIDE_CHECKIN_WINDOW": {http.StatusBadRequest, "仅可在发车后 30 分钟内签到"},
		"RATE_LIMITED":           {http.StatusTooManyRequests, "操作过于频繁，请稍后再试"},
		"OWNER_REQUIRED":         {http.StatusForbidden, "只有车头或管理员可以执行此操作"},
		"TARGET_NOT_MEMBER":      {http.StatusBadRequest, "新车头必须是当前成员"},
		"CANNOT_REMOVE_OWNER":    {http.StatusBadRequest, "不能移除车头"},
		"MEMBER_NOT_FOUND":       {http.StatusNotFound, "该用户不是本车队成员"},
		"RATING_NOT_OPEN":        {http.StatusBadRequest, "发车后才能评价"},
		"SAME_RUN_REQUIRED":      {http.StatusForbidden, "只能评价同场队友"},
	}
	item, ok := definitions[domainErr.Code]
	if !ok {
		return err
	}
	return apiError(item.status, domainErr.Code, strings.TrimSpace(item.message))
}

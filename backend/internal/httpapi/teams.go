package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
)

type Team struct {
	ID, OwnerID                                                     int64
	GameID                                                          *int64
	Game, Mode, Rank                                                string
	Capacity                                                        int
	VoiceName, VoiceLink, Notes, Newbie, Vibe, Channels, Recurrence string
	Reminder, Retention                                             int
	Status                                                          string
}
type TeamRun struct {
	ID, TeamID   int64
	Starts       time.Time
	Expires      *time.Time
	Status       string
	ReminderSent *time.Time
	Created      time.Time
}

var defaultGames = []struct {
	Name    string
	Aliases []string
}{{"英雄联盟", []string{"LOL", "League of Legends"}}, {"无畏契约", []string{"瓦", "Valorant"}}, {"CS2", []string{"Counter-Strike 2", "CS"}}, {"原神", []string{"Genshin Impact"}}, {"崩坏：星穹铁道", []string{"星铁", "星穹铁道"}}, {"王者荣耀", []string{"王者"}}, {"雀魂", []string{"Mahjong Soul"}}, {"Minecraft", []string{"MC", "我的世界"}}, {"饥荒", []string{"Don't Starve Together", "DST"}}, {"DND", []string{"D&D", "龙与地下城"}}}

func (s *Server) registerTeamRoutes(r chi.Router) {
	r.Get("/team-games", s.handle(s.listTeamGames))
	r.Post("/game-submissions", s.handle(s.submitGame))
	r.Get("/admin/game-submissions", s.handle(s.adminListGameSubmissions))
	r.Post("/admin/game-submissions/{submissionID}/decision", s.handle(s.decideGameSubmission))
	r.Get("/teams", s.handle(s.listTeams))
	r.Post("/teams", s.handle(s.createTeam))
	r.Get("/teams/{teamID}/runs", s.handle(s.listTeamRuns))
	r.Post("/teams/{teamID}/runs", s.handle(s.createTeamRun))
	r.Patch("/teams/{teamID}/runs/{runID}", s.handle(s.updateTeamRun))
	r.Get("/teams/{teamID}/members/history", s.handle(s.teamMemberHistory))
	r.Get("/teams/{teamID}", s.handle(s.getTeam))
	r.Patch("/teams/{teamID}", s.handle(s.updateTeam))
	r.Post("/teams/{teamID}/join", s.handle(s.joinTeam))
	r.Get("/teams/{teamID}/calendar.ics", s.handle(s.teamCalendar))
	r.Post("/teams/{teamID}/leave", s.handle(s.leaveTeam))
	r.Post("/teams/{teamID}/runs/{runID}/excuse", s.handle(s.excuseTeamRun))
	r.Post("/teams/{teamID}/runs/{runID}/check-in", s.handle(s.checkInTeamRun))
	r.Post("/teams/{teamID}/transfer", s.handle(s.transferTeam))
	r.Post("/teams/{teamID}/members/{memberID}/remove", s.handle(s.removeTeamMember))
	r.Post("/teams/{teamID}/cancel", s.handle(s.cancelTeam))
	r.Post("/teams/{teamID}/runs/{runID}/ratings", s.handle(s.rateTeamMember))
}

func normalizeGame(v string) string { return strings.ToLower(strings.Join(strings.Fields(v), "")) }
func (s *Server) ensureGames(ctx context.Context, tx pgx.Tx) error {
	var count int
	if err := tx.QueryRow(ctx, "SELECT count(*) FROM team_games").Scan(&count); err != nil {
		return err
	}
	if count > 0 {
		return nil
	}
	for _, entry := range defaultGames {
		var id int64
		if err := tx.QueryRow(ctx, "INSERT INTO team_games(name,active,created_at,updated_at) VALUES($1,true,now(),now()) RETURNING id", entry.Name).Scan(&id); err != nil {
			return err
		}
		aliases := append([]string{entry.Name}, entry.Aliases...)
		for _, alias := range aliases {
			if _, err := tx.Exec(ctx, "INSERT INTO team_game_aliases(game_id,alias,normalized_alias) VALUES($1,$2,$3)", id, alias, normalizeGame(alias)); err != nil {
				return err
			}
		}
	}
	return nil
}
func (s *Server) listTeamGames(w http.ResponseWriter, r *http.Request) error {
	tx, err := s.DB.Begin(r.Context())
	if err != nil {
		return err
	}
	defer tx.Rollback(r.Context())
	if err := s.ensureGames(r.Context(), tx); err != nil {
		return err
	}
	rows, err := tx.Query(r.Context(), "SELECT id,name,active FROM team_games WHERE active=true ORDER BY id")
	if err != nil {
		return err
	}
	type gameRow struct {
		id     int64
		name   string
		active bool
	}
	games := []gameRow{}
	for rows.Next() {
		var game gameRow
		if err := rows.Scan(&game.id, &game.name, &game.active); err != nil {
			rows.Close()
			return err
		}
		games = append(games, game)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()
	items := []any{}
	for _, game := range games {
		aliases, err := stringRows(r.Context(), tx, "SELECT alias FROM team_game_aliases WHERE game_id=$1 ORDER BY id", game.id)
		if err != nil {
			return err
		}
		items = append(items, map[string]any{"id": game.id, "name": game.name, "aliases": aliases, "active": game.active})
	}
	if err := tx.Commit(r.Context()); err != nil {
		return err
	}
	writeJSON(w, 200, map[string]any{"items": items, "total": len(items)})
	return nil
}
func (s *Server) submitGame(w http.ResponseWriter, r *http.Request) error {
	user, _, err := s.participatingUser(w, r)
	if err != nil {
		return err
	}
	var body struct {
		Name    string   `json:"name"`
		Aliases []string `json:"aliases"`
	}
	if err := decodeBody(r, &body); err != nil {
		return err
	}
	body.Name = strings.TrimSpace(body.Name)
	if body.Name == "" || runeLen(body.Name) > 80 {
		return validation("name", "String should have at least 1 character")
	}
	if len(body.Aliases) > 10 {
		return validation("aliases", "List should have at most 10 items")
	}
	body.Aliases = cleanStrings(body.Aliases, 80)
	tx, err := s.DB.Begin(r.Context())
	if err != nil {
		return err
	}
	defer tx.Rollback(r.Context())
	if err := s.ensureGames(r.Context(), tx); err != nil {
		return err
	}
	if err := checkRateLimitSQL(r.Context(), tx, "game_submission", strconv.FormatInt(user.ID, 10), 5, 24*60); err != nil {
		return err
	}
	keys := []string{normalizeGame(body.Name)}
	for _, a := range body.Aliases {
		keys = append(keys, normalizeGame(a))
	}
	var existing string
	err = tx.QueryRow(r.Context(), "SELECT g.name FROM team_game_aliases a JOIN team_games g ON g.id=a.game_id WHERE a.normalized_alias=ANY($1) LIMIT 1", keys).Scan(&existing)
	if err == nil {
		return apiError(409, "GAME_EXISTS", "该游戏已收录为“"+existing+"”")
	} else if err != pgx.ErrNoRows {
		return err
	}
	var pending bool
	if err := tx.QueryRow(r.Context(), "SELECT EXISTS(SELECT 1 FROM game_submissions WHERE submitter_id=$1 AND status='pending' AND proposed_name=$2)", user.ID, body.Name).Scan(&pending); err != nil {
		return err
	}
	if pending {
		return apiError(409, "GAME_SUBMISSION_EXISTS", "相同游戏已在审核中")
	}
	data, _ := json.Marshal(body.Aliases)
	var id int64
	var status string
	if err := tx.QueryRow(r.Context(), "INSERT INTO game_submissions(submitter_id,proposed_name,aliases,status,admin_note,created_at) VALUES($1,$2,$3,'pending','',now()) RETURNING id,status", user.ID, body.Name, data).Scan(&id, &status); err != nil {
		return err
	}
	actor := user.ID
	_ = auditSQL(r.Context(), tx, &actor, "game_submission.create", "game_submission", id, "", nil, nil, requestID(r.Context()))
	if err := tx.Commit(r.Context()); err != nil {
		return err
	}
	writeJSON(w, 201, map[string]any{"id": id, "status": status, "name": body.Name, "aliases": body.Aliases})
	return nil
}
func (s *Server) adminListGameSubmissions(w http.ResponseWriter, r *http.Request) error {
	if _, err := s.adminUser(w, r); err != nil {
		return err
	}
	page, size, err := pagination(r, 50, 100)
	if err != nil {
		return err
	}
	status := r.URL.Query().Get("status")
	if status == "" {
		status = "pending"
	}
	where := ""
	args := []any{}
	if status != "" {
		where = " WHERE status=$1"
		args = append(args, status)
	}
	var total int
	if err := s.DB.QueryRow(r.Context(), "SELECT count(*) FROM game_submissions"+where, args...).Scan(&total); err != nil {
		return err
	}
	args = append(args, size, (page-1)*size)
	rows, err := s.DB.Query(r.Context(), fmt.Sprintf("SELECT id,submitter_id,proposed_name,aliases,status,resolved_game_id,admin_note,created_at,reviewed_at FROM game_submissions%s ORDER BY created_at DESC LIMIT $%d OFFSET $%d", where, len(args)-1, len(args)), args...)
	if err != nil {
		return err
	}
	defer rows.Close()
	items := []any{}
	for rows.Next() {
		var id, submitter int64
		var name string
		var aliases []byte
		var state string
		var resolved *int64
		var note string
		var created time.Time
		var reviewed *time.Time
		if err := rows.Scan(&id, &submitter, &name, &aliases, &state, &resolved, &note, &created, &reviewed); err != nil {
			return err
		}
		var list []string
		_ = json.Unmarshal(aliases, &list)
		items = append(items, map[string]any{"id": id, "submitter_id": submitter, "name": name, "aliases": list, "status": state, "resolved_game_id": resolved, "admin_note": note, "created_at": created, "reviewed_at": reviewed})
	}
	writeJSON(w, 200, pagePayload(items, page, size, total))
	return rows.Err()
}
func (s *Server) decideGameSubmission(w http.ResponseWriter, r *http.Request) error {
	admin, err := s.adminUser(w, r)
	if err != nil {
		return err
	}
	id, err := pathID(r, "submissionID")
	if err != nil {
		return err
	}
	var body struct {
		Action        string `json:"action"`
		TargetGameID  *int64 `json:"target_game_id"`
		CanonicalName string `json:"canonical_name"`
		AdminNote     string `json:"admin_note"`
	}
	if err := decodeBody(r, &body); err != nil {
		return err
	}
	if body.Action != "approve_new" && body.Action != "merge" && body.Action != "reject" {
		return validation("action", "Value error, 游戏审核动作无效")
	}
	tx, err := s.DB.Begin(r.Context())
	if err != nil {
		return err
	}
	defer tx.Rollback(r.Context())
	var submitter int64
	var proposed string
	var aliasesRaw []byte
	var status string
	if err := tx.QueryRow(r.Context(), "SELECT submitter_id,proposed_name,aliases,status FROM game_submissions WHERE id=$1 FOR UPDATE", id).Scan(&submitter, &proposed, &aliasesRaw, &status); err == pgx.ErrNoRows {
		return apiError(404, "GAME_SUBMISSION_NOT_FOUND", "游戏提交不存在")
	} else if err != nil {
		return err
	}
	if status != "pending" {
		return apiError(409, "GAME_SUBMISSION_DECIDED", "该提交已经处理")
	}
	var aliases []string
	_ = json.Unmarshal(aliasesRaw, &aliases)
	var gameID *int64
	var gameName string
	if body.Action == "approve_new" {
		gameName = strings.TrimSpace(body.CanonicalName)
		if gameName == "" {
			gameName = proposed
		}
		var exists bool
		if err := tx.QueryRow(r.Context(), "SELECT EXISTS(SELECT 1 FROM team_games WHERE name=$1)", gameName).Scan(&exists); err != nil {
			return err
		}
		if exists {
			return apiError(409, "GAME_EXISTS", "规范游戏名称已存在，请改为合并")
		}
		var gid int64
		if err := tx.QueryRow(r.Context(), "INSERT INTO team_games(name,active,created_at,updated_at) VALUES($1,true,now(),now()) RETURNING id", gameName).Scan(&gid); err != nil {
			return err
		}
		gameID = &gid
		status = "approved"
	} else if body.Action == "merge" {
		if body.TargetGameID == nil {
			return validation("target_game_id", "Field required")
		}
		if err := tx.QueryRow(r.Context(), "SELECT name FROM team_games WHERE id=$1 AND active=true", *body.TargetGameID).Scan(&gameName); err == pgx.ErrNoRows {
			return apiError(404, "TEAM_GAME_NOT_FOUND", "目标游戏不存在")
		} else if err != nil {
			return err
		}
		gameID = body.TargetGameID
		status = "merged"
	} else {
		status = "rejected"
	}
	if gameID != nil {
		all := append([]string{gameName, proposed}, aliases...)
		for _, alias := range all {
			key := normalizeGame(alias)
			var otherID int64
			err := tx.QueryRow(r.Context(), "SELECT game_id FROM team_game_aliases WHERE normalized_alias=$1", key).Scan(&otherID)
			if err == nil && otherID != *gameID {
				return apiError(409, "GAME_ALIAS_CONFLICT", "别名“"+alias+"”已属于其他游戏")
			}
			if err != nil && err != pgx.ErrNoRows {
				return err
			}
			if err == pgx.ErrNoRows {
				if _, err := tx.Exec(r.Context(), "INSERT INTO team_game_aliases(game_id,alias,normalized_alias) VALUES($1,$2,$3)", *gameID, strings.TrimSpace(alias), key); err != nil {
					return err
				}
			}
		}
		keys := []string{}
		for _, a := range all {
			keys = append(keys, normalizeGame(a))
		}
		if _, err := tx.Exec(r.Context(), "UPDATE teams SET game_id=$1,game=$2 WHERE lower(regexp_replace(game,'\\s','','g'))=ANY($3)", *gameID, gameName, keys); err != nil {
			return err
		}
	}
	if _, err := tx.Exec(r.Context(), "UPDATE game_submissions SET status=$1,resolved_game_id=$2,reviewer_id=$3,admin_note=$4,reviewed_at=now() WHERE id=$5", status, gameID, admin.ID, strings.TrimSpace(body.AdminNote), id); err != nil {
		return err
	}
	label := map[string]string{"approved": "收录", "merged": "合并", "rejected": "驳回"}[status]
	_ = notifySQL(r.Context(), tx, submitter, "新游戏提交已处理", fmt.Sprintf("“%s”已%s。%s", proposed, label, strings.TrimSpace(body.AdminNote)), "/teams", "game_submission")
	actor := admin.ID
	_ = auditSQL(r.Context(), tx, &actor, "game_submission.decide", "game_submission", id, "", nil, map[string]any{"status": status, "game_id": gameID}, requestID(r.Context()))
	var game any
	if gameID != nil {
		aliases, err := stringRows(r.Context(), tx, "SELECT alias FROM team_game_aliases WHERE game_id=$1 ORDER BY id", *gameID)
		if err != nil {
			return err
		}
		game = map[string]any{"id": *gameID, "name": gameName, "aliases": aliases, "active": true}
	}
	if err := tx.Commit(r.Context()); err != nil {
		return err
	}
	writeJSON(w, 200, map[string]any{"id": id, "status": status, "game": game})
	return nil
}

func (s *Server) listTeams(w http.ResponseWriter, r *http.Request) error {
	page, size, err := pagination(r, 20, 50)
	if err != nil {
		return err
	}
	viewer, _, err := s.currentUser(w, r, false)
	if err != nil {
		return err
	}
	if err := s.advanceTeams(r.Context(), nil); err != nil {
		return err
	}
	where := "e.status='published' AND t.status='active'"
	args := []any{}
	if game := r.URL.Query().Get("game"); game != "" {
		args = append(args, game)
		where += fmt.Sprintf(" AND t.game=$%d", len(args))
	}
	if raw := r.URL.Query().Get("game_id"); raw != "" {
		id, e := strconv.ParseInt(raw, 10, 64)
		if e != nil || id < 1 {
			return validation("game_id", "Input should be greater than or equal to 1")
		}
		args = append(args, id)
		where += fmt.Sprintf(" AND t.game_id=$%d", len(args))
	}
	var total int
	if err := s.DB.QueryRow(r.Context(), "SELECT count(*) FROM teams t JOIN content_entities e ON e.id=t.entity_id WHERE "+where, args...).Scan(&total); err != nil {
		return err
	}
	args = append(args, size, (page-1)*size)
	rows, err := s.DB.Query(r.Context(), fmt.Sprintf(teamSelect+" JOIN content_entities e ON e.id=t.entity_id WHERE %s ORDER BY e.created_at DESC LIMIT $%d OFFSET $%d", where, len(args)-1, len(args)), args...)
	if err != nil {
		return err
	}
	defer rows.Close()
	items := []any{}
	for rows.Next() {
		team, err := scanTeam(rows)
		if err != nil {
			return err
		}
		payload, err := s.teamPayload(r.Context(), team, userOrNil(viewer))
		if err != nil {
			return err
		}
		items = append(items, payload)
	}
	writeJSON(w, 200, pagePayload(items, page, size, total))
	return rows.Err()
}

func (s *Server) createTeam(w http.ResponseWriter, r *http.Request) error {
	user, _, err := s.participatingUser(w, r)
	if err != nil {
		return err
	}
	var body struct {
		GameID     *int64    `json:"game_id"`
		Game       string    `json:"game"`
		Mode       string    `json:"mode"`
		Rank       string    `json:"rank_requirement"`
		Capacity   int       `json:"capacity"`
		Starts     time.Time `json:"starts_at"`
		Recurrence string    `json:"recurrence"`
		VoiceName  string    `json:"voice_name"`
		VoiceLink  string    `json:"voice_link"`
		Notes      string    `json:"notes"`
		Newbie     string    `json:"newbie_level"`
		Vibe       string    `json:"vibe"`
		Channels   []string  `json:"reminder_channels"`
		Reminder   int       `json:"reminder_minutes"`
		Retention  int       `json:"post_departure_retention_minutes"`
	}
	if err := decodeBody(r, &body); err != nil {
		return err
	}
	applyTeamDefaults(&body)
	if err := validateTeamCreate(body.Mode, body.Capacity, body.Starts, body.Recurrence, body.Channels, body.Reminder, body.Retention); err != nil {
		return err
	}
	tx, err := s.DB.Begin(r.Context())
	if err != nil {
		return err
	}
	defer tx.Rollback(r.Context())
	if err := s.requireCredit(r.Context(), tx, user, "threshold.team_create", "创建车队"); err != nil {
		return err
	}
	if err := s.ensureGames(r.Context(), tx); err != nil {
		return err
	}
	var gameID int64
	var gameName string
	if body.GameID != nil {
		err = tx.QueryRow(r.Context(), "SELECT id,name FROM team_games WHERE id=$1 AND active=true", *body.GameID).Scan(&gameID, &gameName)
	} else if strings.TrimSpace(body.Game) != "" {
		err = tx.QueryRow(r.Context(), "SELECT g.id,g.name FROM team_game_aliases a JOIN team_games g ON g.id=a.game_id WHERE a.normalized_alias=$1 AND g.active=true", normalizeGame(body.Game)).Scan(&gameID, &gameName)
	} else {
		err = pgx.ErrNoRows
	}
	if err == pgx.ErrNoRows {
		return apiError(400, "GAME_NOT_APPROVED", "请选择已审核的游戏，或先提交新游戏")
	}
	if err != nil {
		return err
	}
	entity, _, err := s.createEntity(r.Context(), tx, user.ID, "team", gameName+" "+body.Mode+" "+body.Notes, true, true, false)
	if err != nil {
		return err
	}
	_, err = tx.Exec(r.Context(), `INSERT INTO teams(entity_id,owner_id,game_id,game,mode,rank_requirement,capacity,voice_name,voice_link,notes,newbie_level,vibe,reminder_channels,recurrence,reminder_minutes,post_departure_retention_minutes,status) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,'active')`, entity.ID, user.ID, gameID, gameName, strings.TrimSpace(body.Mode), strings.TrimSpace(body.Rank), body.Capacity, strings.TrimSpace(body.VoiceName), strings.TrimSpace(body.VoiceLink), strings.TrimSpace(body.Notes), strings.TrimSpace(body.Newbie), strings.TrimSpace(body.Vibe), strings.Join(body.Channels, ","), body.Recurrence, body.Reminder, body.Retention)
	if err != nil {
		return err
	}
	expires := body.Starts.Add(time.Duration(body.Retention) * time.Minute)
	var runID int64
	if err := tx.QueryRow(r.Context(), "INSERT INTO team_runs(team_id,starts_at,expires_at,status,created_at) VALUES($1,$2,$3,'scheduled',now()) RETURNING id", entity.ID, body.Starts, expires).Scan(&runID); err != nil {
		return err
	}
	if _, err := tx.Exec(r.Context(), "INSERT INTO team_memberships(team_id,user_id,role,status,reminder_channels,joined_at) VALUES($1,$2,'owner','active',$3,now())", entity.ID, user.ID, strings.Join(body.Channels, ",")); err != nil {
		return err
	}
	if _, err := tx.Exec(r.Context(), "INSERT INTO team_run_members(run_id,user_id,status,credit_awarded) VALUES($1,$2,'joined',false)", runID, user.ID); err != nil {
		return err
	}
	actor := user.ID
	_ = auditSQL(r.Context(), tx, &actor, "team.create", "team", entity.ID, "", nil, nil, requestID(r.Context()))
	team := Team{ID: entity.ID, OwnerID: user.ID, GameID: &gameID, Game: gameName, Mode: strings.TrimSpace(body.Mode), Rank: strings.TrimSpace(body.Rank), Capacity: body.Capacity, VoiceName: strings.TrimSpace(body.VoiceName), VoiceLink: strings.TrimSpace(body.VoiceLink), Notes: strings.TrimSpace(body.Notes), Newbie: strings.TrimSpace(body.Newbie), Vibe: strings.TrimSpace(body.Vibe), Channels: strings.Join(body.Channels, ","), Recurrence: body.Recurrence, Reminder: body.Reminder, Retention: body.Retention, Status: "active"}
	payload, err := s.teamPayloadTx(r.Context(), tx, team, &user)
	if err != nil {
		return err
	}
	if err := tx.Commit(r.Context()); err != nil {
		return err
	}
	writeJSON(w, 201, payload)
	return nil
}

func (s *Server) listTeamRuns(w http.ResponseWriter, r *http.Request) error {
	teamID, err := pathID(r, "teamID")
	if err != nil {
		return err
	}
	page, size, err := pagination(r, 20, 100)
	if err != nil {
		return err
	}
	viewer, _, err := s.currentUser(w, r, false)
	if err != nil {
		return err
	}
	_ = s.advanceTeams(r.Context(), &teamID)
	team, err := s.loadTeam(r.Context(), s.DB, teamID)
	if err == pgx.ErrNoRows {
		return apiError(404, "TEAM_NOT_FOUND", "车队不存在")
	}
	if err != nil {
		return err
	}
	var entityStatus string
	_ = s.DB.QueryRow(r.Context(), "SELECT status FROM content_entities WHERE id=$1", teamID).Scan(&entityStatus)
	var historical bool
	if viewer.ID != 0 {
		_ = s.DB.QueryRow(r.Context(), "SELECT EXISTS(SELECT 1 FROM team_memberships WHERE team_id=$1 AND user_id=$2)", teamID, viewer.ID).Scan(&historical)
	}
	priv := viewer.ID != 0 && (team.OwnerID == viewer.ID || viewer.Role == "moderator" || viewer.Role == "admin" || historical)
	if entityStatus != "published" && !priv {
		return apiError(404, "TEAM_NOT_FOUND", "车队不存在")
	}
	var total int
	_ = s.DB.QueryRow(r.Context(), "SELECT count(*) FROM team_runs WHERE team_id=$1", teamID).Scan(&total)
	rows, err := s.DB.Query(r.Context(), runSelect+" WHERE team_id=$1 ORDER BY starts_at DESC LIMIT $2 OFFSET $3", teamID, size, (page-1)*size)
	if err != nil {
		return err
	}
	defer rows.Close()
	items := []any{}
	for rows.Next() {
		run, err := scanRun(rows)
		if err != nil {
			return err
		}
		payload, err := s.runPayload(r.Context(), run, userOrNil(viewer), priv)
		if err != nil {
			return err
		}
		items = append(items, payload)
	}
	writeJSON(w, 200, pagePayload(items, page, size, total))
	return rows.Err()
}

func (s *Server) createTeamRun(w http.ResponseWriter, r *http.Request) error {
	teamID, err := pathID(r, "teamID")
	if err != nil {
		return err
	}
	user, _, err := s.participatingUser(w, r)
	if err != nil {
		return err
	}
	var body struct {
		Starts time.Time `json:"starts_at"`
	}
	if err := decodeBody(r, &body); err != nil {
		return err
	}
	if !body.Starts.After(time.Now().UTC().Add(10 * time.Minute)) {
		return apiError(400, "START_TIME_TOO_SOON", "发车时间至少需要提前 10 分钟")
	}
	tx, err := s.DB.Begin(r.Context())
	if err != nil {
		return err
	}
	defer tx.Rollback(r.Context())
	team, err := s.loadTeam(r.Context(), tx, teamID)
	if err != nil || team.OwnerID != user.ID || team.Status != "active" {
		return apiError(403, "OWNER_REQUIRED", "只有车头可以新增场次")
	}
	var existingID int64
	err = tx.QueryRow(r.Context(), "SELECT id FROM team_runs WHERE team_id=$1 AND starts_at=$2", teamID, body.Starts).Scan(&existingID)
	if err == nil {
		run, err := s.loadRun(r.Context(), tx, existingID)
		if err != nil {
			return err
		}
		payload, err := s.runPayloadTx(r.Context(), tx, run, &user, true)
		if err != nil {
			return err
		}
		writeJSON(w, 201, payload)
		return nil
	}
	if err != pgx.ErrNoRows {
		return err
	}
	var runID int64
	if err := tx.QueryRow(r.Context(), "INSERT INTO team_runs(team_id,starts_at,expires_at,status,created_at) VALUES($1,$2,$3,'scheduled',now()) RETURNING id", teamID, body.Starts, body.Starts.Add(time.Duration(team.Retention)*time.Minute)).Scan(&runID); err != nil {
		return err
	}
	if _, err := tx.Exec(r.Context(), "INSERT INTO team_run_members(run_id,user_id,status,credit_awarded) SELECT $1,user_id,'joined',false FROM team_memberships WHERE team_id=$2 AND status='active'", runID, teamID); err != nil {
		return err
	}
	actor := user.ID
	_ = auditSQL(r.Context(), tx, &actor, "team_run.create", "team_run", runID, "", nil, map[string]any{"starts_at": body.Starts}, requestID(r.Context()))
	run, err := s.loadRun(r.Context(), tx, runID)
	if err != nil {
		return err
	}
	payload, err := s.runPayloadTx(r.Context(), tx, run, &user, true)
	if err != nil {
		return err
	}
	if err := tx.Commit(r.Context()); err != nil {
		return err
	}
	writeJSON(w, 201, payload)
	return nil
}

func (s *Server) updateTeamRun(w http.ResponseWriter, r *http.Request) error {
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
	var raw map[string]json.RawMessage
	if err := decodeBody(r, &raw); err != nil {
		return err
	}
	var starts *time.Time
	var status *string
	if v, ok := raw["starts_at"]; ok {
		var x time.Time
		if json.Unmarshal(v, &x) != nil {
			return validation("starts_at", "Input should be a valid datetime")
		}
		starts = &x
	}
	if v, ok := raw["status"]; ok {
		var x string
		if json.Unmarshal(v, &x) != nil || x != "scheduled" && x != "cancelled" {
			return validation("status", "Value error, 场次状态无效")
		}
		status = &x
	}
	tx, err := s.DB.Begin(r.Context())
	if err != nil {
		return err
	}
	defer tx.Rollback(r.Context())
	team, err := s.loadTeam(r.Context(), tx, teamID)
	if err != nil || team.OwnerID != user.ID {
		return apiError(403, "OWNER_REQUIRED", "只有车头可以修改场次")
	}
	run, err := s.loadRunForUpdate(r.Context(), tx, runID)
	if err == pgx.ErrNoRows || run.TeamID != teamID {
		return apiError(404, "RUN_NOT_FOUND", "发车场次不存在")
	}
	if err != nil {
		return err
	}
	if run.Status == "scheduled" {
		before := map[string]any{"starts_at": run.Starts, "status": run.Status}
		if starts != nil {
			if !starts.After(time.Now().UTC().Add(10 * time.Minute)) {
				return apiError(400, "START_TIME_TOO_SOON", "发车时间至少需要提前 10 分钟")
			}
			var dup bool
			_ = tx.QueryRow(r.Context(), "SELECT EXISTS(SELECT 1 FROM team_runs WHERE team_id=$1 AND starts_at=$2 AND id<>$3)", teamID, *starts, runID).Scan(&dup)
			if dup {
				return apiError(409, "RUN_TIME_CONFLICT", "该时间已有发车场次")
			}
			run.Starts = *starts
			expires := starts.Add(time.Duration(team.Retention) * time.Minute)
			run.Expires = &expires
			run.ReminderSent = nil
		}
		if status != nil && *status == "cancelled" {
			run.Status = "cancelled"
			if _, err := tx.Exec(r.Context(), "UPDATE team_run_members SET status='cancelled' WHERE run_id=$1 AND status NOT IN ('left','removed')", runID); err != nil {
				return err
			}
			members, err := int64Rows(r.Context(), tx, "SELECT user_id FROM team_run_members WHERE run_id=$1 AND status='cancelled'", runID)
			if err != nil {
				return err
			}
			for _, id := range members {
				_ = notifySQL(r.Context(), tx, id, "发车场次已取消", team.Game+" · "+team.Mode+" 的本次场次已取消", fmt.Sprintf("/teams/%d", teamID), "team")
			}
		}
		if _, err := tx.Exec(r.Context(), "UPDATE team_runs SET starts_at=$1,expires_at=$2,status=$3,reminder_sent_at=$4 WHERE id=$5", run.Starts, run.Expires, run.Status, run.ReminderSent, runID); err != nil {
			return err
		}
		actor := user.ID
		_ = auditSQL(r.Context(), tx, &actor, "team_run.update", "team_run", runID, "", before, map[string]any{"starts_at": run.Starts, "status": run.Status}, requestID(r.Context()))
	}
	payload, err := s.runPayloadTx(r.Context(), tx, run, &user, true)
	if err != nil {
		return err
	}
	if err := tx.Commit(r.Context()); err != nil {
		return err
	}
	writeJSON(w, 200, payload)
	return nil
}

func (s *Server) teamMemberHistory(w http.ResponseWriter, r *http.Request) error {
	teamID, err := pathID(r, "teamID")
	if err != nil {
		return err
	}
	user, _, err := s.currentUser(w, r, true)
	if err != nil {
		return err
	}
	team, err := s.loadTeam(r.Context(), s.DB, teamID)
	if err != nil || team.OwnerID != user.ID && user.Role != "moderator" && user.Role != "admin" {
		return apiError(403, "OWNER_REQUIRED", "无权查看成员历史")
	}
	rows, err := s.DB.Query(r.Context(), `SELECT m.id,m.user_id,COALESCE(u.nickname,'已注销用户'),m.role,m.status,m.joined_at,m.left_at FROM team_memberships m LEFT JOIN users u ON u.id=m.user_id WHERE m.team_id=$1 ORDER BY m.joined_at DESC`, teamID)
	if err != nil {
		return err
	}
	defer rows.Close()
	items := []any{}
	for rows.Next() {
		var id, userID int64
		var nick, role, status string
		var joined time.Time
		var left *time.Time
		if err := rows.Scan(&id, &userID, &nick, &role, &status, &joined, &left); err != nil {
			return err
		}
		items = append(items, map[string]any{"id": id, "user_id": userID, "nickname": nick, "role": role, "status": status, "joined_at": joined, "left_at": left})
	}
	size := len(items)
	if size == 0 {
		size = 20
	}
	writeJSON(w, 200, pagePayload(items, 1, size, len(items)))
	return rows.Err()
}

func (s *Server) getTeam(w http.ResponseWriter, r *http.Request) error {
	teamID, err := pathID(r, "teamID")
	if err != nil {
		return err
	}
	viewer, _, err := s.currentUser(w, r, false)
	if err != nil {
		return err
	}
	_ = s.advanceTeams(r.Context(), &teamID)
	team, err := s.loadTeam(r.Context(), s.DB, teamID)
	if err == pgx.ErrNoRows {
		return apiError(404, "TEAM_NOT_FOUND", "车队不存在")
	}
	if err != nil {
		return err
	}
	var status string
	_ = s.DB.QueryRow(r.Context(), "SELECT status FROM content_entities WHERE id=$1", teamID).Scan(&status)
	var historical bool
	if viewer.ID != 0 {
		_ = s.DB.QueryRow(r.Context(), "SELECT EXISTS(SELECT 1 FROM team_memberships WHERE team_id=$1 AND user_id=$2)", teamID, viewer.ID).Scan(&historical)
	}
	priv := viewer.ID != 0 && (team.OwnerID == viewer.ID || viewer.Role == "moderator" || viewer.Role == "admin" || historical)
	if status != "published" && !priv {
		return apiError(404, "TEAM_NOT_FOUND", "车队不存在")
	}
	payload, err := s.teamPayload(r.Context(), team, userOrNil(viewer))
	if err != nil {
		return err
	}
	writeJSON(w, 200, payload)
	return nil
}

func (s *Server) updateTeam(w http.ResponseWriter, r *http.Request) error {
	teamID, err := pathID(r, "teamID")
	if err != nil {
		return err
	}
	user, _, err := s.participatingUser(w, r)
	if err != nil {
		return err
	}
	var raw map[string]json.RawMessage
	if err := decodeBody(r, &raw); err != nil {
		return err
	}
	tx, err := s.DB.Begin(r.Context())
	if err != nil {
		return err
	}
	defer tx.Rollback(r.Context())
	team, err := s.loadTeam(r.Context(), tx, teamID)
	if err == pgx.ErrNoRows {
		return apiError(404, "TEAM_NOT_FOUND", "车队不存在")
	}
	if err != nil {
		return err
	}
	if team.OwnerID != user.ID && user.Role != "moderator" && user.Role != "admin" {
		return apiError(403, "OWNER_REQUIRED", "只有车头可以修改车队")
	}
	if v, ok := raw["game_id"]; ok {
		var id int64
		if json.Unmarshal(v, &id) != nil {
			return validation("game_id", "Input should be a valid integer")
		}
		var name string
		if err := tx.QueryRow(r.Context(), "SELECT name FROM team_games WHERE id=$1 AND active=true", id).Scan(&name); err == pgx.ErrNoRows {
			return apiError(404, "TEAM_GAME_NOT_FOUND", "游戏不存在或尚未审核")
		} else if err != nil {
			return err
		}
		team.GameID = &id
		team.Game = name
	}
	stringFields := map[string]*string{"mode": &team.Mode, "rank_requirement": &team.Rank, "voice_name": &team.VoiceName, "voice_link": &team.VoiceLink, "notes": &team.Notes, "newbie_level": &team.Newbie, "vibe": &team.Vibe}
	for key, dest := range stringFields {
		if v, ok := raw[key]; ok {
			var x string
			if json.Unmarshal(v, &x) != nil {
				return validation(key, "Input should be a valid string")
			}
			*dest = strings.TrimSpace(x)
		}
	}
	if v, ok := raw["capacity"]; ok {
		var x int
		if json.Unmarshal(v, &x) != nil || x < 2 || x > 99 {
			return validation("capacity", "Input should be between 2 and 99")
		}
		var members int
		_ = tx.QueryRow(r.Context(), "SELECT count(*) FROM team_memberships WHERE team_id=$1 AND status='active'", teamID).Scan(&members)
		if x < members {
			return apiError(400, "CAPACITY_TOO_SMALL", "容量不能小于当前成员数")
		}
		team.Capacity = x
	}
	if v, ok := raw["reminder_channels"]; ok {
		var x []string
		if json.Unmarshal(v, &x) != nil || !validChannels(x) {
			return validation("reminder_channels", "Value error, 提醒渠道仅支持邮件、站内和日历")
		}
		team.Channels = strings.Join(uniqueStrings(x), ",")
	}
	if v, ok := raw["reminder_minutes"]; ok {
		var x int
		if json.Unmarshal(v, &x) != nil || x < 5 || x > 1440 {
			return validation("reminder_minutes", "Input should be between 5 and 1440")
		}
		team.Reminder = x
	}
	_, err = tx.Exec(r.Context(), `UPDATE teams SET game_id=$1,game=$2,mode=$3,rank_requirement=$4,capacity=$5,voice_name=$6,voice_link=$7,notes=$8,newbie_level=$9,vibe=$10,reminder_channels=$11,reminder_minutes=$12 WHERE entity_id=$13`, team.GameID, team.Game, team.Mode, team.Rank, team.Capacity, team.VoiceName, team.VoiceLink, team.Notes, team.Newbie, team.Vibe, team.Channels, team.Reminder, teamID)
	if err != nil {
		return err
	}
	members, err := int64Rows(r.Context(), tx, "SELECT user_id FROM team_memberships WHERE team_id=$1 AND status='active' AND user_id<>$2", teamID, user.ID)
	if err != nil {
		return err
	}
	for _, id := range members {
		_ = notifySQL(r.Context(), tx, id, "车队信息已更新", team.Game+" · "+team.Mode+" 的车头更新了车队信息", fmt.Sprintf("/teams/%d", teamID), "team")
	}
	_, _ = tx.Exec(r.Context(), "UPDATE content_entities SET updated_at=now() WHERE id=$1", teamID)
	actor := user.ID
	_ = auditSQL(r.Context(), tx, &actor, "team.update", "team", teamID, "", nil, nil, requestID(r.Context()))
	payload, err := s.teamPayloadTx(r.Context(), tx, team, &user)
	if err != nil {
		return err
	}
	if err := tx.Commit(r.Context()); err != nil {
		return err
	}
	writeJSON(w, 200, payload)
	return nil
}

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
		if err := decodeBody(r, &body); err != nil {
			return err
		}
	}
	if len(body.Channels) == 0 {
		body.Channels = []string{"email", "in_app"}
	}
	if !validChannels(body.Channels) {
		return validation("reminder_channels", "Value error, 提醒渠道仅支持邮件、站内和日历")
	}
	_ = s.advanceTeams(r.Context(), &teamID)
	tx, err := s.DB.Begin(r.Context())
	if err != nil {
		return err
	}
	defer tx.Rollback(r.Context())
	team, err := s.loadTeamForUpdate(r.Context(), tx, teamID)
	if err != nil {
		return apiError(404, "TEAM_NOT_FOUND", "车队不存在或已关闭")
	}
	var contentStatus string
	_ = tx.QueryRow(r.Context(), "SELECT status FROM content_entities WHERE id=$1", teamID).Scan(&contentStatus)
	if contentStatus != "published" || team.Status != "active" {
		return apiError(404, "TEAM_NOT_FOUND", "车队不存在或已关闭")
	}
	var membershipID int64
	err = tx.QueryRow(r.Context(), "SELECT id FROM team_memberships WHERE team_id=$1 AND user_id=$2 AND status='active'", teamID, user.ID).Scan(&membershipID)
	if err == nil {
		_, _ = tx.Exec(r.Context(), "UPDATE team_memberships SET reminder_channels=$1 WHERE id=$2", strings.Join(uniqueStrings(body.Channels), ","), membershipID)
		payload, err := s.teamPayloadTx(r.Context(), tx, team, &user)
		if err != nil {
			return err
		}
		if err := tx.Commit(r.Context()); err != nil {
			return err
		}
		writeJSON(w, 200, payload)
		return nil
	} else if err != pgx.ErrNoRows {
		return err
	}
	var count int
	_ = tx.QueryRow(r.Context(), "SELECT count(*) FROM team_memberships WHERE team_id=$1 AND status='active'", teamID).Scan(&count)
	if count >= team.Capacity {
		return apiError(409, "TEAM_FULL", "车队已满员")
	}
	run, err := s.currentRun(r.Context(), tx, teamID)
	if err == pgx.ErrNoRows || !run.Starts.After(time.Now().UTC()) {
		return apiError(409, "TEAM_ALREADY_DEPARTED", "车队已经发车，不能继续上车")
	}
	if err != nil {
		return err
	}
	_, err = tx.Exec(r.Context(), `INSERT INTO team_memberships(team_id,user_id,role,status,reminder_channels,joined_at) VALUES($1,$2,'member','active',$3,now())`, teamID, user.ID, strings.Join(uniqueStrings(body.Channels), ","))
	if err != nil {
		return err
	}
	_, err = tx.Exec(r.Context(), `INSERT INTO team_run_members(run_id,user_id,status,credit_awarded) VALUES($1,$2,'joined',false) ON CONFLICT(run_id,user_id) DO UPDATE SET status='joined',excused_at=NULL`, run.ID, user.ID)
	if err != nil {
		return err
	}
	_ = notifySQL(r.Context(), tx, team.OwnerID, "有新成员上车", user.Nickname+" 加入了你的 "+team.Game+" 车队", fmt.Sprintf("/teams/%d", teamID), "team")
	_ = notifySQL(r.Context(), tx, user.ID, "上车成功", "你已加入 "+team.Game+" · "+team.Mode+"，请留意发车提醒", fmt.Sprintf("/teams/%d", teamID), "team")
	_, _ = tx.Exec(r.Context(), "UPDATE content_entities SET updated_at=now() WHERE id=$1", teamID)
	payload, err := s.teamPayloadTx(r.Context(), tx, team, &user)
	if err != nil {
		return err
	}
	if err := tx.Commit(r.Context()); err != nil {
		return err
	}
	writeJSON(w, 200, payload)
	return nil
}

func (s *Server) teamCalendar(w http.ResponseWriter, r *http.Request) error {
	teamID, err := pathID(r, "teamID")
	if err != nil {
		return err
	}
	user, _, err := s.currentUser(w, r, true)
	if err != nil {
		return err
	}
	team, err := s.loadTeam(r.Context(), s.DB, teamID)
	if err != nil {
		return apiError(404, "TEAM_RUN_NOT_FOUND", "只有当前车队成员可以订阅发车日历")
	}
	run, err := s.currentRun(r.Context(), s.DB, teamID)
	if err != nil {
		return apiError(404, "TEAM_RUN_NOT_FOUND", "只有当前车队成员可以订阅发车日历")
	}
	var member bool
	_ = s.DB.QueryRow(r.Context(), "SELECT EXISTS(SELECT 1 FROM team_memberships WHERE team_id=$1 AND user_id=$2 AND status='active')", teamID, user.ID).Scan(&member)
	if !member {
		return apiError(404, "TEAM_RUN_NOT_FOUND", "只有当前车队成员可以订阅发车日历")
	}
	description := fmt.Sprintf("%s；段位 %s；语音 %s；%s", team.Mode, team.Rank, firstNonempty(team.VoiceName, "待通知"), team.Notes)
	lines := []string{"BEGIN:VCALENDAR", "VERSION:2.0", "PRODID:-//Wutong Wall//Team Calendar//ZH-CN", "CALSCALE:GREGORIAN", "BEGIN:VEVENT", fmt.Sprintf("UID:team-%d-run-%d@wutong-wall", teamID, run.ID), "DTSTAMP:" + icsTime(time.Now().UTC()), "DTSTART:" + icsTime(run.Starts), "DTEND:" + icsTime(run.Starts.Add(2*time.Hour)), "SUMMARY:" + icsEscape(team.Game+" · "+team.Mode), "DESCRIPTION:" + icsEscape(description), "END:VEVENT", "END:VCALENDAR", ""}
	w.Header().Set("Content-Type", "text/calendar; charset=utf-8")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="wutong-team-%d.ics"`, teamID))
	w.WriteHeader(200)
	_, err = w.Write([]byte(strings.Join(lines, "\r\n")))
	return err
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
	tx, err := s.DB.Begin(r.Context())
	if err != nil {
		return err
	}
	defer tx.Rollback(r.Context())
	team, err := s.loadTeam(r.Context(), tx, teamID)
	if err != nil {
		return apiError(404, "TEAM_NOT_FOUND", "车队不存在")
	}
	if team.OwnerID == user.ID {
		return apiError(400, "OWNER_CANNOT_LEAVE", "车头需先转让或取消车队")
	}
	var membership int64
	err = tx.QueryRow(r.Context(), "SELECT id FROM team_memberships WHERE team_id=$1 AND user_id=$2 AND status='active'", teamID, user.ID).Scan(&membership)
	if err == pgx.ErrNoRows {
		writeJSON(w, 200, map[string]any{"ok": true, "credit_delta": 0})
		return nil
	}
	if err != nil {
		return err
	}
	delta := 0
	run, runErr := s.currentRun(r.Context(), tx, teamID)
	if runErr == nil {
		var memberID int64
		var excused *time.Time
		err = tx.QueryRow(r.Context(), "SELECT id,excused_at FROM team_run_members WHERE run_id=$1 AND user_id=$2", run.ID, user.ID).Scan(&memberID, &excused)
		if err == nil {
			if !time.Now().UTC().Before(run.Starts.Add(-30*time.Minute)) && time.Now().UTC().Before(run.Starts) && excused == nil {
				delta, err = s.applyCredit(r.Context(), tx, user.ID, "penalty.team_late_leave", "team_run", run.ID)
				if err != nil {
					return err
				}
			}
			_, _ = tx.Exec(r.Context(), "UPDATE team_run_members SET status='left' WHERE id=$1", memberID)
		}
	}
	_, err = tx.Exec(r.Context(), "UPDATE team_memberships SET status='left',left_at=now() WHERE id=$1", membership)
	if err != nil {
		return err
	}
	_ = notifySQL(r.Context(), tx, team.OwnerID, "成员已下车", user.Nickname+" 退出了 "+team.Game+" 车队", fmt.Sprintf("/teams/%d", teamID), "team")
	_, _ = tx.Exec(r.Context(), "UPDATE content_entities SET updated_at=now() WHERE id=$1", teamID)
	if err := tx.Commit(r.Context()); err != nil {
		return err
	}
	writeJSON(w, 200, map[string]any{"ok": true, "credit_delta": delta})
	return nil
}

func (s *Server) excuseTeamRun(w http.ResponseWriter, r *http.Request) error {
	teamID, _ := pathID(r, "teamID")
	runID, _ := pathID(r, "runID")
	user, _, err := s.currentUser(w, r, true)
	if err != nil {
		return err
	}
	tx, err := s.DB.Begin(r.Context())
	if err != nil {
		return err
	}
	defer tx.Rollback(r.Context())
	run, err := s.loadRun(r.Context(), tx, runID)
	if err != nil || run.TeamID != teamID {
		return apiError(403, "RUN_MEMBER_REQUIRED", "只有本场成员可以请假")
	}
	if !time.Now().UTC().Before(run.Starts) {
		return apiError(400, "RUN_STARTED", "发车后不能请假")
	}
	tag, err := tx.Exec(r.Context(), "UPDATE team_run_members SET excused_at=COALESCE(excused_at,now()),status='excused' WHERE run_id=$1 AND user_id=$2 AND status='joined'", runID, user.ID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return apiError(403, "RUN_MEMBER_REQUIRED", "只有本场成员可以请假")
	}
	team, err := s.loadTeam(r.Context(), tx, teamID)
	if err != nil {
		return err
	}
	_ = notifySQL(r.Context(), tx, team.OwnerID, "成员请假", user.Nickname+" 已为本次发车请假", fmt.Sprintf("/teams/%d", teamID), "team")
	_, _ = tx.Exec(r.Context(), "UPDATE content_entities SET updated_at=now() WHERE id=$1", teamID)
	if err := tx.Commit(r.Context()); err != nil {
		return err
	}
	writeJSON(w, 200, map[string]any{"ok": true})
	return nil
}

func (s *Server) checkInTeamRun(w http.ResponseWriter, r *http.Request) error {
	teamID, _ := pathID(r, "teamID")
	runID, _ := pathID(r, "runID")
	user, _, err := s.currentUser(w, r, true)
	if err != nil {
		return err
	}
	tx, err := s.DB.Begin(r.Context())
	if err != nil {
		return err
	}
	defer tx.Rollback(r.Context())
	run, err := s.loadRun(r.Context(), tx, runID)
	if err != nil || run.TeamID != teamID {
		return apiError(404, "RUN_NOT_FOUND", "发车场次不存在")
	}
	if mathAbs(time.Until(run.Starts).Seconds()) > 1800 {
		return apiError(400, "OUTSIDE_CHECKIN_WINDOW", "仅可在发车前后 30 分钟签到")
	}
	var memberID int64
	var checked *time.Time
	var awarded bool
	err = tx.QueryRow(r.Context(), "SELECT id,checked_in_at,credit_awarded FROM team_run_members WHERE run_id=$1 AND user_id=$2 AND status IN ('joined','checked_in') FOR UPDATE", runID, user.ID).Scan(&memberID, &checked, &awarded)
	if err != nil {
		return apiError(403, "RUN_MEMBER_REQUIRED", "只有本场成员可以签到")
	}
	if checked == nil {
		now := time.Now().UTC()
		checked = &now
		_, _ = tx.Exec(r.Context(), "UPDATE team_run_members SET checked_in_at=$1,status='checked_in' WHERE id=$2", now, memberID)
	}
	delta := 0
	if !awarded {
		delta, err = s.applyCredit(r.Context(), tx, user.ID, "reward.team_check_in", "team_run", runID)
		if err != nil {
			return err
		}
		_, _ = tx.Exec(r.Context(), "UPDATE team_run_members SET credit_awarded=true WHERE id=$1", memberID)
	}
	_, _ = tx.Exec(r.Context(), "UPDATE content_entities SET updated_at=now() WHERE id=$1", teamID)
	if err := tx.Commit(r.Context()); err != nil {
		return err
	}
	writeJSON(w, 200, map[string]any{"ok": true, "credit_delta": delta, "checked_in_at": checked})
	return nil
}

func (s *Server) transferTeam(w http.ResponseWriter, r *http.Request) error {
	teamID, _ := pathID(r, "teamID")
	user, _, err := s.currentUser(w, r, true)
	if err != nil {
		return err
	}
	var body struct {
		UserID int64 `json:"user_id"`
	}
	if err := decodeBody(r, &body); err != nil {
		return err
	}
	tx, err := s.DB.Begin(r.Context())
	if err != nil {
		return err
	}
	defer tx.Rollback(r.Context())
	team, err := s.loadTeamForUpdate(r.Context(), tx, teamID)
	if err != nil || team.OwnerID != user.ID {
		return apiError(403, "OWNER_REQUIRED", "只有车头可以转让车队")
	}
	var targetID int64
	if err := tx.QueryRow(r.Context(), "SELECT id FROM team_memberships WHERE team_id=$1 AND user_id=$2 AND status='active'", teamID, body.UserID).Scan(&targetID); err != nil {
		return apiError(400, "TARGET_NOT_MEMBER", "新车头必须是当前成员")
	}
	_, err = tx.Exec(r.Context(), "UPDATE teams SET owner_id=$1 WHERE entity_id=$2", body.UserID, teamID)
	if err != nil {
		return err
	}
	_, _ = tx.Exec(r.Context(), "UPDATE team_memberships SET role=CASE WHEN user_id=$1 THEN 'owner' WHEN user_id=$2 THEN 'member' ELSE role END WHERE team_id=$3", body.UserID, user.ID, teamID)
	_ = notifySQL(r.Context(), tx, body.UserID, "你已成为车头", team.Game+" · "+team.Mode+" 已转让给你", fmt.Sprintf("/teams/%d", teamID), "team")
	actor := user.ID
	_ = auditSQL(r.Context(), tx, &actor, "team.transfer", "team", teamID, "", nil, map[string]any{"owner_id": body.UserID}, requestID(r.Context()))
	_, _ = tx.Exec(r.Context(), "UPDATE content_entities SET updated_at=now() WHERE id=$1", teamID)
	team.OwnerID = body.UserID
	payload, err := s.teamPayloadTx(r.Context(), tx, team, &user)
	if err != nil {
		return err
	}
	if err := tx.Commit(r.Context()); err != nil {
		return err
	}
	writeJSON(w, 200, payload)
	return nil
}

func (s *Server) removeTeamMember(w http.ResponseWriter, r *http.Request) error {
	teamID, _ := pathID(r, "teamID")
	memberID, _ := pathID(r, "memberID")
	user, _, err := s.currentUser(w, r, true)
	if err != nil {
		return err
	}
	tx, err := s.DB.Begin(r.Context())
	if err != nil {
		return err
	}
	defer tx.Rollback(r.Context())
	team, err := s.loadTeam(r.Context(), tx, teamID)
	if err != nil || team.OwnerID != user.ID && user.Role != "moderator" && user.Role != "admin" {
		return apiError(403, "OWNER_REQUIRED", "无权移除成员")
	}
	if memberID == team.OwnerID {
		return apiError(400, "CANNOT_REMOVE_OWNER", "不能移除车头")
	}
	_, _ = tx.Exec(r.Context(), "UPDATE team_memberships SET status='removed',left_at=now() WHERE team_id=$1 AND user_id=$2 AND status='active'", teamID, memberID)
	if run, err := s.currentRun(r.Context(), tx, teamID); err == nil {
		_, _ = tx.Exec(r.Context(), "UPDATE team_run_members SET status='removed' WHERE run_id=$1 AND user_id=$2", run.ID, memberID)
	}
	_ = notifySQL(r.Context(), tx, memberID, "你已被移出车队", "你已被移出 "+team.Game+" · "+team.Mode, "/teams", "team")
	_, _ = tx.Exec(r.Context(), "UPDATE content_entities SET updated_at=now() WHERE id=$1", teamID)
	if err := tx.Commit(r.Context()); err != nil {
		return err
	}
	writeJSON(w, 200, map[string]any{"ok": true})
	return nil
}
func (s *Server) cancelTeam(w http.ResponseWriter, r *http.Request) error {
	teamID, _ := pathID(r, "teamID")
	user, _, err := s.currentUser(w, r, true)
	if err != nil {
		return err
	}
	tx, err := s.DB.Begin(r.Context())
	if err != nil {
		return err
	}
	defer tx.Rollback(r.Context())
	team, err := s.loadTeam(r.Context(), tx, teamID)
	if err != nil || team.OwnerID != user.ID && user.Role != "moderator" && user.Role != "admin" {
		return apiError(403, "OWNER_REQUIRED", "无权取消车队")
	}
	members, err := int64Rows(r.Context(), tx, "SELECT user_id FROM team_memberships WHERE team_id=$1 AND status='active'", teamID)
	if err != nil {
		return err
	}
	_, err = tx.Exec(r.Context(), "UPDATE teams SET status='cancelled' WHERE entity_id=$1", teamID)
	if err != nil {
		return err
	}
	_, _ = tx.Exec(r.Context(), "UPDATE content_entities SET status='hidden',updated_at=now() WHERE id=$1", teamID)
	_, _ = tx.Exec(r.Context(), "UPDATE team_runs SET status='cancelled' WHERE team_id=$1 AND status='scheduled'", teamID)
	_, _ = tx.Exec(r.Context(), "UPDATE team_memberships SET status='cancelled',left_at=now() WHERE team_id=$1 AND status='active'", teamID)
	for _, id := range members {
		_ = notifySQL(r.Context(), tx, id, "车队已取消", team.Game+" · "+team.Mode+" 已取消", "/teams", "team")
	}
	actor := user.ID
	_ = auditSQL(r.Context(), tx, &actor, "team.cancel", "team", teamID, "", nil, nil, requestID(r.Context()))
	if err := tx.Commit(r.Context()); err != nil {
		return err
	}
	writeJSON(w, 200, map[string]any{"ok": true})
	return nil
}

func (s *Server) rateTeamMember(w http.ResponseWriter, r *http.Request) error {
	teamID, _ := pathID(r, "teamID")
	runID, _ := pathID(r, "runID")
	user, _, err := s.participatingUser(w, r)
	if err != nil {
		return err
	}
	var body struct {
		Target int64    `json:"target_user_id"`
		Tags   []string `json:"tags"`
	}
	if err := decodeBody(r, &body); err != nil {
		return err
	}
	valid := map[string]bool{"friendly": true, "communication": true, "skill": true, "punctual": true}
	if len(body.Tags) < 1 || len(body.Tags) > 4 {
		return validation("tags", "List should have at least 1 item")
	}
	for _, tag := range body.Tags {
		if !valid[tag] {
			return apiError(400, "INVALID_RATING_TAG", "评价标签无效")
		}
	}
	if body.Target == user.ID {
		return apiError(400, "SELF_RATING", "不能评价自己")
	}
	tx, err := s.DB.Begin(r.Context())
	if err != nil {
		return err
	}
	defer tx.Rollback(r.Context())
	run, err := s.loadRun(r.Context(), tx, runID)
	if err != nil || run.TeamID != teamID || time.Now().UTC().Before(run.Starts) {
		return apiError(400, "RATING_NOT_OPEN", "发车后才能评价")
	}
	var count int
	_ = tx.QueryRow(r.Context(), "SELECT count(*) FROM team_run_members WHERE run_id=$1 AND user_id=ANY($2) AND status=ANY($3)", runID, []int64{user.ID, body.Target}, []string{"joined", "checked_in", "excused"}).Scan(&count)
	if count != 2 {
		return apiError(403, "SAME_RUN_REQUIRED", "只能评价同场队友")
	}
	for _, tag := range uniqueStrings(body.Tags) {
		_, err = tx.Exec(r.Context(), "INSERT INTO team_ratings(run_id,rater_id,target_id,tag,created_at) VALUES($1,$2,$3,$4,now()) ON CONFLICT(run_id,rater_id,target_id,tag) DO NOTHING", runID, user.ID, body.Target, tag)
		if err != nil {
			return err
		}
	}
	if err := tx.Commit(r.Context()); err != nil {
		return err
	}
	writeJSON(w, 200, map[string]any{"ok": true})
	return nil
}

// Team payload and persistence helpers.
const teamSelect = `SELECT t.entity_id,t.owner_id,t.game_id,t.game,t.mode,t.rank_requirement,t.capacity,t.voice_name,t.voice_link,t.notes,t.newbie_level,t.vibe,t.reminder_channels,t.recurrence,t.reminder_minutes,t.post_departure_retention_minutes,t.status FROM teams t`
const runSelect = `SELECT id,team_id,starts_at,expires_at,status,reminder_sent_at,created_at FROM team_runs`

func scanTeam(row pgx.Row) (Team, error) {
	var t Team
	err := row.Scan(&t.ID, &t.OwnerID, &t.GameID, &t.Game, &t.Mode, &t.Rank, &t.Capacity, &t.VoiceName, &t.VoiceLink, &t.Notes, &t.Newbie, &t.Vibe, &t.Channels, &t.Recurrence, &t.Reminder, &t.Retention, &t.Status)
	return t, err
}
func (s *Server) loadTeam(ctx context.Context, q interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}, id int64) (Team, error) {
	return scanTeam(q.QueryRow(ctx, teamSelect+" WHERE t.entity_id=$1", id))
}
func (s *Server) loadTeamForUpdate(ctx context.Context, tx pgx.Tx, id int64) (Team, error) {
	return scanTeam(tx.QueryRow(ctx, teamSelect+" WHERE t.entity_id=$1 FOR UPDATE", id))
}
func scanRun(row pgx.Row) (TeamRun, error) {
	var x TeamRun
	err := row.Scan(&x.ID, &x.TeamID, &x.Starts, &x.Expires, &x.Status, &x.ReminderSent, &x.Created)
	return x, err
}
func (s *Server) loadRun(ctx context.Context, q interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}, id int64) (TeamRun, error) {
	return scanRun(q.QueryRow(ctx, runSelect+" WHERE id=$1", id))
}
func (s *Server) loadRunForUpdate(ctx context.Context, tx pgx.Tx, id int64) (TeamRun, error) {
	return scanRun(tx.QueryRow(ctx, runSelect+" WHERE id=$1 FOR UPDATE", id))
}
func (s *Server) currentRun(ctx context.Context, q interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}, teamID int64) (TeamRun, error) {
	return scanRun(q.QueryRow(ctx, runSelect+" WHERE team_id=$1 AND status='scheduled' AND (expires_at IS NULL OR expires_at>now()) ORDER BY starts_at LIMIT 1", teamID))
}

func (s *Server) teamPayload(ctx context.Context, t Team, viewer *User) (map[string]any, error) {
	return s.teamPayloadQ(ctx, s.DB, t, viewer)
}
func (s *Server) teamPayloadTx(ctx context.Context, tx pgx.Tx, t Team, viewer *User) (map[string]any, error) {
	return s.teamPayloadQ(ctx, tx, t, viewer)
}
func (s *Server) teamPayloadQ(ctx context.Context, q queryer, t Team, viewer *User) (map[string]any, error) {
	var ownerID int64
	var ownerName string
	var ownerCredit int
	var verified *time.Time
	if err := q.QueryRow(ctx, "SELECT id,nickname,credit,verified_at FROM users WHERE id=$1", t.OwnerID).Scan(&ownerID, &ownerName, &ownerCredit, &verified); err != nil && err != pgx.ErrNoRows {
		return nil, err
	}
	var contentStatus string
	var created time.Time
	if err := q.QueryRow(ctx, "SELECT status,created_at FROM content_entities WHERE id=$1", t.ID).Scan(&contentStatus, &created); err != nil {
		return nil, err
	}
	rows, err := q.Query(ctx, `SELECT m.user_id,u.nickname,u.credit,m.reminder_channels FROM team_memberships m JOIN users u ON u.id=m.user_id WHERE m.team_id=$1 AND m.status='active' ORDER BY m.joined_at`, t.ID)
	if err != nil {
		return nil, err
	}
	members := []any{}
	joined := false
	myChannels := []string{}
	for rows.Next() {
		var id int64
		var name string
		var credit int
		var channels string
		if err := rows.Scan(&id, &name, &credit, &channels); err != nil {
			return nil, err
		}
		members = append(members, map[string]any{"id": id, "nickname": name, "credit": credit})
		if viewer != nil && id == viewer.ID {
			joined = true
			myChannels = splitCSV(channels)
		}
	}
	rows.Close()
	tags := map[string]int{}
	ratingRows, err := q.Query(ctx, "SELECT tag,count(*) FROM team_ratings WHERE target_id=$1 GROUP BY tag", t.OwnerID)
	if err != nil {
		return nil, err
	}
	for ratingRows.Next() {
		var tag string
		var count int
		if err := ratingRows.Scan(&tag, &count); err != nil {
			return nil, err
		}
		tags[tag] = count
	}
	ratingRows.Close()
	var completed, cancelled int
	_ = q.QueryRow(ctx, `SELECT count(*) FILTER(WHERE r.status='completed'),count(*) FILTER(WHERE r.status='cancelled') FROM team_runs r JOIN teams t ON t.entity_id=r.team_id WHERE t.owner_id=$1 AND r.starts_at<now() AND r.status IN ('completed','cancelled')`, t.OwnerID).Scan(&completed, &cancelled)
	var completion any
	if completed+cancelled > 0 {
		completion = int(mathRound(float64(completed) * 100 / float64(completed+cancelled)))
	}
	var next any
	if run, err := s.currentRun(ctx, q, t.ID); err == nil {
		expires := run.Expires
		if expires == nil {
			v := run.Starts.Add(time.Duration(t.Retention) * time.Minute)
			expires = &v
		}
		next = map[string]any{"id": run.ID, "starts_at": run.Starts, "expires_at": expires, "status": run.Status}
	}
	voiceLink := ""
	if joined {
		voiceLink = t.VoiceLink
	}
	owner := any(nil)
	if ownerID != 0 {
		owner = map[string]any{"id": ownerID, "nickname": ownerName, "credit": ownerCredit, "verified": verified != nil}
	}
	return map[string]any{"id": t.ID, "game": t.Game, "game_id": t.GameID, "mode": t.Mode, "rank_requirement": t.Rank, "capacity": t.Capacity, "owner": owner, "completion_rate": completion, "rating_tags": tags, "voice_name": t.VoiceName, "voice_link": voiceLink, "notes": t.Notes, "newbie_level": t.Newbie, "vibe": t.Vibe, "reminder_channels": splitCSV(t.Channels), "my_reminder_channels": myChannels, "recurrence": t.Recurrence, "reminder_minutes": t.Reminder, "post_departure_retention_minutes": t.Retention, "status": t.Status, "content_status": contentStatus, "next_run": next, "members": members, "member_count": len(members), "joined": joined, "mine": viewer != nil && t.OwnerID == viewer.ID, "created_at": created}, nil
}

func (s *Server) runPayload(ctx context.Context, run TeamRun, viewer *User, include bool) (map[string]any, error) {
	return s.runPayloadQ(ctx, s.DB, run, viewer, include)
}
func (s *Server) runPayloadTx(ctx context.Context, tx pgx.Tx, run TeamRun, viewer *User, include bool) (map[string]any, error) {
	return s.runPayloadQ(ctx, tx, run, viewer, include)
}
func (s *Server) runPayloadQ(ctx context.Context, q queryer, run TeamRun, viewer *User, include bool) (map[string]any, error) {
	rows, err := q.Query(ctx, `SELECT m.user_id,m.status,m.checked_in_at,m.excused_at,m.credit_awarded,COALESCE(u.nickname,'已注销用户') FROM team_run_members m LEFT JOIN users u ON u.id=m.user_id WHERE m.run_id=$1 ORDER BY m.id`, run.ID)
	if err != nil {
		return nil, err
	}
	members := []any{}
	count := 0
	var myStatus any
	checked, excused := false, false
	for rows.Next() {
		var id int64
		var status, nick string
		var check, excuse *time.Time
		var awarded bool
		if err := rows.Scan(&id, &status, &check, &excuse, &awarded, &nick); err != nil {
			return nil, err
		}
		if status != "left" && status != "removed" {
			count++
		}
		if viewer != nil && id == viewer.ID {
			myStatus = status
			checked = check != nil
			excused = excuse != nil
		}
		if include {
			members = append(members, map[string]any{"user_id": id, "nickname": nick, "status": status, "checked_in_at": check, "excused_at": excuse, "credit_awarded": awarded})
		}
	}
	rows.Close()
	payload := map[string]any{"id": run.ID, "team_id": run.TeamID, "starts_at": run.Starts, "expires_at": run.Expires, "status": run.Status, "member_count": count, "my_status": myStatus, "checked_in": checked, "excused": excused, "created_at": run.Created}
	if include {
		payload["members"] = members
	}
	return payload, nil
}

func (s *Server) advanceTeams(ctx context.Context, teamID *int64) error {
	where := "t.status='active'"
	args := []any{}
	if teamID != nil {
		where += " AND t.entity_id=$1"
		args = append(args, *teamID)
	}
	rows, err := s.DB.Query(ctx, teamSelect+" WHERE "+where, args...)
	if err != nil {
		return err
	}
	var teams []Team
	for rows.Next() {
		t, err := scanTeam(rows)
		if err != nil {
			return err
		}
		teams = append(teams, t)
	}
	rows.Close()
	now := time.Now().UTC()
	for _, team := range teams {
		tx, err := s.DB.Begin(ctx)
		if err != nil {
			return err
		}
		runs, err := tx.Query(ctx, runSelect+" WHERE team_id=$1 AND status='scheduled' ORDER BY starts_at FOR UPDATE", team.ID)
		if err != nil {
			tx.Rollback(ctx)
			return err
		}
		var scheduled []TeamRun
		for runs.Next() {
			run, err := scanRun(runs)
			if err != nil {
				runs.Close()
				tx.Rollback(ctx)
				return err
			}
			scheduled = append(scheduled, run)
		}
		if err := runs.Err(); err != nil {
			runs.Close()
			tx.Rollback(ctx)
			return err
		}
		runs.Close()
		var expired []TeamRun
		remaining := false
		for _, run := range scheduled {
			expires := run.Starts.Add(time.Duration(team.Retention) * time.Minute)
			if run.Expires != nil {
				expires = *run.Expires
			} else if _, err := tx.Exec(ctx, "UPDATE team_runs SET expires_at=$1 WHERE id=$2", expires, run.ID); err != nil {
				tx.Rollback(ctx)
				return err
			}
			if !expires.After(now) {
				if _, err := tx.Exec(ctx, "UPDATE team_runs SET status='completed' WHERE id=$1", run.ID); err != nil {
					tx.Rollback(ctx)
					return err
				}
				expired = append(expired, run)
			} else {
				remaining = true
			}
		}
		if len(expired) > 0 {
			if team.Recurrence == "once" {
				_, _ = tx.Exec(ctx, "UPDATE teams SET status='archived' WHERE entity_id=$1", team.ID)
				_, _ = tx.Exec(ctx, "UPDATE content_entities SET status='expired',search_visible=false WHERE id=$1 AND status='published'", team.ID)
			} else if !remaining {
				next := expired[len(expired)-1].Starts.Add(7 * 24 * time.Hour)
				for !next.Add(time.Duration(team.Retention) * time.Minute).After(now) {
					next = next.Add(7 * 24 * time.Hour)
				}
				var runID int64
				err = tx.QueryRow(ctx, "INSERT INTO team_runs(team_id,starts_at,expires_at,status,created_at) VALUES($1,$2,$3,'scheduled',now()) ON CONFLICT(team_id,starts_at) DO UPDATE SET starts_at=EXCLUDED.starts_at RETURNING id", team.ID, next, next.Add(time.Duration(team.Retention)*time.Minute)).Scan(&runID)
				if err == nil {
					_, _ = tx.Exec(ctx, "INSERT INTO team_run_members(run_id,user_id,status,credit_awarded) SELECT $1,user_id,'joined',false FROM team_memberships WHERE team_id=$2 AND status='active' ON CONFLICT(run_id,user_id) DO NOTHING", runID, team.ID)
				}
			}
		}
		if err := tx.Commit(ctx); err != nil {
			return err
		}
	}
	return nil
}

func (s *Server) applyCredit(ctx context.Context, tx pgx.Tx, userID int64, key, targetType string, targetID int64) (int, error) {
	delta := creditDefault(key)
	_ = tx.QueryRow(ctx, "SELECT value FROM credit_rules WHERE key=$1", key).Scan(&delta)
	var before, after int
	if err := tx.QueryRow(ctx, "SELECT credit FROM users WHERE id=$1 FOR UPDATE", userID).Scan(&before); err != nil {
		return 0, err
	}
	if err := tx.QueryRow(ctx, "UPDATE users SET credit=GREATEST(0,LEAST(1000,credit+$1)),updated_at=now() WHERE id=$2 RETURNING credit", delta, userID).Scan(&after); err != nil {
		return 0, err
	}
	applied := after - before
	if applied != 0 {
		actor := userID
		_ = auditSQL(ctx, tx, &actor, "credit."+key, targetType, targetID, "", map[string]any{"credit": before}, map[string]any{"credit": after, "delta": applied, "rule": key}, "")
	}
	return applied, nil
}

func validateTeamCreate(mode string, capacity int, starts time.Time, recurrence string, channels []string, reminder, retention int) error {
	fields := map[string]string{}
	if strings.TrimSpace(mode) == "" || len(mode) > 80 {
		fields["mode"] = "String should have at least 1 character"
	}
	if capacity < 2 || capacity > 99 {
		fields["capacity"] = "Input should be between 2 and 99"
	}
	if starts.IsZero() {
		fields["starts_at"] = "Field required"
	}
	if recurrence != "once" && recurrence != "weekly" {
		fields["recurrence"] = "Value error, 仅支持单次或每周车队"
	}
	if !validChannels(channels) {
		fields["reminder_channels"] = "Value error, 提醒渠道仅支持邮件、站内和日历"
	}
	if reminder < 5 || reminder > 1440 {
		fields["reminder_minutes"] = "Input should be between 5 and 1440"
	}
	if retention < 60 || retention > 480 {
		fields["post_departure_retention_minutes"] = "Input should be between 60 and 480"
	}
	if len(fields) > 0 {
		return validationFields(fields)
	}
	if !starts.After(time.Now().UTC().Add(10 * time.Minute)) {
		return apiError(400, "START_TIME_TOO_SOON", "发车时间至少需要提前 10 分钟")
	}
	return nil
}
func applyTeamDefaults(body any) {
	b := body.(*struct {
		GameID     *int64    `json:"game_id"`
		Game       string    `json:"game"`
		Mode       string    `json:"mode"`
		Rank       string    `json:"rank_requirement"`
		Capacity   int       `json:"capacity"`
		Starts     time.Time `json:"starts_at"`
		Recurrence string    `json:"recurrence"`
		VoiceName  string    `json:"voice_name"`
		VoiceLink  string    `json:"voice_link"`
		Notes      string    `json:"notes"`
		Newbie     string    `json:"newbie_level"`
		Vibe       string    `json:"vibe"`
		Channels   []string  `json:"reminder_channels"`
		Reminder   int       `json:"reminder_minutes"`
		Retention  int       `json:"post_departure_retention_minutes"`
	})
	if b.Rank == "" {
		b.Rank = "不限"
	}
	if b.Capacity == 0 {
		b.Capacity = 5
	}
	if b.Recurrence == "" {
		b.Recurrence = "once"
	}
	if b.Newbie == "" {
		b.Newbie = "欢迎新手"
	}
	if len(b.Channels) == 0 {
		b.Channels = []string{"email", "in_app"}
	}
	b.Channels = uniqueStrings(b.Channels)
	if b.Reminder == 0 {
		b.Reminder = 30
	}
	if b.Retention == 0 {
		b.Retention = 120
	}
}
func validChannels(v []string) bool {
	if len(v) < 1 || len(v) > 3 {
		return false
	}
	valid := map[string]bool{"email": true, "in_app": true, "calendar": true}
	for _, x := range v {
		if !valid[x] {
			return false
		}
	}
	return true
}
func uniqueStrings(values []string) []string {
	seen := map[string]bool{}
	out := []string{}
	for _, v := range values {
		if !seen[v] {
			seen[v] = true
			out = append(out, v)
		}
	}
	return out
}
func cleanStrings(values []string, max int) []string {
	out := []string{}
	seen := map[string]bool{}
	for _, v := range values {
		v = strings.TrimSpace(v)
		if v != "" && !seen[v] && runeLen(v) <= max {
			seen[v] = true
			out = append(out, v)
		}
	}
	return out
}
func splitCSV(v string) []string {
	out := []string{}
	for _, x := range strings.Split(v, ",") {
		if x = strings.TrimSpace(x); x != "" {
			out = append(out, x)
		}
	}
	return out
}
func checkRateLimitSQL(ctx context.Context, tx pgx.Tx, action, subject string, limit, minutes int) error {
	var count int
	if err := tx.QueryRow(ctx, "SELECT count(*) FROM rate_limit_events WHERE action=$1 AND subject=$2 AND created_at>=now()-($3*interval '1 minute')", action, subject, minutes).Scan(&count); err != nil {
		return err
	}
	if count >= limit {
		return apiError(429, "RATE_LIMITED", "操作过于频繁，请稍后再试")
	}
	_, err := tx.Exec(ctx, "INSERT INTO rate_limit_events(action,subject,created_at) VALUES($1,$2,now())", action, subject)
	return err
}
func stringRows(ctx context.Context, q queryer, sql string, args ...any) ([]string, error) {
	rows, err := q.Query(ctx, sql, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []string{}
	for rows.Next() {
		var v string
		if err := rows.Scan(&v); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}
func int64Rows(ctx context.Context, q queryer, sql string, args ...any) ([]int64, error) {
	rows, err := q.Query(ctx, sql, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []int64{}
	for rows.Next() {
		var v int64
		if err := rows.Scan(&v); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}
func mathAbs(v float64) float64 {
	if v < 0 {
		return -v
	}
	return v
}
func mathRound(v float64) float64 {
	if v >= 0 {
		return float64(int(v + 0.5))
	}
	return float64(int(v - 0.5))
}
func icsEscape(v string) string {
	v = strings.ReplaceAll(v, "\\", "\\\\")
	v = strings.ReplaceAll(v, ";", "\\;")
	v = strings.ReplaceAll(v, ",", "\\,")
	return strings.ReplaceAll(v, "\n", "\\n")
}
func icsTime(v time.Time) string { return v.UTC().Format("20060102T150405Z") }

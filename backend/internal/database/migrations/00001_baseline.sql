-- +goose Up
CREATE EXTENSION IF NOT EXISTS pg_trgm;

CREATE TABLE announcements (
	id BIGSERIAL NOT NULL, 
	title VARCHAR(160) NOT NULL, 
	body TEXT NOT NULL, 
	level VARCHAR(20) NOT NULL, 
	audience VARCHAR(30) NOT NULL, 
	published_at TIMESTAMP WITH TIME ZONE NOT NULL, 
	PRIMARY KEY (id)
);

CREATE TABLE conversations (
	id BIGSERIAL NOT NULL, 
	context_type VARCHAR(30) NOT NULL, 
	context_id BIGINT, 
	created_at TIMESTAMP WITH TIME ZONE NOT NULL, 
	PRIMARY KEY (id)
);

CREATE TABLE courses (
	id BIGSERIAL NOT NULL, 
	name VARCHAR(160) NOT NULL, 
	teacher VARCHAR(100) NOT NULL, 
	active BOOLEAN NOT NULL, 
	PRIMARY KEY (id), 
	CONSTRAINT uq_course_teacher UNIQUE (name, teacher)
);

CREATE TABLE email_outbox (
	id BIGSERIAL NOT NULL, 
	to_email VARCHAR(320) NOT NULL, 
	subject VARCHAR(200) NOT NULL, 
	body TEXT NOT NULL, 
	status VARCHAR(20) NOT NULL, 
	attempts INTEGER NOT NULL, 
	next_attempt_at TIMESTAMP WITH TIME ZONE NOT NULL, 
	sent_at TIMESTAMP WITH TIME ZONE, 
	last_error TEXT NOT NULL, 
	created_at TIMESTAMP WITH TIME ZONE NOT NULL, 
	PRIMARY KEY (id)
);

CREATE TABLE rate_limit_events (
	id BIGSERIAL NOT NULL, 
	action VARCHAR(40) NOT NULL, 
	subject VARCHAR(320) NOT NULL, 
	created_at TIMESTAMP WITH TIME ZONE NOT NULL, 
	PRIMARY KEY (id)
);

CREATE TABLE team_games (
	id BIGSERIAL NOT NULL, 
	name VARCHAR(80) NOT NULL, 
	active BOOLEAN NOT NULL, 
	created_at TIMESTAMP WITH TIME ZONE NOT NULL, 
	updated_at TIMESTAMP WITH TIME ZONE NOT NULL, 
	PRIMARY KEY (id)
);

CREATE TABLE users (
	id BIGSERIAL NOT NULL, 
	email VARCHAR(320), 
	password_hash VARCHAR(255) NOT NULL, 
	nickname VARCHAR(40) NOT NULL, 
	alias VARCHAR(40) NOT NULL, 
	campus_identity VARCHAR(20) NOT NULL, 
	role VARCHAR(20) NOT NULL, 
	status VARCHAR(20) NOT NULL, 
	credit INTEGER NOT NULL, 
	xp INTEGER NOT NULL, 
	avatar_path VARCHAR(500), 
	dm_stranger_off BOOLEAN NOT NULL, 
	hide_online BOOLEAN NOT NULL, 
	verified_at TIMESTAMP WITH TIME ZONE NOT NULL, 
	deactivated_at TIMESTAMP WITH TIME ZONE, 
	created_at TIMESTAMP WITH TIME ZONE NOT NULL, 
	updated_at TIMESTAMP WITH TIME ZONE NOT NULL, 
	PRIMARY KEY (id), 
	CONSTRAINT ck_user_credit CHECK (credit >= 0 AND credit <= 1000), 
	UNIQUE (alias)
);

CREATE TABLE verification_codes (
	id BIGSERIAL NOT NULL, 
	email VARCHAR(320) NOT NULL, 
	purpose VARCHAR(30) NOT NULL, 
	code_hash VARCHAR(64) NOT NULL, 
	ip_address VARCHAR(64) NOT NULL, 
	expires_at TIMESTAMP WITH TIME ZONE NOT NULL, 
	consumed_at TIMESTAMP WITH TIME ZONE, 
	created_at TIMESTAMP WITH TIME ZONE NOT NULL, 
	PRIMARY KEY (id)
);

CREATE TABLE announcement_reads (
	id BIGSERIAL NOT NULL, 
	announcement_id BIGINT NOT NULL, 
	user_id BIGINT NOT NULL, 
	read_at TIMESTAMP WITH TIME ZONE NOT NULL, 
	PRIMARY KEY (id), 
	CONSTRAINT uq_announcement_read UNIQUE (announcement_id, user_id), 
	FOREIGN KEY(announcement_id) REFERENCES announcements (id) ON DELETE CASCADE, 
	FOREIGN KEY(user_id) REFERENCES users (id) ON DELETE CASCADE
);

CREATE TABLE audit_logs (
	id BIGSERIAL NOT NULL, 
	actor_id BIGINT, 
	action VARCHAR(80) NOT NULL, 
	target_type VARCHAR(40) NOT NULL, 
	target_id VARCHAR(80) NOT NULL, 
	reason TEXT NOT NULL, 
	before_json TEXT NOT NULL, 
	after_json TEXT NOT NULL, 
	request_id VARCHAR(64) NOT NULL, 
	created_at TIMESTAMP WITH TIME ZONE NOT NULL, 
	PRIMARY KEY (id), 
	FOREIGN KEY(actor_id) REFERENCES users (id) ON DELETE SET NULL
);

CREATE TABLE backup_jobs (
	id BIGSERIAL NOT NULL, 
	requested_by BIGINT NOT NULL, 
	status VARCHAR(20) NOT NULL, 
	file_path VARCHAR(500) NOT NULL, 
	download_token VARCHAR(100) NOT NULL, 
	error TEXT NOT NULL, 
	created_at TIMESTAMP WITH TIME ZONE NOT NULL, 
	finished_at TIMESTAMP WITH TIME ZONE, 
	PRIMARY KEY (id), 
	FOREIGN KEY(requested_by) REFERENCES users (id) ON DELETE RESTRICT, 
	UNIQUE (download_token)
);

CREATE TABLE blocks (
	id BIGSERIAL NOT NULL, 
	user_id BIGINT NOT NULL, 
	blocked_id BIGINT NOT NULL, 
	created_at TIMESTAMP WITH TIME ZONE NOT NULL, 
	PRIMARY KEY (id), 
	CONSTRAINT uq_block UNIQUE (user_id, blocked_id), 
	FOREIGN KEY(user_id) REFERENCES users (id) ON DELETE CASCADE, 
	FOREIGN KEY(blocked_id) REFERENCES users (id) ON DELETE CASCADE
);

CREATE TABLE campus_services (
	id BIGSERIAL NOT NULL, 
	name VARCHAR(160) NOT NULL, 
	category VARCHAR(60) NOT NULL, 
	manager_user_id BIGINT, 
	active BOOLEAN NOT NULL, 
	created_at TIMESTAMP WITH TIME ZONE NOT NULL, 
	updated_at TIMESTAMP WITH TIME ZONE NOT NULL, 
	PRIMARY KEY (id), 
	FOREIGN KEY(manager_user_id) REFERENCES users (id) ON DELETE SET NULL
);

CREATE TABLE content_entities (
	id BIGSERIAL NOT NULL, 
	type VARCHAR(30) NOT NULL, 
	owner_id BIGINT NOT NULL, 
	status VARCHAR(20) NOT NULL, 
	allow_comments BOOLEAN NOT NULL, 
	search_visible BOOLEAN NOT NULL, 
	moderation_reason TEXT NOT NULL, 
	revision INTEGER NOT NULL, 
	deleted_at TIMESTAMP WITH TIME ZONE, 
	created_at TIMESTAMP WITH TIME ZONE NOT NULL, 
	updated_at TIMESTAMP WITH TIME ZONE NOT NULL, 
	PRIMARY KEY (id), 
	FOREIGN KEY(owner_id) REFERENCES users (id) ON DELETE RESTRICT
);

CREATE TABLE conversation_members (
	id BIGSERIAL NOT NULL, 
	conversation_id BIGINT NOT NULL, 
	user_id BIGINT NOT NULL, 
	last_read_at TIMESTAMP WITH TIME ZONE, 
	PRIMARY KEY (id), 
	CONSTRAINT uq_conversation_member UNIQUE (conversation_id, user_id), 
	FOREIGN KEY(conversation_id) REFERENCES conversations (id) ON DELETE CASCADE, 
	FOREIGN KEY(user_id) REFERENCES users (id) ON DELETE CASCADE
);

CREATE TABLE course_offerings (
	id BIGSERIAL NOT NULL, 
	course_id BIGINT NOT NULL, 
	semester VARCHAR(30) NOT NULL, 
	section VARCHAR(60) NOT NULL, 
	PRIMARY KEY (id), 
	CONSTRAINT uq_offering UNIQUE (course_id, semester, section), 
	FOREIGN KEY(course_id) REFERENCES courses (id) ON DELETE CASCADE
);

CREATE TABLE credit_rules (
	key VARCHAR(80) NOT NULL, 
	label VARCHAR(120) NOT NULL, 
	kind VARCHAR(20) NOT NULL, 
	value INTEGER NOT NULL, 
	description VARCHAR(500) NOT NULL, 
	updated_by BIGINT, 
	updated_at TIMESTAMP WITH TIME ZONE NOT NULL, 
	PRIMARY KEY (key), 
	CONSTRAINT ck_credit_rule_kind CHECK (kind IN ('baseline', 'threshold', 'reward', 'penalty')), 
	CONSTRAINT ck_credit_rule_value CHECK (value >= -1000 AND value <= 1000), 
	FOREIGN KEY(updated_by) REFERENCES users (id) ON DELETE SET NULL
);

CREATE TABLE game_submissions (
	id BIGSERIAL NOT NULL, 
	submitter_id BIGINT NOT NULL, 
	proposed_name VARCHAR(80) NOT NULL, 
	aliases JSON NOT NULL, 
	status VARCHAR(20) NOT NULL, 
	resolved_game_id BIGINT, 
	reviewer_id BIGINT, 
	admin_note TEXT NOT NULL, 
	created_at TIMESTAMP WITH TIME ZONE NOT NULL, 
	reviewed_at TIMESTAMP WITH TIME ZONE, 
	PRIMARY KEY (id), 
	FOREIGN KEY(submitter_id) REFERENCES users (id) ON DELETE CASCADE, 
	FOREIGN KEY(resolved_game_id) REFERENCES team_games (id) ON DELETE SET NULL, 
	FOREIGN KEY(reviewer_id) REFERENCES users (id) ON DELETE SET NULL
);

CREATE TABLE notifications (
	id BIGSERIAL NOT NULL, 
	user_id BIGINT NOT NULL, 
	type VARCHAR(30) NOT NULL, 
	title VARCHAR(120) NOT NULL, 
	body TEXT NOT NULL, 
	link VARCHAR(500) NOT NULL, 
	read_at TIMESTAMP WITH TIME ZONE, 
	created_at TIMESTAMP WITH TIME ZONE NOT NULL, 
	PRIMARY KEY (id), 
	FOREIGN KEY(user_id) REFERENCES users (id) ON DELETE CASCADE
);

CREATE TABLE penalties (
	id BIGSERIAL NOT NULL, 
	user_id BIGINT NOT NULL, 
	public_mask VARCHAR(60) NOT NULL, 
	violation_type VARCHAR(120) NOT NULL, 
	result TEXT NOT NULL, 
	rule VARCHAR(160) NOT NULL, 
	created_at TIMESTAMP WITH TIME ZONE NOT NULL, 
	PRIMARY KEY (id), 
	FOREIGN KEY(user_id) REFERENCES users (id) ON DELETE RESTRICT
);

CREATE TABLE sessions (
	id BIGSERIAL NOT NULL, 
	user_id BIGINT NOT NULL, 
	token_hash VARCHAR(64) NOT NULL, 
	csrf_token VARCHAR(64) NOT NULL, 
	ip_address VARCHAR(64) NOT NULL, 
	user_agent VARCHAR(500) NOT NULL, 
	expires_at TIMESTAMP WITH TIME ZONE NOT NULL, 
	absolute_expires_at TIMESTAMP WITH TIME ZONE NOT NULL, 
	last_seen_at TIMESTAMP WITH TIME ZONE NOT NULL, 
	revoked_at TIMESTAMP WITH TIME ZONE, 
	created_at TIMESTAMP WITH TIME ZONE NOT NULL, 
	PRIMARY KEY (id), 
	FOREIGN KEY(user_id) REFERENCES users (id) ON DELETE CASCADE
);

CREATE TABLE settings (
	key VARCHAR(80) NOT NULL, 
	value TEXT NOT NULL, 
	updated_by BIGINT, 
	updated_at TIMESTAMP WITH TIME ZONE NOT NULL, 
	PRIMARY KEY (key), 
	FOREIGN KEY(updated_by) REFERENCES users (id) ON DELETE SET NULL
);

CREATE TABLE team_game_aliases (
	id BIGSERIAL NOT NULL, 
	game_id BIGINT NOT NULL, 
	alias VARCHAR(80) NOT NULL, 
	normalized_alias VARCHAR(80) NOT NULL, 
	PRIMARY KEY (id), 
	FOREIGN KEY(game_id) REFERENCES team_games (id) ON DELETE CASCADE
);

CREATE TABLE activities (
	entity_id BIGINT NOT NULL, 
	category VARCHAR(60) NOT NULL, 
	title VARCHAR(160) NOT NULL, 
	body TEXT NOT NULL, 
	location VARCHAR(160) NOT NULL, 
	starts_at TIMESTAMP WITH TIME ZONE NOT NULL, 
	ends_at TIMESTAMP WITH TIME ZONE, 
	capacity INTEGER, 
	status VARCHAR(20) NOT NULL, 
	PRIMARY KEY (entity_id), 
	FOREIGN KEY(entity_id) REFERENCES content_entities (id) ON DELETE CASCADE
);

CREATE TABLE appeals (
	id BIGSERIAL NOT NULL, 
	penalty_id BIGINT NOT NULL, 
	user_id BIGINT NOT NULL, 
	reason TEXT NOT NULL, 
	status VARCHAR(20) NOT NULL, 
	admin_note TEXT NOT NULL, 
	created_at TIMESTAMP WITH TIME ZONE NOT NULL, 
	PRIMARY KEY (id), 
	CONSTRAINT uq_appeal UNIQUE (penalty_id, user_id), 
	FOREIGN KEY(penalty_id) REFERENCES penalties (id) ON DELETE CASCADE, 
	FOREIGN KEY(user_id) REFERENCES users (id) ON DELETE CASCADE
);

CREATE TABLE attachments (
	id BIGSERIAL NOT NULL, 
	owner_id BIGINT NOT NULL, 
	entity_id BIGINT, 
	path VARCHAR(500) NOT NULL, 
	thumbnail_path VARCHAR(500) NOT NULL, 
	mime_type VARCHAR(100) NOT NULL, 
	size_bytes INTEGER NOT NULL, 
	width INTEGER NOT NULL, 
	height INTEGER NOT NULL, 
	status VARCHAR(20) NOT NULL, 
	created_at TIMESTAMP WITH TIME ZONE NOT NULL, 
	PRIMARY KEY (id), 
	FOREIGN KEY(owner_id) REFERENCES users (id) ON DELETE CASCADE, 
	FOREIGN KEY(entity_id) REFERENCES content_entities (id) ON DELETE CASCADE, 
	UNIQUE (path)
);

CREATE TABLE campus_service_ratings (
	id BIGSERIAL NOT NULL, 
	service_id BIGINT NOT NULL, 
	user_id BIGINT NOT NULL, 
	rating INTEGER NOT NULL, 
	body TEXT NOT NULL, 
	response TEXT NOT NULL, 
	responder_id BIGINT, 
	responded_at TIMESTAMP WITH TIME ZONE, 
	created_at TIMESTAMP WITH TIME ZONE NOT NULL, 
	updated_at TIMESTAMP WITH TIME ZONE NOT NULL, 
	PRIMARY KEY (id), 
	CONSTRAINT ck_campus_service_rating CHECK (rating >= 1 AND rating <= 5), 
	FOREIGN KEY(service_id) REFERENCES campus_services (id) ON DELETE CASCADE, 
	FOREIGN KEY(user_id) REFERENCES users (id) ON DELETE CASCADE, 
	FOREIGN KEY(responder_id) REFERENCES users (id) ON DELETE SET NULL
);

CREATE TABLE comments (
	entity_id BIGINT NOT NULL, 
	target_entity_id BIGINT NOT NULL, 
	parent_id BIGINT, 
	reply_to_user_id BIGINT, 
	body TEXT NOT NULL, 
	identity_mode VARCHAR(20) NOT NULL, 
	PRIMARY KEY (entity_id), 
	FOREIGN KEY(entity_id) REFERENCES content_entities (id) ON DELETE CASCADE, 
	FOREIGN KEY(target_entity_id) REFERENCES content_entities (id) ON DELETE CASCADE, 
	FOREIGN KEY(parent_id) REFERENCES comments (entity_id) ON DELETE CASCADE, 
	FOREIGN KEY(reply_to_user_id) REFERENCES users (id) ON DELETE SET NULL
);

CREATE TABLE content_revisions (
	id BIGSERIAL NOT NULL, 
	entity_id BIGINT NOT NULL, 
	editor_id BIGINT NOT NULL, 
	revision INTEGER NOT NULL, 
	title VARCHAR(160) NOT NULL, 
	body TEXT NOT NULL, 
	created_at TIMESTAMP WITH TIME ZONE NOT NULL, 
	PRIMARY KEY (id), 
	CONSTRAINT uq_content_revision UNIQUE (entity_id, revision), 
	FOREIGN KEY(entity_id) REFERENCES content_entities (id) ON DELETE CASCADE, 
	FOREIGN KEY(editor_id) REFERENCES users (id) ON DELETE RESTRICT
);

CREATE TABLE course_reviews (
	entity_id BIGINT NOT NULL, 
	offering_id BIGINT NOT NULL, 
	user_id BIGINT NOT NULL, 
	rating INTEGER NOT NULL, 
	tags VARCHAR(300) NOT NULL, 
	body TEXT NOT NULL, 
	correction TEXT NOT NULL, 
	PRIMARY KEY (entity_id), 
	CONSTRAINT ck_review_rating CHECK (rating >= 1 AND rating <= 5), 
	CONSTRAINT uq_course_review_user UNIQUE (offering_id, user_id), 
	FOREIGN KEY(entity_id) REFERENCES content_entities (id) ON DELETE CASCADE, 
	FOREIGN KEY(offering_id) REFERENCES course_offerings (id) ON DELETE CASCADE, 
	FOREIGN KEY(user_id) REFERENCES users (id) ON DELETE CASCADE
);

CREATE TABLE favorites (
	id BIGSERIAL NOT NULL, 
	entity_id BIGINT NOT NULL, 
	user_id BIGINT NOT NULL, 
	created_at TIMESTAMP WITH TIME ZONE NOT NULL, 
	PRIMARY KEY (id), 
	CONSTRAINT uq_favorite UNIQUE (entity_id, user_id), 
	FOREIGN KEY(entity_id) REFERENCES content_entities (id) ON DELETE CASCADE, 
	FOREIGN KEY(user_id) REFERENCES users (id) ON DELETE CASCADE
);

CREATE TABLE feedback (
	entity_id BIGINT NOT NULL, 
	type VARCHAR(30) NOT NULL, 
	title VARCHAR(160) NOT NULL, 
	body TEXT NOT NULL, 
	status VARCHAR(20) NOT NULL, 
	admin_note TEXT NOT NULL, 
	PRIMARY KEY (entity_id), 
	FOREIGN KEY(entity_id) REFERENCES content_entities (id) ON DELETE CASCADE
);

CREATE TABLE handbook_articles (
	entity_id BIGINT NOT NULL, 
	category VARCHAR(80) NOT NULL, 
	title VARCHAR(160) NOT NULL, 
	body TEXT NOT NULL, 
	featured_at TIMESTAMP WITH TIME ZONE, 
	featured_rewarded BOOLEAN NOT NULL, 
	PRIMARY KEY (entity_id), 
	FOREIGN KEY(entity_id) REFERENCES content_entities (id) ON DELETE CASCADE
);

CREATE TABLE listings (
	entity_id BIGINT NOT NULL, 
	category VARCHAR(60) NOT NULL, 
	title VARCHAR(160) NOT NULL, 
	description TEXT NOT NULL, 
	price FLOAT NOT NULL, 
	condition VARCHAR(80) NOT NULL, 
	negotiable BOOLEAN NOT NULL, 
	purchased_at DATE, 
	location VARCHAR(120) NOT NULL, 
	trade_status VARCHAR(20) NOT NULL, 
	PRIMARY KEY (entity_id), 
	CONSTRAINT ck_listing_price CHECK (price >= 0), 
	FOREIGN KEY(entity_id) REFERENCES content_entities (id) ON DELETE CASCADE
);

CREATE TABLE lost_items (
	entity_id BIGINT NOT NULL, 
	kind VARCHAR(20) NOT NULL, 
	item_name VARCHAR(160) NOT NULL, 
	description TEXT NOT NULL, 
	location VARCHAR(160) NOT NULL, 
	happened_at TIMESTAMP WITH TIME ZONE, 
	status VARCHAR(20) NOT NULL, 
	PRIMARY KEY (entity_id), 
	FOREIGN KEY(entity_id) REFERENCES content_entities (id) ON DELETE CASCADE
);

CREATE TABLE messages (
	entity_id BIGINT NOT NULL, 
	conversation_id BIGINT NOT NULL, 
	body TEXT NOT NULL, 
	PRIMARY KEY (entity_id), 
	FOREIGN KEY(entity_id) REFERENCES content_entities (id) ON DELETE CASCADE, 
	FOREIGN KEY(conversation_id) REFERENCES conversations (id) ON DELETE CASCADE
);

CREATE TABLE moderation_cases (
	id BIGSERIAL NOT NULL, 
	entity_id BIGINT NOT NULL, 
	source VARCHAR(30) NOT NULL, 
	status VARCHAR(20) NOT NULL, 
	assignee_id BIGINT, 
	decision VARCHAR(30) NOT NULL, 
	notes TEXT NOT NULL, 
	created_at TIMESTAMP WITH TIME ZONE NOT NULL, 
	decided_at TIMESTAMP WITH TIME ZONE, 
	PRIMARY KEY (id), 
	UNIQUE (entity_id), 
	FOREIGN KEY(entity_id) REFERENCES content_entities (id) ON DELETE CASCADE, 
	FOREIGN KEY(assignee_id) REFERENCES users (id) ON DELETE SET NULL
);

CREATE TABLE observe_posts (
	entity_id BIGINT NOT NULL, 
	title VARCHAR(160) NOT NULL, 
	body_masked TEXT NOT NULL, 
	body_raw TEXT NOT NULL, 
	respondent_id BIGINT, 
	response TEXT NOT NULL, 
	response_at TIMESTAMP WITH TIME ZONE, 
	admin_note TEXT NOT NULL, 
	PRIMARY KEY (entity_id), 
	FOREIGN KEY(entity_id) REFERENCES content_entities (id) ON DELETE CASCADE, 
	FOREIGN KEY(respondent_id) REFERENCES users (id) ON DELETE SET NULL
);

CREATE TABLE posts (
	entity_id BIGINT NOT NULL, 
	board VARCHAR(30) NOT NULL, 
	title VARCHAR(120) NOT NULL, 
	body TEXT NOT NULL, 
	identity_mode VARCHAR(20) NOT NULL, 
	expires_at TIMESTAMP WITH TIME ZONE, 
	views INTEGER NOT NULL, 
	PRIMARY KEY (entity_id), 
	FOREIGN KEY(entity_id) REFERENCES content_entities (id) ON DELETE CASCADE
);

CREATE TABLE questions (
	entity_id BIGINT NOT NULL, 
	title VARCHAR(160) NOT NULL, 
	body TEXT NOT NULL, 
	category VARCHAR(60) NOT NULL, 
	tags VARCHAR(300) NOT NULL, 
	bounty_xp INTEGER NOT NULL, 
	bounty_settled BOOLEAN NOT NULL, 
	accepted_answer_id BIGINT, 
	PRIMARY KEY (entity_id), 
	FOREIGN KEY(entity_id) REFERENCES content_entities (id) ON DELETE CASCADE
);

CREATE TABLE reactions (
	id BIGSERIAL NOT NULL, 
	entity_id BIGINT NOT NULL, 
	user_id BIGINT NOT NULL, 
	type VARCHAR(20) NOT NULL, 
	created_at TIMESTAMP WITH TIME ZONE NOT NULL, 
	PRIMARY KEY (id), 
	CONSTRAINT uq_reaction UNIQUE (entity_id, user_id, type), 
	FOREIGN KEY(entity_id) REFERENCES content_entities (id) ON DELETE CASCADE, 
	FOREIGN KEY(user_id) REFERENCES users (id) ON DELETE CASCADE
);

CREATE TABLE reports (
	id BIGSERIAL NOT NULL, 
	entity_id BIGINT NOT NULL, 
	reporter_id BIGINT NOT NULL, 
	reason VARCHAR(80) NOT NULL, 
	detail TEXT NOT NULL, 
	status VARCHAR(20) NOT NULL, 
	created_at TIMESTAMP WITH TIME ZONE NOT NULL, 
	PRIMARY KEY (id), 
	CONSTRAINT uq_report UNIQUE (entity_id, reporter_id), 
	FOREIGN KEY(entity_id) REFERENCES content_entities (id) ON DELETE CASCADE, 
	FOREIGN KEY(reporter_id) REFERENCES users (id) ON DELETE RESTRICT
);

CREATE TABLE teams (
	entity_id BIGINT NOT NULL, 
	owner_id BIGINT NOT NULL, 
	game_id BIGINT, 
	game VARCHAR(60) NOT NULL, 
	mode VARCHAR(80) NOT NULL, 
	rank_requirement VARCHAR(80) NOT NULL, 
	capacity INTEGER NOT NULL, 
	voice_name VARCHAR(80) NOT NULL, 
	voice_link VARCHAR(500) NOT NULL, 
	notes TEXT NOT NULL, 
	newbie_level VARCHAR(80) NOT NULL, 
	vibe VARCHAR(160) NOT NULL, 
	reminder_channels VARCHAR(160) NOT NULL, 
	recurrence VARCHAR(20) NOT NULL, 
	reminder_minutes INTEGER NOT NULL, 
	post_departure_retention_minutes INTEGER NOT NULL, 
	status VARCHAR(20) NOT NULL, 
	PRIMARY KEY (entity_id), 
	CONSTRAINT ck_team_capacity CHECK (capacity >= 2 AND capacity <= 99), 
	CONSTRAINT ck_team_retention CHECK (post_departure_retention_minutes >= 60 AND post_departure_retention_minutes <= 480), 
	FOREIGN KEY(entity_id) REFERENCES content_entities (id) ON DELETE CASCADE, 
	FOREIGN KEY(owner_id) REFERENCES users (id) ON DELETE RESTRICT, 
	FOREIGN KEY(game_id) REFERENCES team_games (id) ON DELETE SET NULL
);

CREATE TABLE thread_anonymous_identities (
	id BIGSERIAL NOT NULL, 
	thread_id BIGINT NOT NULL, 
	user_id BIGINT NOT NULL, 
	display_name VARCHAR(40) NOT NULL, 
	created_at TIMESTAMP WITH TIME ZONE NOT NULL, 
	PRIMARY KEY (id), 
	CONSTRAINT uq_thread_anonymous_user UNIQUE (thread_id, user_id), 
	CONSTRAINT uq_thread_anonymous_name UNIQUE (thread_id, display_name), 
	FOREIGN KEY(thread_id) REFERENCES content_entities (id) ON DELETE CASCADE, 
	FOREIGN KEY(user_id) REFERENCES users (id) ON DELETE CASCADE
);

CREATE TABLE activity_members (
	id BIGSERIAL NOT NULL, 
	activity_id BIGINT NOT NULL, 
	user_id BIGINT NOT NULL, 
	status VARCHAR(20) NOT NULL, 
	joined_at TIMESTAMP WITH TIME ZONE NOT NULL, 
	PRIMARY KEY (id), 
	CONSTRAINT uq_activity_member UNIQUE (activity_id, user_id), 
	FOREIGN KEY(activity_id) REFERENCES activities (entity_id) ON DELETE CASCADE, 
	FOREIGN KEY(user_id) REFERENCES users (id) ON DELETE CASCADE
);

CREATE TABLE answers (
	entity_id BIGINT NOT NULL, 
	question_id BIGINT NOT NULL, 
	body TEXT NOT NULL, 
	PRIMARY KEY (entity_id), 
	FOREIGN KEY(entity_id) REFERENCES content_entities (id) ON DELETE CASCADE, 
	FOREIGN KEY(question_id) REFERENCES questions (entity_id) ON DELETE CASCADE
);

CREATE TABLE lost_claims (
	id BIGSERIAL NOT NULL, 
	item_id BIGINT NOT NULL, 
	claimant_id BIGINT NOT NULL, 
	message TEXT NOT NULL, 
	status VARCHAR(20) NOT NULL, 
	created_at TIMESTAMP WITH TIME ZONE NOT NULL, 
	decided_at TIMESTAMP WITH TIME ZONE, 
	PRIMARY KEY (id), 
	CONSTRAINT uq_lost_claim UNIQUE (item_id, claimant_id), 
	FOREIGN KEY(item_id) REFERENCES lost_items (entity_id) ON DELETE CASCADE, 
	FOREIGN KEY(claimant_id) REFERENCES users (id) ON DELETE CASCADE
);

CREATE TABLE team_memberships (
	id BIGSERIAL NOT NULL, 
	team_id BIGINT NOT NULL, 
	user_id BIGINT NOT NULL, 
	role VARCHAR(20) NOT NULL, 
	status VARCHAR(20) NOT NULL, 
	reminder_channels VARCHAR(160) NOT NULL, 
	joined_at TIMESTAMP WITH TIME ZONE NOT NULL, 
	left_at TIMESTAMP WITH TIME ZONE, 
	PRIMARY KEY (id), 
	FOREIGN KEY(team_id) REFERENCES teams (entity_id) ON DELETE CASCADE, 
	FOREIGN KEY(user_id) REFERENCES users (id) ON DELETE CASCADE
);

CREATE TABLE team_runs (
	id BIGSERIAL NOT NULL, 
	team_id BIGINT NOT NULL, 
	starts_at TIMESTAMP WITH TIME ZONE NOT NULL, 
	expires_at TIMESTAMP WITH TIME ZONE, 
	status VARCHAR(20) NOT NULL, 
	reminder_sent_at TIMESTAMP WITH TIME ZONE, 
	created_at TIMESTAMP WITH TIME ZONE NOT NULL, 
	PRIMARY KEY (id), 
	CONSTRAINT uq_team_run_start UNIQUE (team_id, starts_at), 
	FOREIGN KEY(team_id) REFERENCES teams (entity_id) ON DELETE CASCADE
);

CREATE TABLE team_ratings (
	id BIGSERIAL NOT NULL, 
	run_id BIGINT NOT NULL, 
	rater_id BIGINT NOT NULL, 
	target_id BIGINT NOT NULL, 
	tag VARCHAR(30) NOT NULL, 
	created_at TIMESTAMP WITH TIME ZONE NOT NULL, 
	PRIMARY KEY (id), 
	CONSTRAINT uq_team_rating UNIQUE (run_id, rater_id, target_id, tag), 
	FOREIGN KEY(run_id) REFERENCES team_runs (id) ON DELETE CASCADE, 
	FOREIGN KEY(rater_id) REFERENCES users (id) ON DELETE CASCADE, 
	FOREIGN KEY(target_id) REFERENCES users (id) ON DELETE CASCADE
);

CREATE TABLE team_run_members (
	id BIGSERIAL NOT NULL, 
	run_id BIGINT NOT NULL, 
	user_id BIGINT NOT NULL, 
	status VARCHAR(20) NOT NULL, 
	checked_in_at TIMESTAMP WITH TIME ZONE, 
	excused_at TIMESTAMP WITH TIME ZONE, 
	credit_awarded BOOLEAN NOT NULL, 
	PRIMARY KEY (id), 
	CONSTRAINT uq_team_run_member UNIQUE (run_id, user_id), 
	FOREIGN KEY(run_id) REFERENCES team_runs (id) ON DELETE CASCADE, 
	FOREIGN KEY(user_id) REFERENCES users (id) ON DELETE CASCADE
);

CREATE INDEX ix_announcements_published_at ON announcements (published_at);
CREATE INDEX ix_courses_teacher ON courses (teacher);
CREATE INDEX ix_courses_name ON courses (name);
CREATE INDEX ix_email_outbox_status ON email_outbox (status);
CREATE INDEX ix_email_outbox_to_email ON email_outbox (to_email);
CREATE INDEX ix_rate_limit_events_action ON rate_limit_events (action);
CREATE INDEX ix_rate_limit_events_created_at ON rate_limit_events (created_at);
CREATE INDEX ix_rate_limit_events_subject ON rate_limit_events (subject);
CREATE INDEX ix_team_games_active ON team_games (active);
CREATE UNIQUE INDEX ix_team_games_name ON team_games (name);
CREATE INDEX ix_users_nickname ON users (nickname);
CREATE INDEX ix_users_role ON users (role);
CREATE INDEX ix_users_status ON users (status);
CREATE UNIQUE INDEX ix_users_email ON users (email);
CREATE INDEX ix_verification_codes_email ON verification_codes (email);
CREATE INDEX ix_verification_codes_purpose ON verification_codes (purpose);
CREATE INDEX ix_verification_codes_expires_at ON verification_codes (expires_at);
CREATE INDEX ix_announcement_reads_user_id ON announcement_reads (user_id);
CREATE INDEX ix_announcement_reads_announcement_id ON announcement_reads (announcement_id);
CREATE INDEX ix_audit_logs_created_at ON audit_logs (created_at);
CREATE INDEX ix_audit_logs_actor_id ON audit_logs (actor_id);
CREATE INDEX ix_audit_logs_action ON audit_logs (action);
CREATE INDEX ix_backup_jobs_status ON backup_jobs (status);
CREATE INDEX ix_blocks_user_id ON blocks (user_id);
CREATE INDEX ix_blocks_blocked_id ON blocks (blocked_id);
CREATE UNIQUE INDEX ix_campus_services_name ON campus_services (name);
CREATE INDEX ix_campus_services_manager_user_id ON campus_services (manager_user_id);
CREATE INDEX ix_campus_services_active ON campus_services (active);
CREATE INDEX ix_campus_services_category ON campus_services (category);
CREATE INDEX ix_content_entities_created_at ON content_entities (created_at);
CREATE INDEX ix_content_entities_owner_id ON content_entities (owner_id);
CREATE INDEX ix_content_entities_type ON content_entities (type);
CREATE INDEX ix_content_entities_status ON content_entities (status);
CREATE INDEX ix_conversation_members_user_id ON conversation_members (user_id);
CREATE INDEX ix_conversation_members_conversation_id ON conversation_members (conversation_id);
CREATE INDEX ix_course_offerings_semester ON course_offerings (semester);
CREATE INDEX ix_course_offerings_course_id ON course_offerings (course_id);
CREATE INDEX ix_credit_rules_kind ON credit_rules (kind);
CREATE INDEX ix_game_submissions_submitter_id ON game_submissions (submitter_id);
CREATE INDEX ix_game_submissions_status ON game_submissions (status);
CREATE INDEX ix_notifications_read_at ON notifications (read_at);
CREATE INDEX ix_notifications_created_at ON notifications (created_at);
CREATE INDEX ix_notifications_user_id ON notifications (user_id);
CREATE INDEX ix_penalties_user_id ON penalties (user_id);
CREATE INDEX ix_sessions_expires_at ON sessions (expires_at);
CREATE UNIQUE INDEX ix_sessions_token_hash ON sessions (token_hash);
CREATE INDEX ix_sessions_user_id ON sessions (user_id);
CREATE INDEX ix_team_game_aliases_game_id ON team_game_aliases (game_id);
CREATE UNIQUE INDEX ix_team_game_aliases_normalized_alias ON team_game_aliases (normalized_alias);
CREATE INDEX ix_activities_starts_at ON activities (starts_at);
CREATE INDEX ix_activities_category ON activities (category);
CREATE INDEX ix_activities_title ON activities (title);
CREATE INDEX ix_activities_status ON activities (status);
CREATE INDEX ix_appeals_user_id ON appeals (user_id);
CREATE INDEX ix_appeals_penalty_id ON appeals (penalty_id);
CREATE INDEX ix_appeals_status ON appeals (status);
CREATE INDEX ix_attachments_entity_id ON attachments (entity_id);
CREATE INDEX ix_attachments_owner_id ON attachments (owner_id);
CREATE INDEX ix_attachments_status ON attachments (status);
CREATE INDEX ix_campus_service_ratings_service_id ON campus_service_ratings (service_id);
CREATE INDEX ix_campus_service_ratings_created_at ON campus_service_ratings (created_at);
CREATE INDEX ix_campus_service_ratings_user_id ON campus_service_ratings (user_id);
CREATE INDEX ix_comments_target_entity_id ON comments (target_entity_id);
CREATE INDEX ix_comments_parent_id ON comments (parent_id);
CREATE INDEX ix_content_revisions_entity_id ON content_revisions (entity_id);
CREATE INDEX ix_content_revisions_editor_id ON content_revisions (editor_id);
CREATE INDEX ix_course_reviews_user_id ON course_reviews (user_id);
CREATE INDEX ix_course_reviews_offering_id ON course_reviews (offering_id);
CREATE INDEX ix_favorites_entity_id ON favorites (entity_id);
CREATE INDEX ix_favorites_user_id ON favorites (user_id);
CREATE INDEX ix_handbook_articles_title ON handbook_articles (title);
CREATE INDEX ix_handbook_articles_category ON handbook_articles (category);
CREATE INDEX ix_listings_title ON listings (title);
CREATE INDEX ix_listings_trade_status ON listings (trade_status);
CREATE INDEX ix_listings_category ON listings (category);
CREATE INDEX ix_lost_items_status ON lost_items (status);
CREATE INDEX ix_lost_items_item_name ON lost_items (item_name);
CREATE INDEX ix_messages_conversation_id ON messages (conversation_id);
CREATE INDEX ix_moderation_cases_status ON moderation_cases (status);
CREATE INDEX ix_observe_posts_respondent_id ON observe_posts (respondent_id);
CREATE INDEX ix_posts_search ON posts (title, body);
CREATE INDEX ix_posts_board ON posts (board);
CREATE INDEX ix_posts_expires_at ON posts (expires_at);
CREATE INDEX ix_questions_category ON questions (category);
CREATE INDEX ix_questions_title ON questions (title);
CREATE INDEX ix_reactions_user_id ON reactions (user_id);
CREATE INDEX ix_reactions_entity_id ON reactions (entity_id);
CREATE INDEX ix_reports_reporter_id ON reports (reporter_id);
CREATE INDEX ix_reports_entity_id ON reports (entity_id);
CREATE INDEX ix_reports_status ON reports (status);
CREATE INDEX ix_teams_status ON teams (status);
CREATE INDEX ix_teams_game_id ON teams (game_id);
CREATE INDEX ix_teams_owner_id ON teams (owner_id);
CREATE INDEX ix_teams_game ON teams (game);
CREATE INDEX ix_thread_anonymous_identities_thread_id ON thread_anonymous_identities (thread_id);
CREATE INDEX ix_thread_anonymous_identities_user_id ON thread_anonymous_identities (user_id);
CREATE INDEX ix_activity_members_user_id ON activity_members (user_id);
CREATE INDEX ix_activity_members_activity_id ON activity_members (activity_id);
CREATE INDEX ix_answers_question_id ON answers (question_id);
CREATE INDEX ix_lost_claims_status ON lost_claims (status);
CREATE INDEX ix_lost_claims_item_id ON lost_claims (item_id);
CREATE INDEX ix_lost_claims_claimant_id ON lost_claims (claimant_id);
CREATE INDEX ix_team_memberships_user_id ON team_memberships (user_id);
CREATE INDEX ix_team_memberships_team_id ON team_memberships (team_id);
CREATE INDEX ix_team_memberships_status ON team_memberships (status);
CREATE INDEX ix_team_runs_team_id ON team_runs (team_id);
CREATE INDEX ix_team_runs_status ON team_runs (status);
CREATE INDEX ix_team_runs_starts_at ON team_runs (starts_at);
CREATE INDEX ix_team_runs_expires_at ON team_runs (expires_at);
CREATE INDEX ix_team_ratings_run_id ON team_ratings (run_id);
CREATE INDEX ix_team_run_members_run_id ON team_run_members (run_id);
CREATE INDEX ix_team_run_members_user_id ON team_run_members (user_id);
CREATE INDEX IF NOT EXISTS ix_posts_title_trgm ON posts USING gin (title gin_trgm_ops);
CREATE INDEX IF NOT EXISTS ix_questions_title_trgm ON questions USING gin (title gin_trgm_ops);
CREATE INDEX IF NOT EXISTS ix_listings_title_trgm ON listings USING gin (title gin_trgm_ops);

-- +goose Down
DROP TABLE IF EXISTS team_run_members CASCADE;
DROP TABLE IF EXISTS team_ratings CASCADE;
DROP TABLE IF EXISTS team_runs CASCADE;
DROP TABLE IF EXISTS team_memberships CASCADE;
DROP TABLE IF EXISTS lost_claims CASCADE;
DROP TABLE IF EXISTS answers CASCADE;
DROP TABLE IF EXISTS activity_members CASCADE;
DROP TABLE IF EXISTS thread_anonymous_identities CASCADE;
DROP TABLE IF EXISTS teams CASCADE;
DROP TABLE IF EXISTS reports CASCADE;
DROP TABLE IF EXISTS reactions CASCADE;
DROP TABLE IF EXISTS questions CASCADE;
DROP TABLE IF EXISTS posts CASCADE;
DROP TABLE IF EXISTS observe_posts CASCADE;
DROP TABLE IF EXISTS moderation_cases CASCADE;
DROP TABLE IF EXISTS messages CASCADE;
DROP TABLE IF EXISTS lost_items CASCADE;
DROP TABLE IF EXISTS listings CASCADE;
DROP TABLE IF EXISTS handbook_articles CASCADE;
DROP TABLE IF EXISTS feedback CASCADE;
DROP TABLE IF EXISTS favorites CASCADE;
DROP TABLE IF EXISTS course_reviews CASCADE;
DROP TABLE IF EXISTS content_revisions CASCADE;
DROP TABLE IF EXISTS comments CASCADE;
DROP TABLE IF EXISTS campus_service_ratings CASCADE;
DROP TABLE IF EXISTS attachments CASCADE;
DROP TABLE IF EXISTS appeals CASCADE;
DROP TABLE IF EXISTS activities CASCADE;
DROP TABLE IF EXISTS team_game_aliases CASCADE;
DROP TABLE IF EXISTS settings CASCADE;
DROP TABLE IF EXISTS sessions CASCADE;
DROP TABLE IF EXISTS penalties CASCADE;
DROP TABLE IF EXISTS notifications CASCADE;
DROP TABLE IF EXISTS game_submissions CASCADE;
DROP TABLE IF EXISTS credit_rules CASCADE;
DROP TABLE IF EXISTS course_offerings CASCADE;
DROP TABLE IF EXISTS conversation_members CASCADE;
DROP TABLE IF EXISTS content_entities CASCADE;
DROP TABLE IF EXISTS campus_services CASCADE;
DROP TABLE IF EXISTS blocks CASCADE;
DROP TABLE IF EXISTS backup_jobs CASCADE;
DROP TABLE IF EXISTS audit_logs CASCADE;
DROP TABLE IF EXISTS announcement_reads CASCADE;
DROP TABLE IF EXISTS verification_codes CASCADE;
DROP TABLE IF EXISTS users CASCADE;
DROP TABLE IF EXISTS team_games CASCADE;
DROP TABLE IF EXISTS rate_limit_events CASCADE;
DROP TABLE IF EXISTS email_outbox CASCADE;
DROP TABLE IF EXISTS courses CASCADE;
DROP TABLE IF EXISTS conversations CASCADE;
DROP TABLE IF EXISTS announcements CASCADE;

-- name: GetUserByEmail :one
SELECT id, email, password_hash, nickname, alias, campus_identity, role, status,
       credit, xp, avatar_path, dm_stranger_off, hide_online, verified_at, created_at
FROM users
WHERE email = $1;

-- name: GetSessionByTokenHash :one
SELECT id, user_id, token_hash, csrf_token, ip_address, user_agent, expires_at,
       absolute_expires_at, last_seen_at, revoked_at, created_at
FROM sessions
WHERE token_hash = $1;

-- name: CountUnreadNotifications :one
SELECT count(*)
FROM notifications
WHERE user_id = $1 AND read_at IS NULL;

-- +goose Up
ALTER TABLE sessions
    ADD COLUMN previous_token_hash VARCHAR(64),
    ADD COLUMN previous_token_expires_at TIMESTAMP WITH TIME ZONE;

CREATE INDEX ix_sessions_previous_token_hash
    ON sessions (previous_token_hash)
    WHERE previous_token_hash IS NOT NULL;

-- +goose Down
DROP INDEX IF EXISTS ix_sessions_previous_token_hash;
ALTER TABLE sessions
    DROP COLUMN previous_token_expires_at,
    DROP COLUMN previous_token_hash;

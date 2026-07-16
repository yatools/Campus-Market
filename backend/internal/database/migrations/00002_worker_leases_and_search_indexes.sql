-- +goose Up
ALTER TABLE email_outbox
    ADD COLUMN lease_until TIMESTAMP WITH TIME ZONE,
    ADD COLUMN worker_id VARCHAR(160),
    ADD COLUMN processing_at TIMESTAMP WITH TIME ZONE;

CREATE INDEX ix_email_outbox_claim
    ON email_outbox (status, next_attempt_at, lease_until, id);

-- +goose StatementBegin
CREATE FUNCTION notify_notification_change() RETURNS trigger AS $$
BEGIN
    PERFORM pg_notify('wutong_notifications', NEW.user_id::text);
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;
-- +goose StatementEnd

CREATE TRIGGER trg_notifications_changed
    AFTER INSERT OR UPDATE OF read_at ON notifications
    FOR EACH ROW EXECUTE FUNCTION notify_notification_change();

CREATE INDEX ix_posts_body_trgm ON posts USING gin (body gin_trgm_ops);
CREATE INDEX ix_questions_body_trgm ON questions USING gin (body gin_trgm_ops);
CREATE INDEX ix_handbook_articles_title_trgm ON handbook_articles USING gin (title gin_trgm_ops);
CREATE INDEX ix_handbook_articles_body_trgm ON handbook_articles USING gin (body gin_trgm_ops);
CREATE INDEX ix_listings_description_trgm ON listings USING gin (description gin_trgm_ops);
CREATE INDEX ix_activities_title_trgm ON activities USING gin (title gin_trgm_ops);
CREATE INDEX ix_activities_body_trgm ON activities USING gin (body gin_trgm_ops);
CREATE INDEX ix_lost_items_name_trgm ON lost_items USING gin (item_name gin_trgm_ops);
CREATE INDEX ix_lost_items_description_trgm ON lost_items USING gin (description gin_trgm_ops);

-- +goose Down
DROP TRIGGER IF EXISTS trg_notifications_changed ON notifications;
DROP FUNCTION IF EXISTS notify_notification_change();
DROP INDEX IF EXISTS ix_lost_items_description_trgm;
DROP INDEX IF EXISTS ix_lost_items_name_trgm;
DROP INDEX IF EXISTS ix_activities_body_trgm;
DROP INDEX IF EXISTS ix_activities_title_trgm;
DROP INDEX IF EXISTS ix_listings_description_trgm;
DROP INDEX IF EXISTS ix_handbook_articles_body_trgm;
DROP INDEX IF EXISTS ix_handbook_articles_title_trgm;
DROP INDEX IF EXISTS ix_questions_body_trgm;
DROP INDEX IF EXISTS ix_posts_body_trgm;
DROP INDEX IF EXISTS ix_email_outbox_claim;
ALTER TABLE email_outbox
    DROP COLUMN processing_at,
    DROP COLUMN worker_id,
    DROP COLUMN lease_until;

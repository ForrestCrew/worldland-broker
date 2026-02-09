-- 012: Store SSH credentials in rental_sessions for Hub restart persistence
-- Previously SSH info was only in-memory; lost on Hub restart

ALTER TABLE rental_sessions ADD COLUMN IF NOT EXISTS ssh_host VARCHAR(255);
ALTER TABLE rental_sessions ADD COLUMN IF NOT EXISTS ssh_port INTEGER;
ALTER TABLE rental_sessions ADD COLUMN IF NOT EXISTS ssh_user VARCHAR(64) DEFAULT 'ubuntu';
ALTER TABLE rental_sessions ADD COLUMN IF NOT EXISTS ssh_password VARCHAR(128);

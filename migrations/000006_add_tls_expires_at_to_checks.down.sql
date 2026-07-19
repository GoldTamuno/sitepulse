DROP INDEX IF EXISTS idx_checks_tls_expires_at;
ALTER TABLE checks DROP COLUMN IF EXISTS tls_expires_at;

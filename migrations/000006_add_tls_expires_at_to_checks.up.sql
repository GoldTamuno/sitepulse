ALTER TABLE checks ADD COLUMN tls_expires_at TIMESTAMPTZ;

-- Supports Phase 7's future "certificates expiring soon" query. Partial
-- index (only rows that actually have a value) since the large majority of
-- http:// monitors or failed-before-TLS checks will have NULL here, and
-- indexing NULLs would waste space for no query benefit.
CREATE INDEX idx_checks_tls_expires_at ON checks (tls_expires_at) WHERE tls_expires_at IS NOT NULL;

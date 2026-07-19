CREATE TABLE checks (
    id                BIGSERIAL PRIMARY KEY,
    monitor_id        BIGINT NOT NULL REFERENCES monitors(id) ON DELETE CASCADE,
    status            TEXT NOT NULL CHECK (status IN ('up', 'down', 'slow')),
    status_code       INT,          -- NULL when the request never got a response (timeout, DNS failure, etc.)
    response_time_ms  INT NOT NULL,
    error             TEXT,         -- empty/NULL on success
    checked_at        TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- This is the index that matters most in the whole schema. Every read this
-- table serves — "recent checks for monitor X", "uptime % for monitor X
-- since time T" — filters on monitor_id and orders/ranges by checked_at.
-- DESC matches our most common query pattern (most recent checks first).
CREATE INDEX idx_checks_monitor_time ON checks (monitor_id, checked_at DESC);

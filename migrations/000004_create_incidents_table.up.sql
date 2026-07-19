CREATE TABLE incidents (
    id           BIGSERIAL PRIMARY KEY,
    monitor_id   BIGINT NOT NULL REFERENCES monitors(id) ON DELETE CASCADE,
    status       TEXT NOT NULL DEFAULT 'open' CHECK (status IN ('open', 'resolved')),
    started_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    resolved_at  TIMESTAMPTZ,
    cause        TEXT
);

CREATE INDEX idx_incidents_monitor_id ON incidents (monitor_id, started_at DESC);

-- Database-enforced invariant: a monitor can have at most one OPEN incident
-- at a time. This isn't just a query optimization — it's a correctness
-- guarantee. Without it, a race between two scheduler ticks (or a bug in
-- the "open incident on down" logic) could silently create duplicate open
-- incidents for the same outage. With it, that INSERT fails loudly instead.
CREATE UNIQUE INDEX idx_incidents_one_open_per_monitor
    ON incidents (monitor_id)
    WHERE status = 'open';

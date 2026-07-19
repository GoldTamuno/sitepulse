CREATE TABLE monitors (
    id                    BIGSERIAL PRIMARY KEY,
    user_id               BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    name                  TEXT NOT NULL,
    url                   TEXT NOT NULL,
    type                  TEXT NOT NULL DEFAULT 'http' CHECK (type IN ('http', 'tcp')),
    interval_seconds      INT NOT NULL DEFAULT 60 CHECK (interval_seconds >= 10),
    timeout_seconds       INT NOT NULL DEFAULT 10 CHECK (timeout_seconds >= 1),
    expected_status_code  INT NOT NULL DEFAULT 200,
    active                BOOLEAN NOT NULL DEFAULT true,
    created_at            TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at            TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Supports "list my monitors" (dashboard, CRUD listing).
CREATE INDEX idx_monitors_user_id ON monitors (user_id);

-- Supports the scheduler's core query: "which active monitors exist".
-- Partial index (only indexes rows where active = true) keeps this index
-- small and fast even if users accumulate thousands of disabled monitors
-- over time — Postgres never has to look at rows that don't match.
CREATE INDEX idx_monitors_active ON monitors (id) WHERE active = true;

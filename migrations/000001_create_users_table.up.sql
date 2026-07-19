CREATE TABLE users (
    id             BIGSERIAL PRIMARY KEY,
    email          TEXT NOT NULL UNIQUE,
    password_hash  TEXT NOT NULL,
    -- Three-tier RBAC: admin manages users/system, operator manages
    -- monitors, viewer has read-only access. Defaulting new signups to the
    -- least-privileged role ("viewer") is a secure-by-default choice —
    -- privilege must be explicitly granted by an admin, never assumed.
    role           TEXT NOT NULL DEFAULT 'viewer' CHECK (role IN ('admin', 'operator', 'viewer')),
    email_verified BOOLEAN NOT NULL DEFAULT false,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Case-insensitive lookups matter here: email uniqueness and login should
-- both treat "User@x.com" and "user@x.com" as the same account. We enforce
-- that at the application layer (normalize to lowercase before insert), but
-- an index on a lowered expression protects us even if application code
-- ever forgets to normalize.
CREATE UNIQUE INDEX idx_users_email_lower ON users (LOWER(email));

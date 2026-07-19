CREATE TABLE refresh_tokens (
    id          BIGSERIAL PRIMARY KEY,
    user_id     BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    -- SHA-256 hex digest of the raw refresh token. Never store the raw
    -- token — see domain.RefreshToken doc comment for why.
    token_hash  TEXT NOT NULL UNIQUE,
    -- Groups a chain of rotated tokens. On refresh, we issue a new token
    -- with the SAME family_id and revoke the old one. If a revoked token
    -- is ever presented again (theft/replay), we revoke the whole family.
    family_id   TEXT NOT NULL,
    expires_at  TIMESTAMPTZ NOT NULL,
    revoked     BOOLEAN NOT NULL DEFAULT false,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Supports the hot-path lookup on every refresh request.
CREATE INDEX idx_refresh_tokens_hash ON refresh_tokens (token_hash);

-- Supports "revoke the whole family" on reuse detection.
CREATE INDEX idx_refresh_tokens_family ON refresh_tokens (family_id);

-- Supports the periodic cleanup job (DeleteExpired) so this table doesn't
-- grow unbounded with dead tokens.
CREATE INDEX idx_refresh_tokens_expires_at ON refresh_tokens (expires_at);

-- Accounts and renewable sessions. There is no sign-up: the owner creates each
-- account from the server with `api user create`.

-- +goose Up
CREATE TABLE users (
    id            UUID         PRIMARY KEY,
    -- Stored trimmed and lowercase, so the unique index ignores case.
    username      VARCHAR(40)  NOT NULL,
    display_name  VARCHAR(60)  NOT NULL,
    -- argon2id in PHC format: $argon2id$v=19$m=...,t=...,p=...$salt$hash
    password_hash VARCHAR(255) NOT NULL,
    -- Deactivating instead of deleting cuts access at once and keeps the history.
    is_active     BOOLEAN      NOT NULL DEFAULT true,
    created_at    TIMESTAMPTZ  NOT NULL DEFAULT now(),
    updated_at    TIMESTAMPTZ  NOT NULL DEFAULT now(),
    last_login_at TIMESTAMPTZ,
    CONSTRAINT users_username_format CHECK (username ~ '^[a-z0-9][a-z0-9._-]{2,39}$')
);
CREATE UNIQUE INDEX users_username_key ON users (username);

-- One per device. The access token carries its id (sid) and every request
-- checks that the session is still alive.
CREATE TABLE sessions (
    id             UUID         PRIMARY KEY,
    user_id        UUID         NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    is_web         BOOLEAN      NOT NULL DEFAULT false,
    created_at     TIMESTAMPTZ  NOT NULL DEFAULT now(),
    last_used_at   TIMESTAMPTZ  NOT NULL DEFAULT now(),
    revoked_at     TIMESTAMPTZ,
    revoked_reason VARCHAR(30),
    user_agent     VARCHAR(255)
);
CREATE INDEX sessions_user_active_idx ON sessions (user_id) WHERE revoked_at IS NULL;

-- Only the SHA-256 hash: the token in clear text only exists on the device.
CREATE TABLE refresh_tokens (
    id         UUID        PRIMARY KEY,
    session_id UUID        NOT NULL REFERENCES sessions (id) ON DELETE CASCADE,
    token_hash BYTEA       NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    expires_at TIMESTAMPTZ NOT NULL,
    used_at    TIMESTAMPTZ
);
CREATE UNIQUE INDEX refresh_tokens_token_hash_key ON refresh_tokens (token_hash);
CREATE INDEX refresh_tokens_session_idx ON refresh_tokens (session_id);

-- +goose Down
DROP TABLE refresh_tokens;
DROP TABLE sessions;
DROP TABLE users;

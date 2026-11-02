-- Each owner's car, who borrows it, the per-km rates and the share link.

-- +goose Up
CREATE TABLE user_settings (
    -- The user id is the primary key: it expresses the one-to-one without an extra column.
    user_id       UUID         PRIMARY KEY REFERENCES users (id) ON DELETE CASCADE,
    car_name      VARCHAR(60)  NOT NULL DEFAULT 'Mi auto',
    borrower_name VARCHAR(40)  NOT NULL DEFAULT 'Familiar',
    -- Soles per km. A trip copies the rates it was charged with, so editing
    -- these never changes an amount already registered.
    gasoline_rate NUMERIC(8,3) NOT NULL DEFAULT 0.30 CHECK (gasoline_rate >= 0),
    lpg_rate      NUMERIC(8,3) NOT NULL DEFAULT 0.10 CHECK (lpg_rate >= 0),
    wear_rate     NUMERIC(8,3) NOT NULL DEFAULT 0.30 CHECK (wear_rate >= 0),
    -- Goes in the public URL instead of the user id, so a leaked link can be
    -- revoked by rotating it.
    share_token   UUID         NOT NULL DEFAULT gen_random_uuid(),
    created_at    TIMESTAMPTZ  NOT NULL DEFAULT now(),
    updated_at    TIMESTAMPTZ  NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX user_settings_share_token_key ON user_settings (share_token);

-- +goose Down
DROP TABLE user_settings;

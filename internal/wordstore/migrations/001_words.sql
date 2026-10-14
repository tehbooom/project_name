CREATE TABLE IF NOT EXISTS words (
    id text PRIMARY KEY,
    value text NOT NULL,
    category text NOT NULL CHECK (category IN ('nouns', 'adjectives')),
    status text NOT NULL CHECK (status IN ('proposed', 'approved')),
    safe boolean NOT NULL DEFAULT true,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (category, value)
);

ALTER TABLE words ADD COLUMN IF NOT EXISTS safe boolean NOT NULL DEFAULT true;

CREATE INDEX IF NOT EXISTS words_list_idx
    ON words (category, status, value, id);

CREATE INDEX IF NOT EXISTS words_random_idx
    ON words (category, status);

CREATE INDEX IF NOT EXISTS words_safe_random_idx
    ON words (category, status, safe);

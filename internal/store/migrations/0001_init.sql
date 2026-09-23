-- Backs idgen's block allocator (see internal/idgen). INCREMENT BY 1000
-- means one nextval() call reserves a whole block: the block's first ID
-- is (nextval() - 999). Sequences are non-transactional in Postgres (a
-- rolled-back transaction does not give back the value), which is
-- exactly the "never reuse, gaps are fine" guarantee an ID generator
-- needs, and unlike a Redis counter it can't be silently evicted under
-- memory pressure.
CREATE SEQUENCE IF NOT EXISTS link_id_seq
    AS BIGINT
    INCREMENT BY 1000
    START WITH 60466176; -- 62^4: guarantees short codes are at least 5 characters

CREATE TABLE IF NOT EXISTS links (
    id          BIGINT PRIMARY KEY,
    code        VARCHAR(32) NOT NULL UNIQUE,
    long_url    TEXT NOT NULL,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    expires_at  TIMESTAMPTZ,
    clicks      BIGINT NOT NULL DEFAULT 0
);

CREATE INDEX IF NOT EXISTS idx_links_expires_at ON links (expires_at) WHERE expires_at IS NOT NULL;

CREATE TABLE IF NOT EXISTS click_events (
    id          BIGSERIAL PRIMARY KEY,
    code        VARCHAR(32) NOT NULL,
    ts          TIMESTAMPTZ NOT NULL,
    ip          TEXT,
    user_agent  TEXT,
    referrer    TEXT
);

CREATE INDEX IF NOT EXISTS idx_click_events_code_ts ON click_events (code, ts);

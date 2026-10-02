-- Messages the assistant sent the owner without being asked (owner decision
-- 2026-10-02): a morning message when the records show they woke, and a
-- check-in after a long stretch outside the day's tasks. A row with an empty
-- message is a decision to stay quiet. The log is what lets the assistant
-- see, in a later conversation, what was said in its name.

CREATE TABLE IF NOT EXISTS outreach_log (
    id BIGSERIAL PRIMARY KEY,
    kind TEXT NOT NULL,
    message TEXT NOT NULL DEFAULT '',
    facts TEXT NOT NULL DEFAULT '',
    delivered BOOLEAN NOT NULL DEFAULT false,
    error TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT outreach_log_kind_check CHECK (kind IN ('morning', 'off_plan'))
);

CREATE INDEX IF NOT EXISTS outreach_log_created_index ON outreach_log (created_at DESC);

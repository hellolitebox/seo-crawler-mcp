CREATE TABLE IF NOT EXISTS agent_readiness_checks (
    id                    INTEGER PRIMARY KEY AUTOINCREMENT,
    job_id                TEXT NOT NULL REFERENCES crawl_jobs(id) ON DELETE CASCADE,
    category              TEXT NOT NULL,
    check_key             TEXT NOT NULL,
    status                TEXT NOT NULL,
    score                 INTEGER NOT NULL DEFAULT 0,
    target_url            TEXT NOT NULL,
    endpoint              TEXT NOT NULL,
    method                TEXT NOT NULL DEFAULT 'GET',
    request_headers_json  TEXT,
    response_status       INTEGER,
    response_headers_json TEXT,
    evidence_json         TEXT NOT NULL DEFAULT '{}',
    recommendation        TEXT,
    resources_json        TEXT NOT NULL DEFAULT '[]',
    checked_at            TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    UNIQUE(job_id, category, check_key, target_url)
);

CREATE INDEX IF NOT EXISTS idx_agent_readiness_job
ON agent_readiness_checks(job_id, category, check_key);

-- Keep Live Activity incremental queries index-only enough to stay responsive
-- on databases with large historical crawls.
CREATE INDEX IF NOT EXISTS idx_fetches_job_id ON fetches(job_id, id);
CREATE INDEX IF NOT EXISTS idx_crawl_events_job_type_id ON crawl_events(job_id, event_type, id);
CREATE INDEX IF NOT EXISTS idx_issues_job_id ON issues(job_id, id);

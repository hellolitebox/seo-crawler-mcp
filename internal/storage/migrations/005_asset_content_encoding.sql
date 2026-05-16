-- Add compression metadata for assets captured by HEAD/full fetches.
ALTER TABLE assets ADD COLUMN content_encoding TEXT;

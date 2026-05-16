-- Rich asset metadata used by the Images & Assets report.
ALTER TABLE assets ADD COLUMN cache_control TEXT;
ALTER TABLE assets ADD COLUMN transfer_size INTEGER;
ALTER TABLE assets ADD COLUMN decoded_size INTEGER;

ALTER TABLE asset_references ADD COLUMN natural_width INTEGER;
ALTER TABLE asset_references ADD COLUMN natural_height INTEGER;
ALTER TABLE asset_references ADD COLUMN rendered_width INTEGER;
ALTER TABLE asset_references ADD COLUMN rendered_height INTEGER;

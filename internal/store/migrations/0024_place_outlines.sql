-- A place drawn as an outline (a hospital's grounds, a market's building) is
-- matched by the box around it, so standing inside it counts as being there
-- rather than tens of metres from its centre.
ALTER TABLE map_places ADD COLUMN IF NOT EXISTS min_lat DOUBLE PRECISION;
ALTER TABLE map_places ADD COLUMN IF NOT EXISTS max_lat DOUBLE PRECISION;
ALTER TABLE map_places ADD COLUMN IF NOT EXISTS min_lon DOUBLE PRECISION;
ALTER TABLE map_places ADD COLUMN IF NOT EXISTS max_lon DOUBLE PRECISION;
CREATE INDEX IF NOT EXISTS map_places_outline ON map_places (min_lat, max_lat) WHERE min_lat IS NOT NULL;

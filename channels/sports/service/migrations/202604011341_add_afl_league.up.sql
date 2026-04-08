-- Remove any existing AFL entries first to ensure clean slate
DELETE FROM tracked_leagues WHERE name IN ('AFL', 'Australian AFL');

-- Insert AFL with correct sport_api
INSERT INTO tracked_leagues (name, sport_api, api_host, league_id, category, country, logo_url, season, season_format, is_enabled, offseason_months)
VALUES (
  'Australian AFL',
  'australian-football',
  'v1.afl.api-sports.io',
  '4456',
  'Australian Football',
  'Australia',
  'https://media.api-sports.io/flags/au.svg',
  '2026',
  'calendar',
  true,
  ARRAY[2, 3, 4, 5, 6, 7]
);

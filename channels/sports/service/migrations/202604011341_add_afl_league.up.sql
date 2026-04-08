INSERT INTO tracked_leagues (name, sport_api, api_host, league_id, category, country, logo_url, season, season_format, is_enabled, offseason_months)
VALUES (
  'Australian AFL',
  'australian-football',
  'v1.afl.api-sports.io',
  '4456',
  'australian-football',
  'Australia',
  'https://media.api-sports.io/flags/au.svg',
  '2026',
  'calendar',
  true,
  ARRAY[2, 3, 4, 5, 6, 7]
)
ON CONFLICT (name) DO UPDATE SET
  sport_api = EXCLUDED.sport_api,
  api_host = EXCLUDED.api_host,
  league_id = EXCLUDED.league_id,
  is_enabled = true;

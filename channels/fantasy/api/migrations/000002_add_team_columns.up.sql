-- Add team_key and team_name columns to yahoo_user_leagues
DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM information_schema.columns
        WHERE table_name = 'yahoo_user_leagues' AND column_name = 'team_key'
    ) THEN
        ALTER TABLE yahoo_user_leagues ADD COLUMN team_key VARCHAR(50);
    END IF;
END $$;

DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM information_schema.columns
        WHERE table_name = 'yahoo_user_leagues' AND column_name = 'team_name'
    ) THEN
        ALTER TABLE yahoo_user_leagues ADD COLUMN team_name VARCHAR(255);
    END IF;
END $$;

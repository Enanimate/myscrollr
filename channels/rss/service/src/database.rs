use std::{env, time::Duration, sync::Arc};
use anyhow::{Context, Result};
use sqlx::postgres::{PgConnectOptions, PgPoolOptions};
pub use sqlx::PgPool;
use sqlx::{FromRow, query, query_as};
use serde::Deserialize;
use chrono::{DateTime, Utc};

pub async fn initialize_pool() -> Result<PgPool> {
    let pool_options = PgPoolOptions::new()
        .max_connections(20)
        .min_connections(1)
        .acquire_timeout(Duration::from_secs(10))
        .idle_timeout(Duration::from_millis(30_000));

    if let Ok(mut database_url) = env::var("DATABASE_URL") {
        database_url = database_url.trim().trim_matches('"').trim_matches('\'').to_string();
        if database_url.starts_with("postgres:") && !database_url.starts_with("postgres://") {
            database_url = database_url.replacen("postgres:", "postgres://", 1);
        } else if database_url.starts_with("postgresql:") && !database_url.starts_with("postgresql://") {
            database_url = database_url.replacen("postgresql:", "postgresql://", 1);
        }
        let pool = pool_options.connect(&database_url).await.context("Failed to connect to the PostgreSQL database via DATABASE_URL")?;
        return Ok(pool);
    }

    let get_env_var = |key: &str| -> Result<String> {
        env::var(key).with_context(|| format!("Missing environment variable: {}", key))
    };

    let raw_host = get_env_var("DB_HOST")?;
    let port_str = get_env_var("DB_PORT")?;
    let user = get_env_var("DB_USER")?;
    let password = get_env_var("DB_PASSWORD")?;
    let database = get_env_var("DB_DATABASE")?;

    let host = if let Some(fixed) = raw_host.strip_prefix("db.") { fixed } else { &raw_host };
    let port: u16 = port_str.parse().context("DB_PORT must be a valid u16 integer")?;

    let connect_options = PgConnectOptions::new().host(host).port(port).username(&user).password(&password).database(&database);
    let pool = pool_options.connect_with(connect_options).await.context("Failed to connect to the PostgreSQL database")?;
    Ok(pool)
}

// ── Config types ─────────────────────────────────────────────────

#[derive(Deserialize, Clone, Debug)]
pub struct FeedConfig {
    pub name: String,
    pub url: String,
    pub category: String,
}

#[derive(Clone, Debug, FromRow)]
pub struct TrackedFeed {
    pub url: String,
    pub name: String,
    pub category: String,
    pub is_default: bool,
    pub is_enabled: bool,
    pub consecutive_failures: i32,
}

// ── Parsed article ready for DB insertion ────────────────────────

pub struct ParsedArticle {
    pub feed_url: String,
    pub guid: String,
    pub title: String,
    pub link: String,
    pub description: String,
    pub source_name: String,
    pub published_at: Option<DateTime<Utc>>,
}

// ── Table creation ───────────────────────────────────────────────

pub async fn create_tables(pool: &Arc<PgPool>) -> Result<()> {
    let tracked_feeds_statement = "
        CREATE TABLE IF NOT EXISTS tracked_feeds (
            url             TEXT PRIMARY KEY,
            name            TEXT NOT NULL,
            category        TEXT NOT NULL DEFAULT 'General',
            is_default      BOOLEAN NOT NULL DEFAULT false,
            is_enabled      BOOLEAN NOT NULL DEFAULT true,
            created_at      TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP
        );
    ";

    let rss_items_statement = "
        CREATE TABLE IF NOT EXISTS rss_items (
            id              SERIAL PRIMARY KEY,
            feed_url        TEXT NOT NULL REFERENCES tracked_feeds(url) ON DELETE CASCADE,
            guid            TEXT NOT NULL,
            title           TEXT NOT NULL,
            link            TEXT NOT NULL DEFAULT '',
            description     TEXT NOT NULL DEFAULT '',
            source_name     TEXT NOT NULL DEFAULT '',
            published_at    TIMESTAMPTZ,
            created_at      TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
            updated_at      TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
            UNIQUE(feed_url, guid)
        );
    ";

    let mut connection = pool.acquire().await?;
    query(tracked_feeds_statement).execute(&mut *connection).await?;
    query(rss_items_statement).execute(&mut *connection).await?;

    // Idempotent schema migrations
    let migrations = [
        "ALTER TABLE tracked_feeds ADD COLUMN IF NOT EXISTS consecutive_failures INT NOT NULL DEFAULT 0",
        "ALTER TABLE tracked_feeds ADD COLUMN IF NOT EXISTS last_error TEXT",
        "ALTER TABLE tracked_feeds ADD COLUMN IF NOT EXISTS last_error_at TIMESTAMPTZ",
        "ALTER TABLE tracked_feeds ADD COLUMN IF NOT EXISTS last_success_at TIMESTAMPTZ",
        // Track who added custom feeds for authorization on delete
        "ALTER TABLE tracked_feeds ADD COLUMN IF NOT EXISTS added_by TEXT",
    ];
    for migration in &migrations {
        query(migration).execute(&mut *connection).await?;
    }

    Ok(())
}

// ── Seed default feeds from config file (batched) ───────────────

pub async fn seed_tracked_feeds(pool: Arc<PgPool>, feeds: Vec<FeedConfig>) -> Result<()> {
    if feeds.is_empty() {
        return Ok(());
    }

    let urls: Vec<&str> = feeds.iter().map(|f| f.url.as_str()).collect();
    let names: Vec<&str> = feeds.iter().map(|f| f.name.as_str()).collect();
    let categories: Vec<&str> = feeds.iter().map(|f| f.category.as_str()).collect();

    let statement = "
        INSERT INTO tracked_feeds (url, name, category, is_default, is_enabled)
        SELECT * FROM UNNEST($1::text[], $2::text[], $3::text[])
            AS t(url, name, category),
            LATERAL (SELECT true AS is_default, true AS is_enabled) defaults
        ON CONFLICT (url) DO UPDATE SET category = EXCLUDED.category, name = EXCLUDED.name
    ";
    let mut connection = pool.acquire().await?;
    query(statement)
        .bind(&urls)
        .bind(&names)
        .bind(&categories)
        .execute(&mut *connection)
        .await
        .context("Failed to batch seed tracked feeds")?;
    Ok(())
}

// ── Get all enabled, non-quarantined feeds ──────────────────────

pub async fn get_tracked_feeds(pool: Arc<PgPool>) -> Vec<TrackedFeed> {
    let statement = "
        SELECT url, name, category, is_default, is_enabled, consecutive_failures
        FROM tracked_feeds
        WHERE is_enabled = TRUE AND consecutive_failures < 288
    ";
    let res: Result<Vec<TrackedFeed>, sqlx::Error> = async {
        let mut connection = pool.acquire().await?;
        let data = query_as(statement).fetch_all(&mut *connection).await?;
        Ok(data)
    }.await;

    match res {
        Ok(data) => data,
        Err(e) => {
            log::error!("Failed to get tracked feeds: {}", e);
            Vec::new()
        }
    }
}

// ── Get quarantined feeds (for periodic retry) ──────────────────

pub async fn get_quarantined_feeds(pool: Arc<PgPool>) -> Vec<TrackedFeed> {
    let statement = "
        SELECT url, name, category, is_default, is_enabled, consecutive_failures
        FROM tracked_feeds
        WHERE is_enabled = TRUE AND consecutive_failures >= 288
    ";
    let res: Result<Vec<TrackedFeed>, sqlx::Error> = async {
        let mut connection = pool.acquire().await?;
        let data = query_as(statement).fetch_all(&mut *connection).await?;
        Ok(data)
    }.await;

    match res {
        Ok(data) => data,
        Err(e) => {
            log::error!("Failed to get quarantined feeds: {}", e);
            Vec::new()
        }
    }
}

// ── Record feed poll success ────────────────────────────────────

pub async fn record_feed_success(pool: &Arc<PgPool>, feed_url: &str) {
    let statement = "
        UPDATE tracked_feeds
        SET consecutive_failures = 0, last_success_at = NOW()
        WHERE url = $1
    ";
    let res: Result<(), sqlx::Error> = async {
        let mut connection = pool.acquire().await?;
        query(statement).bind(feed_url).execute(&mut *connection).await?;
        Ok(())
    }.await;

    if let Err(e) = res {
        log::error!("Failed to record feed success for {}: {}", feed_url, e);
    }
}

// ── Record feed poll failure ────────────────────────────────────

pub async fn record_feed_failure(pool: &Arc<PgPool>, feed_url: &str, error: &str) {
    let statement = "
        UPDATE tracked_feeds
        SET consecutive_failures = consecutive_failures + 1,
            last_error = $2,
            last_error_at = NOW()
        WHERE url = $1
    ";
    let res: Result<(), sqlx::Error> = async {
        let mut connection = pool.acquire().await?;
        query(statement).bind(feed_url).bind(error).execute(&mut *connection).await?;
        Ok(())
    }.await;

    if let Err(e) = res {
        log::error!("Failed to record feed failure for {}: {}", feed_url, e);
    }
}

// ── Batch upsert RSS items ──────────────────────────────────────

pub async fn batch_upsert_rss_items(pool: &Arc<PgPool>, articles: Vec<ParsedArticle>) -> Result<()> {
    if articles.is_empty() {
        return Ok(());
    }

    let feed_urls: Vec<&str> = articles.iter().map(|a| a.feed_url.as_str()).collect();
    let guids: Vec<&str> = articles.iter().map(|a| a.guid.as_str()).collect();
    let titles: Vec<&str> = articles.iter().map(|a| a.title.as_str()).collect();
    let links: Vec<&str> = articles.iter().map(|a| a.link.as_str()).collect();
    let descriptions: Vec<&str> = articles.iter().map(|a| a.description.as_str()).collect();
    let source_names: Vec<&str> = articles.iter().map(|a| a.source_name.as_str()).collect();
    let published_ats: Vec<Option<DateTime<Utc>>> = articles.iter().map(|a| a.published_at).collect();

    // Only touch the row when content actually changed — unchanged articles
    // are skipped so Sequin CDC won't fire redundant UPDATE events on repoll.
    let statement = "
        INSERT INTO rss_items (feed_url, guid, title, link, description, source_name, published_at)
        SELECT * FROM UNNEST(
            $1::text[], $2::text[], $3::text[], $4::text[],
            $5::text[], $6::text[], $7::timestamptz[]
        ) AS t(feed_url, guid, title, link, description, source_name, published_at)
        ON CONFLICT (feed_url, guid)
        DO UPDATE SET
            title = EXCLUDED.title,
            link = EXCLUDED.link,
            description = EXCLUDED.description,
            source_name = EXCLUDED.source_name,
            published_at = EXCLUDED.published_at,
            updated_at = CURRENT_TIMESTAMP
        WHERE
            rss_items.title        IS DISTINCT FROM EXCLUDED.title
            OR rss_items.link         IS DISTINCT FROM EXCLUDED.link
            OR rss_items.description  IS DISTINCT FROM EXCLUDED.description
            OR rss_items.source_name  IS DISTINCT FROM EXCLUDED.source_name
            OR rss_items.published_at IS DISTINCT FROM EXCLUDED.published_at
    ";
    let mut connection = pool.acquire().await?;
    query(statement)
        .bind(&feed_urls)
        .bind(&guids)
        .bind(&titles)
        .bind(&links)
        .bind(&descriptions)
        .bind(&source_names)
        .bind(&published_ats)
        .execute(&mut *connection)
        .await
        .context("Failed to batch upsert RSS items")?;
    Ok(())
}

// ── Cleanup old articles ─────────────────────────────────────────

pub async fn cleanup_old_articles(pool: &Arc<PgPool>) -> Result<u64> {
    let statement = "DELETE FROM rss_items WHERE published_at < now() - interval '7 days'";
    let mut connection = pool.acquire().await?;
    let result = query(statement).execute(&mut *connection).await?;
    Ok(result.rows_affected())
}

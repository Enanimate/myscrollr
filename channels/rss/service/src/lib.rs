use std::{sync::Arc, fs};
use reqwest::Client;
use tokio::sync::Mutex;
use crate::log::{error, info, warn};
use crate::database::{
    PgPool, get_tracked_feeds, get_quarantined_feeds, seed_tracked_feeds,
    batch_upsert_rss_items, cleanup_old_articles,
    batch_record_feed_successes, batch_record_feed_failures,
    FeedConfig, TrackedFeed, ParsedArticle,
};
pub use crate::types::RssHealth;

pub mod log;
pub mod database;
pub mod types;

pub async fn start_rss_service(pool: Arc<PgPool>, health_state: Arc<Mutex<RssHealth>>, client: &Client, cycle: u64) {
    info!("Starting RSS service (cycle {})...", cycle);

    // Seed default feeds from config on first cycle (ON CONFLICT updates
    // category and name so renames propagate; user customizations are unaffected)
    if cycle == 0 {
        match fs::read_to_string("./configs/feeds.json") {
            Ok(file_contents) => match serde_json::from_str::<Vec<FeedConfig>>(&file_contents) {
                Ok(config) => {
                    info!("Upserting {} default feeds from configs/feeds.json...", config.len());
                    if let Err(e) = seed_tracked_feeds(pool.clone(), config).await {
                        error!("Failed to seed default feeds: {}", e);
                    }
                }
                Err(e) => error!("Failed to parse configs/feeds.json: {}", e),
            },
            Err(e) => warn!("configs/feeds.json not found: {}", e),
        }
    }

    let mut feeds = get_tracked_feeds(pool.clone()).await;

    // Every 288 cycles (~24 hours), retry quarantined feeds to see if they've recovered
    if cycle % 288 == 0 && cycle > 0 {
        let quarantined = get_quarantined_feeds(pool.clone()).await;
        if !quarantined.is_empty() {
            info!("Retrying {} quarantined feeds...", quarantined.len());
            feeds.extend(quarantined);
        }
    }

    if feeds.is_empty() {
        error!("No feeds to track. RSS service idling.");
        return;
    }

    // Reset per-cycle counters
    health_state.lock().await.reset_cycle();

    info!("Polling {} RSS feeds concurrently...", feeds.len());

    // Limit concurrency to avoid overwhelming the network/DB connection pool
    let semaphore = Arc::new(tokio::sync::Semaphore::new(20));
    let mut join_set = tokio::task::JoinSet::new();

    for feed in feeds {
        let client = client.clone();
        let pool = pool.clone();
        let sem = semaphore.clone();
        let feed_name = feed.name.clone();
        let feed_url = feed.url.clone();

        join_set.spawn(async move {
            let _permit = sem.acquire().await.expect("semaphore closed");
            let result = poll_feed(&client, &pool, &feed).await;
            (feed_name, feed_url, feed.consecutive_failures, result)
        });
    }

    // Collect results, then batch-update the DB in two queries instead of 97
    let mut success_urls: Vec<String> = Vec::new();
    let mut failure_urls: Vec<String> = Vec::new();
    let mut failure_errors: Vec<String> = Vec::new();

    while let Some(join_result) = join_set.join_next().await {
        match join_result {
            Ok((feed_name, feed_url, prev_failures, Ok(count))) => {
                success_urls.push(feed_url.clone());
                if prev_failures >= 3 {
                    info!("Feed {} ({}) recovered after {} consecutive failures", feed_name, feed_url, prev_failures);
                }
                health_state.lock().await.record_success(count as u64);
            }
            Ok((feed_name, feed_url, prev_failures, Err(e))) => {
                let err_msg = format!("{}", e);
                failure_urls.push(feed_url.clone());
                failure_errors.push(err_msg);
                let new_failures = prev_failures + 1;
                if new_failures == 3 {
                    warn!("Feed {} ({}) hidden from catalog after 3 consecutive failures", feed_name, feed_url);
                } else if new_failures == 288 {
                    warn!("Feed {} ({}) quarantined after 288 consecutive failures (24h)", feed_name, feed_url);
                }
                error!("Error polling feed {} ({}): {}", feed_name, feed_url, e);
                health_state.lock().await.record_error(format!("{}: {}", feed_name, e));
            }
            Err(panic_err) => {
                error!("PANIC in feed poll task: {}", panic_err);
                health_state.lock().await.record_error(format!("PANIC: {}", panic_err));
            }
        }
    }

    // Batch-update feed statuses (2 queries instead of ~97 sequential ones)
    batch_record_feed_successes(&pool, &success_urls).await;
    batch_record_feed_failures(&pool, &failure_urls, &failure_errors).await;

    // Cleanup old articles (older than 7 days)
    match cleanup_old_articles(&pool).await {
        Ok(deleted) if deleted > 0 => {
            info!("Cleaned up {} old RSS articles", deleted);
        }
        Ok(_) => {} // Nothing to clean
        Err(e) => {
            warn!("Failed to cleanup old articles: {}", e);
        }
    }

    let health = health_state.lock().await;
    info!(
        "RSS poll cycle complete: {} feeds polled, {} items ingested, {} errors",
        health.feeds_polled, health.items_ingested, health.error_count
    );
}

async fn poll_feed(client: &Client, pool: &Arc<PgPool>, feed: &TrackedFeed) -> anyhow::Result<usize> {
    let response = client.get(&feed.url).send().await?;
    let bytes = response.bytes().await?;

    let parsed = feed_rs::parser::parse(&bytes[..])?;

    // Hoist source_name derivation outside the entry loop (avoids cloning per entry)
    let source_name = parsed.title
        .as_ref()
        .map(|t| t.content.clone())
        .unwrap_or_else(|| feed.name.clone());

    let cutoff = chrono::Utc::now() - chrono::Duration::days(7);
    let mut articles = Vec::with_capacity(parsed.entries.len());

    for entry in parsed.entries {
        let guid = entry.id.clone();
        if guid.is_empty() {
            continue;
        }

        let title = entry.title
            .map(|t| t.content)
            .unwrap_or_default();

        if title.is_empty() {
            continue;
        }

        let link = entry.links
            .first()
            .map(|l| l.href.clone())
            .unwrap_or_default();

        let description = entry.summary
            .map(|s| s.content)
            .or_else(|| entry.content.and_then(|c| c.body))
            .unwrap_or_default();

        // Truncate description to 500 characters (char-based to avoid
        // panicking on multi-byte UTF-8 sequences like smart quotes)
        let description = if description.chars().count() > 500 {
            let mut truncated: String = description.chars().take(500).collect();
            truncated.push_str("...");
            truncated
        } else {
            description
        };

        let description = strip_html_tags(&description);

        let published_at = entry.published
            .or(entry.updated)
            .map(|dt| dt.with_timezone(&chrono::Utc));

        // Skip articles older than the cleanup threshold (7 days) so we never
        // re-insert rows that cleanup already deleted — avoids a CDC
        // INSERT→DELETE storm every poll cycle.
        if let Some(pub_date) = &published_at {
            if *pub_date < cutoff {
                continue;
            }
        }

        articles.push(ParsedArticle {
            feed_url: feed.url.clone(),
            guid,
            title,
            link,
            description,
            source_name: source_name.clone(),
            published_at,
        });
    }

    if articles.is_empty() {
        return Ok(0);
    }

    let count = articles.len();
    if let Err(e) = batch_upsert_rss_items(pool, articles).await {
        warn!("Failed to batch upsert RSS items from {}: {}", feed.name, e);
        return Ok(0);
    }

    Ok(count)
}

/// Basic HTML tag stripper — removes angle-bracketed tags.
fn strip_html_tags(input: &str) -> String {
    let mut result = String::with_capacity(input.len());
    let mut in_tag = false;

    for ch in input.chars() {
        match ch {
            '<' => in_tag = true,
            '>' => in_tag = false,
            _ if !in_tag => result.push(ch),
            _ => {}
        }
    }

    // Collapse multiple whitespace and trim
    let collapsed: String = result.split_whitespace().collect::<Vec<&str>>().join(" ");
    collapsed
}

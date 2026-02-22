use std::{collections::HashMap, env, sync::Arc, time::{Duration, Instant}, pin::Pin};

use reqwest::Client;
use serde::{Deserialize, Serialize};
use tokio::time::Sleep;
use crate::database::PgPool;

/// A symbol entry from configs/subscriptions.json (categorized format).
#[derive(Debug, Deserialize, Clone)]
pub struct TrackedSymbolConfig {
    pub symbol: String,
    pub name: String,
    pub category: String,
}

/// TwelveData WebSocket price event.
///
/// ```json
/// {"event":"price","symbol":"AAPL","price":150.75,"timestamp":1678886400,"day_volume":5000000}
/// ```
#[derive(Debug, Deserialize, Clone)]
#[allow(dead_code)]
pub(crate) struct PriceEvent {
    pub event: String,
    pub symbol: Option<String>,
    pub price: Option<f64>,
    pub timestamp: Option<u64>,
    pub day_volume: Option<u64>,
    /// Present on status/error events
    pub status: Option<String>,
    pub message: Option<String>,
}

/// Simplified trade data extracted from a PriceEvent for the update queue.
#[derive(Debug, Clone)]
pub(crate) struct TradeData {
    pub symbol: String,
    pub price: f64,
    pub timestamp: u64,
}

#[derive(Debug, Default)]
pub(crate) struct BatchStats {
    pub batches_processed: u64,
    pub total_updates_processed: u64,
    pub errors: u64,
}

/// TwelveData REST quote response.
///
/// ```json
/// {"symbol":"AAPL","close":"150.75","previous_close":"149.50","change":"1.25","percent_change":"0.84",...}
/// ```
#[derive(Debug, Deserialize)]
pub(crate) struct QuoteResponse {
    pub close: Option<String>,
    pub previous_close: Option<String>,
    pub change: Option<String>,
    pub percent_change: Option<String>,
}

impl QuoteResponse {
    pub fn close_f64(&self) -> f64 {
        self.close.as_deref().and_then(|s| s.parse().ok()).unwrap_or(0.0)
    }
    pub fn previous_close_f64(&self) -> f64 {
        self.previous_close.as_deref().and_then(|s| s.parse().ok()).unwrap_or(0.0)
    }
    pub fn change_f64(&self) -> f64 {
        self.change.as_deref().and_then(|s| s.parse().ok()).unwrap_or(0.0)
    }
    pub fn percent_change_f64(&self) -> f64 {
        self.percent_change.as_deref().and_then(|s| s.parse().ok()).unwrap_or(0.0)
    }
}

pub(crate) struct WebSocketState {
    pub update_queue: HashMap<String, TradeData>,
    pub batch_timer: Option<Pin<Box<Sleep>>>,
    pub is_processing_batch: bool,
    pub stats: BatchStats,
    pub last_log_time: Option<Instant>,
    pub last_error_message: Option<String>,
}

impl WebSocketState {
    pub fn new() -> Self {
        Self {
            update_queue: HashMap::new(),
            batch_timer: None,
            is_processing_batch: false,
            stats: BatchStats::default(),
            last_log_time: None,
            last_error_message: None,
        }
    }
}

#[derive(Clone)]
pub struct FinanceState {
    pub api_key: String,
    pub subscriptions: Vec<String>,
    pub client: Arc<Client>,
    pub pool: Arc<PgPool>,
}

impl FinanceState {
    pub async fn new(pool: Arc<PgPool>) -> Self {
        let api_key = env::var("TWELVEDATA_API_KEY")
            .expect("TWELVEDATA_API_KEY must be set in the environment");

        // TwelveData uses apikey as a query parameter, no custom headers needed.
        let client = Client::builder()
            .timeout(Duration::from_millis(10_000))
            .build()
            .expect("Failed creating finance Reqwest Client");

        // Load symbols from database instead of file
        let subscriptions = crate::database::get_tracked_symbols(pool.clone()).await;

        Self {
            api_key,
            subscriptions,
            client: Arc::new(client),
            pool,
        }
    }
}

#[derive(Serialize)]
pub struct FinanceHealth {
    pub status: String,
    pub connection_status: String,
    pub batch_number: u64,
    pub error_count: u64,
    pub last_error: Option<String>,
}

impl FinanceHealth {
    pub fn new() -> Self {
        Self {
            status: String::from("healthy"),
            connection_status: String::from("disconnected"),
            batch_number: 0,
            error_count: 0,
            last_error: None,
        }
    }

    pub(crate) fn update_health(&mut self, connection_status: String, batch_number: u64, error_count: u64, last_error: Option<String>) {
        self.connection_status = connection_status;
        self.batch_number = batch_number;
        self.error_count = error_count;
        self.last_error = last_error;
    }

    pub fn get_health(&self) -> Self {
        Self {
            status: self.status.clone(),
            connection_status: self.connection_status.clone(),
            batch_number: self.batch_number,
            error_count: self.error_count,
            last_error: self.last_error.clone(),
        }
    }
}

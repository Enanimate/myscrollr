use chrono::{DateTime, Utc};
use serde::Serialize;
use std::sync::atomic::{AtomicU32, Ordering};

#[derive(Serialize, Clone)]
pub struct SportsHealth {
    pub status: String,
    pub last_poll: Option<DateTime<Utc>>,
    pub leagues_active: u32,
    pub leagues_live: u32,
    pub rate_limit_remaining: Option<u32>,
    pub error_count: u64,
    pub last_error: Option<String>,
}

impl SportsHealth {
    pub fn new() -> Self {
        Self {
            status: String::from("starting"),
            last_poll: None,
            leagues_active: 0,
            leagues_live: 0,
            rate_limit_remaining: None,
            error_count: 0,
            last_error: None,
        }
    }

    pub fn record_success(&mut self, leagues_active: u32, leagues_live: u32) {
        self.last_poll = Some(Utc::now());
        self.status = String::from("healthy");
        self.leagues_active = leagues_active;
        self.leagues_live = leagues_live;
    }

    pub fn record_error(&mut self, error: String) {
        self.error_count += 1;
        self.last_error = Some(error);
        self.status = String::from("degraded");
    }

    pub fn set_rate_limit(&mut self, remaining: u32) {
        self.rate_limit_remaining = Some(remaining);
    }

    pub fn get_health(&self) -> Self {
        self.clone()
    }
}

/// Shared atomic counter for tracking remaining API requests across tasks.
/// Updated from response headers after each api-sports.io call.
pub struct RateLimitTracker {
    remaining: AtomicU32,
}

impl RateLimitTracker {
    pub fn new(initial: u32) -> Self {
        Self {
            remaining: AtomicU32::new(initial),
        }
    }

    pub fn update(&self, remaining: u32) {
        self.remaining.store(remaining, Ordering::Relaxed);
    }

    pub fn remaining(&self) -> u32 {
        self.remaining.load(Ordering::Relaxed)
    }

    /// Returns true if we have enough budget to make a request.
    /// Reserves a conservative buffer of 100 requests.
    pub fn has_budget(&self) -> bool {
        self.remaining() > 100
    }
}

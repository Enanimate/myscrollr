/**
 * Centralized configuration for the desktop app.
 *
 * All API endpoints, auth settings, and app-wide constants live here.
 * Desktop builds don't use Vite env vars (no VITE_ prefix), so these
 * are compile-time constants. Swap values via this single file when
 * targeting a different environment.
 */

// ── API ─────────────────────────────────────────────────────────

export const API_BASE = "https://api.myscrollr.relentnet.dev";
export const API_HOST = "api.myscrollr.relentnet.dev";

// ── Auth (Logto PKCE) ───────────────────────────────────────────

export const AUTH_ENDPOINT = "https://auth.myscrollr.relentnet.dev";
export const LOGTO_APP_ID = "kq298uwwusrvw8m6yn6b4";
export const REDIRECT_URI = "http://127.0.0.1:19284/callback";
export const REFRESH_BUFFER_MS = 60_000;

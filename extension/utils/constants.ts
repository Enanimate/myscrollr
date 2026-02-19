// FORK: Original production values (revert to these when switching back):
//   API_URL = 'https://api.myscrollr.relentnet.dev'
//   FRONTEND_URL = 'https://myscrollr.relentnet.dev'
//   LOGTO_ENDPOINT = 'https://auth.myscrollr.relentnet.dev'
//   LOGTO_APP_ID = 'kq298uwwusrvw8m6yn6b4'
export const API_URL = 'https://api.myscrollr.enanimate.dev';
export const SSE_URL = `${API_URL}/events`;
export const FRONTEND_URL = 'https://myscrollr.enanimate.dev';

export const LOGTO_ENDPOINT = 'https://auth.myscrollr.enanimate.dev';
export const LOGTO_APP_ID = 'roazl2zldtcfscslysiuf';

/** Maximum number of items kept in memory per category. */
export const MAX_ITEMS = 50;

/** SSE reconnect: initial delay in ms. */
export const SSE_RECONNECT_BASE = 1000;

/** SSE reconnect: maximum delay in ms. */
export const SSE_RECONNECT_MAX = 30_000;

/** Polling interval in minutes for chrome.alarms (0.5 = 30 seconds). */
export const POLLING_ALARM_INTERVAL = 0.5;

/** Alarm name for the polling timer. */
export const POLLING_ALARM_NAME = 'scrollr-poll';

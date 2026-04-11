package main

import (
	"encoding/json"
	"log"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
)

type state struct {
	mu       sync.RWMutex
	scenario string

	// Per-sport request counters
	sportDailyCount  map[string]int // requests today, per sport
	sportMinuteCount map[string]int // requests this minute, per sport

	// Limits (defaults: 7500/day, 300/min)
	defaultDaily  int
	defaultMinute int

	// Per-sport limit overrides
	sportLimitDaily  map[string]int
	sportLimitMinute map[string]int

	// Reset tracking
	lastDailyReset  time.Time
	lastMinuteReset time.Time
}

func (s *state) setScenario(name string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.scenario = name
}

func (s *state) getScenario() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.scenario
}

func (s *state) getDailyLimit(sport string) int {
	if limit, ok := s.sportLimitDaily[sport]; ok {
		return limit
	}
	return s.defaultDaily
}

func (s *state) getMinuteLimit(sport string) int {
	if limit, ok := s.sportLimitMinute[sport]; ok {
		return limit
	}
	return s.defaultMinute
}

func (s *state) incrementAndCheck(sport string) (bool, int, int) {
	s.mu.Lock()
	defer s.mu.Unlock()

	now := time.Now()

	// Reset minute counters if 60+ seconds elapsed
	if now.Sub(s.lastMinuteReset) >= time.Minute {
		s.sportMinuteCount = make(map[string]int)
		s.lastMinuteReset = now
	}

	// Reset daily counters if past midnight UTC
	if now.Sub(s.lastDailyReset) >= 24*time.Hour {
		s.sportDailyCount = make(map[string]int)
		s.lastDailyReset = now
	}

	// Initialize sport if not exists
	if _, ok := s.sportDailyCount[sport]; !ok {
		s.sportDailyCount[sport] = 0
	}
	if _, ok := s.sportMinuteCount[sport]; !ok {
		s.sportMinuteCount[sport] = 0
	}

	// Increment counts
	s.sportDailyCount[sport]++
	s.sportMinuteCount[sport]++

	dailyCount := s.sportDailyCount[sport]
	minuteCount := s.sportMinuteCount[sport]

	dailyLimit := s.getDailyLimit(sport)
	minuteLimit := s.getMinuteLimit(sport)

	// Check limits
	if dailyCount > dailyLimit {
		return false, dailyCount, minuteCount
	}
	if minuteCount > minuteLimit {
		return false, dailyCount, minuteCount
	}

	return true, dailyCount, minuteCount
}

func (s *state) getStatus() map[string]any {
	s.mu.RLock()
	defer s.mu.RUnlock()

	status := make(map[string]any)
	for sport, daily := range s.sportDailyCount {
		status[sport] = map[string]int{
			"daily_count":  daily,
			"daily_limit":  s.getDailyLimit(sport),
			"minute_count": s.sportMinuteCount[sport],
			"minute_limit": s.getMinuteLimit(sport),
		}
	}
	return status
}

func (s *state) resetCounters(sport string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if sport == "" {
		// Reset all
		s.sportDailyCount = make(map[string]int)
		s.sportMinuteCount = make(map[string]int)
		s.lastDailyReset = time.Now()
		s.lastMinuteReset = time.Now()
	} else {
		// Reset specific sport
		delete(s.sportDailyCount, sport)
		delete(s.sportMinuteCount, sport)
	}
}

var globalState = &state{
	scenario:         "normal",
	sportDailyCount:  make(map[string]int),
	sportMinuteCount: make(map[string]int),
	sportLimitDaily:  make(map[string]int),
	sportLimitMinute: make(map[string]int),
	defaultDaily:     7500,
	defaultMinute:    300,
	lastDailyReset:   time.Now(),
	lastMinuteReset:  time.Now(),
}

func main() {
	// Parse environment variables for rate limits
	if v := os.Getenv("DEFAULT_DAILY_LIMIT"); v != "" {
		if limit, err := strconv.Atoi(v); err == nil {
			globalState.defaultDaily = limit
		}
	}
	if v := os.Getenv("DEFAULT_MINUTE_LIMIT"); v != "" {
		if limit, err := strconv.Atoi(v); err == nil {
			globalState.defaultMinute = limit
		}
	}

	// Parse per-sport overrides (e.g., SPORT_LIMIT_BASKETBALL_DAILY=500)
	for _, env := range os.Environ() {
		if strings.HasPrefix(env, "SPORT_LIMIT_") {
			parts := strings.Split(env, "=")
			if len(parts) != 2 {
				continue
			}
			key := parts[0]
			value := parts[1]
			limit, err := strconv.Atoi(value)
			if err != nil {
				continue
			}

			// Parse sport and limit type from key
			// Format: SPORT_LIMIT_<SPORT>_<TYPE> (e.g., SPORT_LIMIT_BASKETBALL_DAILY)
			prefix := "SPORT_LIMIT_"
			rest := strings.TrimPrefix(key, prefix)
			parts = strings.Split(rest, "_")
			if len(parts) >= 2 {
				sport := strings.ToLower(parts[0])
				limitType := strings.ToUpper(parts[len(parts)-1])
				if limitType == "DAILY" {
					globalState.sportLimitDaily[sport] = limit
				} else if limitType == "MINUTE" {
					globalState.sportLimitMinute[sport] = limit
				}
			}
		}
	}

	log.Printf("[mock-apisports] Default limits: daily=%d, minute=%d", globalState.defaultDaily, globalState.defaultMinute)
	log.Printf("[mock-apisports] Per-sport daily limits: %v", globalState.sportLimitDaily)
	log.Printf("[mock-apisports] Per-sport minute limits: %v", globalState.sportLimitMinute)

	port := os.Getenv("PORT")
	if port == "" {
		port = "9004"
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/", handleAPI)
	mux.HandleFunc("/health", healthHandler)
	mux.HandleFunc("/control/scenario", controlScenarioHandler)
	mux.HandleFunc("/control/rate-limit", controlRateLimitHandler)
	mux.HandleFunc("/control/reset", controlResetHandler)
	mux.HandleFunc("/control/status", controlStatusHandler)

	log.Printf("[mock-apisports] Listening on :%s", port)
	log.Fatalf("[mock-apisports] Error: %v", http.ListenAndServe(":"+port, mux))
}

func healthHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{"status": "ok", "service": "mock-apisports"})
}

func controlScenarioHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST required", http.StatusMethodNotAllowed)
		return
	}
	var req struct {
		Scenario string `json:"scenario"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	globalState.setScenario(req.Scenario)
	log.Printf("[mock-apisports] Scenario set to: %s", req.Scenario)
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{"scenario": req.Scenario, "status": "ok"})
}

func controlRateLimitHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST required", http.StatusMethodNotAllowed)
		return
	}
	var req struct {
		Daily       int    `json:"daily"`
		Minute      int    `json:"minute"`
		Sport       string `json:"sport"`
		SportDaily  int    `json:"sport_daily"`
		SportMinute int    `json:"sport_minute"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	if req.Daily > 0 {
		globalState.defaultDaily = req.Daily
	}
	if req.Minute > 0 {
		globalState.defaultMinute = req.Minute
	}
	if req.Sport != "" && req.SportDaily > 0 {
		globalState.sportLimitDaily[req.Sport] = req.SportDaily
	}
	if req.Sport != "" && req.SportMinute > 0 {
		globalState.sportLimitMinute[req.Sport] = req.SportMinute
	}

	log.Printf("[mock-apisports] Rate limits updated: daily=%d, minute=%d", globalState.defaultDaily, globalState.defaultMinute)
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		"daily_limit":  globalState.defaultDaily,
		"minute_limit": globalState.defaultMinute,
		"sport_limits": map[string]any{
			"daily":  globalState.sportLimitDaily,
			"minute": globalState.sportLimitMinute,
		},
		"status": "ok",
	})
}

func controlResetHandler(w http.ResponseWriter, r *http.Request) {
	sport := r.URL.Query().Get("sport")
	globalState.resetCounters(sport)
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{"status": "ok", "message": "counters reset", "sport": sport})
}

func controlStatusHandler(w http.ResponseWriter, r *http.Request) {
	status := globalState.getStatus()
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		"status": status,
		"defaults": map[string]int{
			"daily":  globalState.defaultDaily,
			"minute": globalState.defaultMinute,
		},
	})
}

// Determine sport from Host header.
// api-sports.io hosts look like: v1.basketball.api-sports.io
func sportFromHost(host string) string {
	host = strings.Split(host, ":")[0] // strip port
	parts := strings.Split(host, ".")
	for _, p := range parts {
		if p != "v1" && p != "v2" && p != "v3" && p != "api-sports" && p != "io" {
			return p
		}
	}
	return "unknown"
}

// Determine endpoint from URL path
func endpointFromPath(path string) string {
	parts := strings.Split(path, "?")
	return strings.TrimPrefix(parts[0], "/")
}

func handleAPI(w http.ResponseWriter, r *http.Request) {
	sport := r.URL.Query().Get("sport")
	if sport == "" {
		sport = sportFromHost(r.Host)
	}

	ok, dailyCount, minuteCount := globalState.incrementAndCheck(sport)
	if !ok {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("x-ratelimit-requests-remaining", "0")
		w.WriteHeader(http.StatusTooManyRequests)
		json.NewEncoder(w).Encode(map[string]any{
			"get":    "error",
			"errors": map[string]string{"rate_limit": "rate limit exceeded"},
		})
		log.Printf("[mock-apisports] Rate limited sport=%s (daily=%d, minute=%d)", sport, dailyCount, minuteCount)
		return
	}

	scenario := globalState.getScenario()
	if scenario == "error" {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]any{
			"get": "error",
		})
		return
	}

	ep := endpointFromPath(r.URL.Path)
	resp := buildResponse(sport, ep, "", scenario)

	w.Header().Set("Content-Type", "application/json")
	// Set rate limit headers with remaining counts
	dailyLimit := globalState.getDailyLimit(sport)
	w.Header().Set("x-ratelimit-requests-remaining", strconv.Itoa(dailyLimit-dailyCount))
	w.Header().Set("x-ratelimit-requests-limit", strconv.Itoa(dailyLimit))
	w.Header().Set("x-ratelimit-requests-reset", "86400") // seconds until daily reset
	json.NewEncoder(w).Encode(resp)
}

func buildResponse(sport, endpoint, query string, scenario string) map[string]any {
	// If scenario is no-games, return empty results
	if scenario == "no-games" {
		return map[string]any{
			"get":      endpoint,
			"results":  0,
			"response": []any{},
		}
	}

	// Check if endpoint contains "standings" - route to standings handlers
	if strings.Contains(endpoint, "standings") {
		// Parse league from query string for handball, basketball, etc.
		league := ""
		if strings.Contains(endpoint, "?") {
			params := strings.Split(endpoint, "?")[1]
			for _, p := range strings.Split(params, "&") {
				if strings.HasPrefix(p, "league=") {
					league = strings.TrimPrefix(p, "league=")
					break
				}
			}
		}
		return standingsResponse(sport, endpoint, scenario, league)
	}

	// Return sport-specific canned data (games/fixtures)
	switch sport {
	case "football":
		return footballResponse(endpoint)
	case "basketball":
		return basketballResponse(endpoint)
	case "american-football":
		return americanFootballResponse(endpoint)
	case "hockey":
		return hockeyResponse(endpoint)
	case "baseball":
		return baseballResponse(endpoint)
	case "rugby":
		return rugbyResponse(endpoint)
	case "volleyball":
		return volleyballResponse(endpoint)
	case "handball":
		return handballResponse(endpoint)
	case "afl":
		return aflResponse(endpoint)
	case "mma":
		return mmaResponse(endpoint)
	case "formula-1":
		return f1Response(endpoint)
	default:
		return map[string]any{
			"get":      endpoint,
			"results":  0,
			"response": []any{},
		}
	}
}

func footballResponse(endpoint string) map[string]any {
	now := time.Now().Format(time.RFC3339)
	return map[string]any{
		"get":     endpoint,
		"results": 2,
		"paging":  map[string]int{"current": 1, "total": 1},
		"response": []any{
			map[string]any{
				"fixture": map[string]any{
					"id":        1,
					"timestamp": time.Now().Unix(),
					"date":      now,
					"status":    map[string]any{"short": "IN1", "long": "1st Half", "elapsed": 23},
					"venue":     map[string]any{"name": "Mock Stadium"},
				},
				"league": map[string]any{
					"id": 1, "name": "Mock Premier League",
				},
				"teams": map[string]any{
					"home": map[string]any{"id": 1, "name": "Home FC", "logo": "https://example.com/home.png"},
					"away": map[string]any{"id": 2, "name": "Away United", "logo": "https://example.com/away.png"},
				},
				"goals": map[string]any{
					"home": 1, "away": 1,
				},
			},
			map[string]any{
				"fixture": map[string]any{
					"id":        2,
					"timestamp": time.Now().Add(2 * time.Hour).Unix(),
					"date":      time.Now().Add(2 * time.Hour).Format(time.RFC3339),
					"status":    map[string]any{"short": "NS", "long": "Not Started"},
					"venue":     map[string]any{"name": "Mock Arena"},
				},
				"league": map[string]any{
					"id": 1, "name": "Mock Premier League",
				},
				"teams": map[string]any{
					"home": map[string]any{"id": 3, "name": "Team A", "logo": "https://example.com/teama.png"},
					"away": map[string]any{"id": 4, "name": "Team B", "logo": "https://example.com/teamb.png"},
				},
				"goals": map[string]any{
					"home": nil, "away": nil,
				},
			},
		},
	}
}

func basketballResponse(endpoint string) map[string]any {
	return map[string]any{
		"get":     endpoint,
		"results": 1,
		"response": []any{
			map[string]any{
				"id":        1001,
				"date":      time.Now().Format(time.RFC3339),
				"timestamp": time.Now().Unix(),
				"status":    map[string]any{"short": "Q3", "long": "3rd Quarter", "timer": "5:42"},
				"teams": map[string]any{
					"home": map[string]any{"id": 10, "name": "LA Mockers", "logo": "https://example.com/lam.png"},
					"away": map[string]any{"id": 11, "name": "NY Balls", "logo": "https://example.com/nyb.png"},
				},
				"scores": map[string]any{
					"home": map[string]any{"total": 78},
					"away": map[string]any{"total": 72},
				},
			},
		},
	}
}

func americanFootballResponse(endpoint string) map[string]any {
	return map[string]any{
		"get":     endpoint,
		"results": 1,
		"response": []any{
			map[string]any{
				"game": map[string]any{
					"id": 2001,
					"date": map[string]any{
						"timestamp": time.Now().Unix(),
						"date":      time.Now().Format("2006-01-02"),
						"start":     time.Now().Format("15:04:05"),
					},
					"status": map[string]any{"short": "Q4", "long": "4th Quarter", "timer": "8:15"},
					"venue":  map[string]any{"name": "Mock Bowl Stadium"},
				},
				"teams": map[string]any{
					"home": map[string]any{"id": 20, "name": "Chicago Tests", "logo": "https://example.com/chi.png"},
					"away": map[string]any{"id": 21, "name": "Dallas Mocks", "logo": "https://example.com/dal.png"},
				},
				"scores": map[string]any{
					"home": map[string]any{"total": 21},
					"away": map[string]any{"total": 17},
				},
			},
		},
	}
}

func hockeyResponse(endpoint string) map[string]any {
	return map[string]any{
		"get":     endpoint,
		"results": 1,
		"response": []any{
			map[string]any{
				"id":        3001,
				"date":      time.Now().Format(time.RFC3339),
				"timestamp": time.Now().Unix(),
				"status":    map[string]any{"short": "3rd", "long": "3rd Period", "timer": "12:30"},
				"teams": map[string]any{
					"home": map[string]any{"id": 30, "name": "Mocktown Pucks", "logo": "https://example.com/mtp.png"},
					"away": map[string]any{"id": 31, "name": "Ice Valley", "logo": "https://example.com/iv.png"},
				},
				"scores": map[string]any{
					"home": 2, "away": 2,
				},
			},
		},
	}
}

func baseballResponse(endpoint string) map[string]any {
	return map[string]any{
		"get":     endpoint,
		"results": 1,
		"response": []any{
			map[string]any{
				"id":        4001,
				"date":      time.Now().Format(time.RFC3339),
				"timestamp": time.Now().Unix(),
				"status":    map[string]any{"short": "Inn 5", "long": "Middle 5th"},
				"teams": map[string]any{
					"home": map[string]any{"id": 40, "name": "NY Bats", "logo": "https://example.com/nyb.png"},
					"away": map[string]any{"id": 41, "name": "LA Sluggers", "logo": "https://example.com/lal.png"},
				},
				"scores": map[string]any{
					"home": map[string]any{"total": 3},
					"away": map[string]any{"total": 2},
				},
			},
		},
	}
}

func rugbyResponse(endpoint string) map[string]any {
	return map[string]any{
		"get":     endpoint,
		"results": 1,
		"response": []any{
			map[string]any{
				"id":        5001,
				"date":      time.Now().Format(time.RFC3339),
				"timestamp": time.Now().Unix(),
				"status":    map[string]any{"short": "1H", "long": "First Half", "timer": "35:00"},
				"teams": map[string]any{
					"home": map[string]any{"id": 50, "name": "Wellington Mocks", "logo": "https://example.com/wlg.png"},
					"away": map[string]any{"id": 51, "name": "Auckland Tests", "logo": "https://example.com/akl.png"},
				},
				"scores": map[string]any{
					"home": 12, "away": 8,
				},
			},
		},
	}
}

func volleyballResponse(endpoint string) map[string]any {
	return map[string]any{
		"get":     endpoint,
		"results": 1,
		"response": []any{
			map[string]any{
				"id":        6001,
				"date":      time.Now().Format(time.RFC3339),
				"timestamp": time.Now().Unix(),
				"status":    map[string]any{"short": "S2", "long": "Set 2", "timer": ""},
				"teams": map[string]any{
					"home": map[string]any{"id": 60, "name": "Spike City", "logo": "https://example.com/spk.png"},
					"away": map[string]any{"id": 61, "name": "Block Town", "logo": "https://example.com/blk.png"},
				},
				"scores": map[string]any{
					"home": 1, "away": 1,
				},
			},
		},
	}
}

func handballResponse(endpoint string) map[string]any {
	return map[string]any{
		"get":     endpoint,
		"results": 1,
		"response": []any{
			map[string]any{
				"id":        7001,
				"date":      time.Now().Format(time.RFC3339),
				"timestamp": time.Now().Unix(),
				"status":    map[string]any{"short": "2nd", "long": "2nd Half", "timer": "45:00"},
				"teams": map[string]any{
					"home": map[string]any{"id": 70, "name": "Throw Club", "logo": "https://example.com/thc.png"},
					"away": map[string]any{"id": 71, "name": "Catch United", "logo": "https://example.com/cau.png"},
				},
				"scores": map[string]any{
					"home": 18, "away": 16,
				},
			},
		},
	}
}

func aflResponse(endpoint string) map[string]any {
	return map[string]any{
		"get":     endpoint,
		"results": 1,
		"response": []any{
			map[string]any{
				"game": map[string]any{
					"id":        8001,
					"timestamp": time.Now().Unix(),
					"date":      time.Now().Format(time.RFC3339),
					"status":    map[string]any{"short": "Q3", "long": "3rd Quarter"},
				},
				"teams": map[string]any{
					"home": map[string]any{"id": 80, "name": "Melbourne Mocks", "logo": "https://example.com/mel.png"},
					"away": map[string]any{"id": 81, "name": "Sydney Tests", "logo": "https://example.com/syd.png"},
				},
				"scores": map[string]any{
					"home": map[string]any{"score": 45, "goals": 8, "behinds": 5},
					"away": map[string]any{"score": 38, "goals": 6, "behinds": 6},
				},
				"venue": "Mock MCG",
			},
		},
	}
}

func mmaResponse(endpoint string) map[string]any {
	return map[string]any{
		"get":     endpoint,
		"results": 1,
		"response": []any{
			map[string]any{
				"id":        9001,
				"date":      time.Now().Format(time.RFC3339),
				"timestamp": time.Now().Unix(),
				"status":    map[string]any{"short": "F", "long": "Finished"},
				"fighters": map[string]any{
					"first":  map[string]any{"id": 90, "name": "Fighter Alpha", "logo": "https://example.com/fa.png"},
					"second": map[string]any{"id": 91, "name": "Fighter Beta", "logo": "https://example.com/fb.png"},
				},
				"category": "Lightweight",
				"slug":     "mock-ufc-1",
			},
		},
	}
}

func f1Response(endpoint string) map[string]any {
	return map[string]any{
		"get":     endpoint,
		"results": 1,
		"response": []any{
			map[string]any{
				"id":          10001,
				"date":        time.Now().Format("2006-01-02"),
				"type":        "Race",
				"status":      "Live",
				"competition": map[string]any{"id": 100, "name": "Mock Grand Prix"},
				"circuit":     map[string]any{"name": "Mock Circuit International"},
			},
		},
	}
}

// standingsResponse routes to sport-specific standings handlers
func standingsResponse(sport, endpoint, scenario, league string) map[string]any {
	if scenario == "no-standings" {
		return map[string]any{
			"get":      endpoint,
			"results":  0,
			"response": []any{},
		}
	}

	switch sport {
	case "afl":
		return aflStandingsResponse(endpoint)
	case "hockey":
		return hockeyStandingsResponse(endpoint)
	case "basketball":
		return basketballStandingsResponse(endpoint, league)
	case "baseball":
		return baseballStandingsResponse(endpoint, league)
	case "american-football":
		return americanFootballStandingsResponse(endpoint)
	case "football":
		return footballStandingsResponse(endpoint)
	case "handball":
		return handballStandingsResponse(endpoint, league)
	case "rugby":
		return rugbyStandingsResponse(endpoint, league)
	case "volleyball":
		return volleyballStandingsResponse(endpoint)
	default:
		return map[string]any{
			"get":      endpoint,
			"results":  0,
			"response": []any{},
		}
	}
}

// aflStandingsResponse returns AFL standings (2023 season)
func aflStandingsResponse(endpoint string) map[string]any {
	return map[string]any{
		"get":        "standings",
		"parameters": map[string]any{"season": "2023", "league": "1"},
		"errors":     []any{},
		"results":    18,
		"response": []any{
			map[string]any{
				"position": 1,
				"team":     map[string]any{"id": 4, "name": "Collingwood Magpies", "logo": "https://media-3.api-sports.io/afl/teams/4.png"},
				"pts":      72,
				"games":    map[string]any{"played": 23, "win": 18, "drawn": 0, "lost": 5},
				"points":   map[string]any{"for": 2142, "against": 1687},
				"last_5":   "WLWLL",
			},
			map[string]any{
				"position": 2,
				"team":     map[string]any{"id": 2, "name": "Brisbane Lions", "logo": "https://media-2.api-sports.io/afl/teams/2.png"},
				"pts":      68,
				"games":    map[string]any{"played": 23, "win": 17, "drawn": 0, "lost": 6},
				"points":   map[string]any{"for": 2180, "against": 1771},
				"last_5":   "WWWWL",
			},
			map[string]any{
				"position": 3,
				"team":     map[string]any{"id": 11, "name": "Port Adelaide Power", "logo": "https://media-3.api-sports.io/afl/teams/11.png"},
				"pts":      68,
				"games":    map[string]any{"played": 23, "win": 17, "drawn": 0, "lost": 6},
				"points":   map[string]any{"for": 2149, "against": 1906},
				"last_5":   "WWWLL",
			},
			map[string]any{
				"position": 4,
				"team":     map[string]any{"id": 9, "name": "Melbourne Demons", "logo": "https://media-1.api-sports.io/afl/teams/9.png"},
				"pts":      64,
				"games":    map[string]any{"played": 23, "win": 16, "drawn": 0, "lost": 7},
				"points":   map[string]any{"for": 2079, "against": 1660},
				"last_5":   "WWLWW",
			},
			map[string]any{
				"position": 5,
				"team":     map[string]any{"id": 3, "name": "Carlton Blues", "logo": "https://media-1.api-sports.io/afl/teams/3.png"},
				"pts":      54,
				"games":    map[string]any{"played": 22, "win": 13, "drawn": 1, "lost": 8},
				"points":   map[string]any{"for": 1849, "against": 1592},
				"last_5":   "WWWWW",
			},
			map[string]any{
				"position": 6,
				"team":     map[string]any{"id": 13, "name": "St Kilda Saints", "logo": "https://media-2.api-sports.io/afl/teams/13.png"},
				"pts":      52,
				"games":    map[string]any{"played": 23, "win": 13, "drawn": 0, "lost": 10},
				"points":   map[string]any{"for": 1775, "against": 1647},
				"last_5":   "LWWLW",
			},
			map[string]any{
				"position": 7,
				"team":     map[string]any{"id": 14, "name": "Sydney Swans", "logo": "https://media-3.api-sports.io/afl/teams/14.png"},
				"pts":      50,
				"games":    map[string]any{"played": 23, "win": 12, "drawn": 1, "lost": 10},
				"points":   map[string]any{"for": 2050, "against": 1863},
				"last_5":   "LWWWW",
			},
			map[string]any{
				"position": 8,
				"team":     map[string]any{"id": 16, "name": "Western Bulldogs", "logo": "https://media-2.api-sports.io/afl/teams/16.png"},
				"pts":      48,
				"games":    map[string]any{"played": 23, "win": 12, "drawn": 0, "lost": 11},
				"points":   map[string]any{"for": 1919, "against": 1766},
				"last_5":   "WLLWL",
			},
			map[string]any{
				"position": 9,
				"team":     map[string]any{"id": 18, "name": "Greater Western Sydney Giants", "logo": "https://media-3.api-sports.io/afl/teams/18.png"},
				"pts":      48,
				"games":    map[string]any{"played": 22, "win": 12, "drawn": 0, "lost": 10},
				"points":   map[string]any{"for": 1913, "against": 1812},
				"last_5":   "WLLWW",
			},
			map[string]any{
				"position": 10,
				"team":     map[string]any{"id": 1, "name": "Adelaide Crows", "logo": "https://media-1.api-sports.io/afl/teams/1.png"},
				"pts":      44,
				"games":    map[string]any{"played": 23, "win": 11, "drawn": 0, "lost": 12},
				"points":   map[string]any{"for": 2193, "against": 1877},
				"last_5":   "WLLWW",
			},
			map[string]any{
				"position": 11,
				"team":     map[string]any{"id": 5, "name": "Essendon Bombers", "logo": "https://media-3.api-sports.io/afl/teams/5.png"},
				"pts":      44,
				"games":    map[string]any{"played": 23, "win": 11, "drawn": 0, "lost": 12},
				"points":   map[string]any{"for": 1838, "against": 2050},
				"last_5":   "LLWWL",
			},
			map[string]any{
				"position": 12,
				"team":     map[string]any{"id": 7, "name": "Geelong Cats", "logo": "https://media-1.api-sports.io/afl/teams/7.png"},
				"pts":      42,
				"games":    map[string]any{"played": 23, "win": 10, "drawn": 1, "lost": 12},
				"points":   map[string]any{"for": 2088, "against": 1855},
				"last_5":   "LLLWL",
			},
			map[string]any{
				"position": 13,
				"team":     map[string]any{"id": 12, "name": "Richmond Tigers", "logo": "https://media-2.api-sports.io/afl/teams/12.png"},
				"pts":      42,
				"games":    map[string]any{"played": 23, "win": 10, "drawn": 1, "lost": 12},
				"points":   map[string]any{"for": 1856, "against": 1983},
				"last_5":   "LWLLL",
			},
			map[string]any{
				"position": 14,
				"team":     map[string]any{"id": 6, "name": "Fremantle Dockers", "logo": "https://media-2.api-sports.io/afl/teams/6.png"},
				"pts":      40,
				"games":    map[string]any{"played": 23, "win": 10, "drawn": 0, "lost": 13},
				"points":   map[string]any{"for": 1835, "against": 1898},
				"last_5":   "WLWLW",
			},
			map[string]any{
				"position": 15,
				"team":     map[string]any{"id": 17, "name": "Gold Coast Suns", "logo": "https://media-3.api-sports.io/afl/teams/17.png"},
				"pts":      36,
				"games":    map[string]any{"played": 23, "win": 9, "drawn": 0, "lost": 14},
				"points":   map[string]any{"for": 1839, "against": 2006},
				"last_5":   "LLLLW",
			},
			map[string]any{
				"position": 16,
				"team":     map[string]any{"id": 8, "name": "Hawthorn Hawks", "logo": "https://media-1.api-sports.io/afl/teams/8.png"},
				"pts":      28,
				"games":    map[string]any{"played": 23, "win": 7, "drawn": 0, "lost": 16},
				"points":   map[string]any{"for": 1686, "against": 2101},
				"last_5":   "LLWWL",
			},
			map[string]any{
				"position": 17,
				"team":     map[string]any{"id": 10, "name": "North Melbourne Kangaroos", "logo": "https://media-1.api-sports.io/afl/teams/10.png"},
				"pts":      12,
				"games":    map[string]any{"played": 23, "win": 3, "drawn": 0, "lost": 20},
				"points":   map[string]any{"for": 1657, "against": 2318},
				"last_5":   "WLLLL",
			},
			map[string]any{
				"position": 18,
				"team":     map[string]any{"id": 15, "name": "West Coast Eagles", "logo": "https://media-2.api-sports.io/afl/teams/15.png"},
				"pts":      12,
				"games":    map[string]any{"played": 23, "win": 3, "drawn": 0, "lost": 20},
				"points":   map[string]any{"for": 1418, "against": 2674},
				"last_5":   "LWLLW",
			},
		},
	}
}

// Placeholder functions for other sports (to be implemented)
func hockeyStandingsResponse(endpoint string) map[string]any {
	return map[string]any{
		"get":        "standings",
		"parameters": map[string]any{"league": "3", "season": "2024"},
		"errors":     []any{},
		"results":    1,
		"response": []any{
			[]any{
				// Western Conference
				map[string]any{
					"position":    1,
					"stage":       "OHL",
					"group":       map[string]any{"name": "Western Conference"},
					"team":        map[string]any{"id": 25, "name": "London Knights", "logo": "https://media.api-sports.io/hockey/teams/25.png"},
					"league":      map[string]any{"id": 3, "name": "OHL", "type": "League", "logo": "https://media.api-sports.io/hockey/leagues/3.png", "season": 2024},
					"country":     map[string]any{"id": 4, "name": "Canada", "code": "CA", "flag": "https://media.api-sports.io/flags/ca.svg"},
					"games":       map[string]any{"played": 62, "win": map[string]any{"total": 40, "percentage": "0.645"}, "win_overtime": map[string]any{"total": 5, "percentage": "0.081"}, "lose": map[string]any{"total": 15, "percentage": "0.242"}, "lose_overtime": map[string]any{"total": 2, "percentage": "0.032"}},
					"goals":       map[string]any{"for": 265, "against": 187},
					"points":      92,
					"form":        "WWWWW",
					"description": "Promotion - OHL (Play Offs)",
				},
				map[string]any{
					"position":    2,
					"stage":       "OHL",
					"group":       map[string]any{"name": "Western Conference"},
					"team":        map[string]any{"id": 33, "name": "Saginaw Spirit", "logo": "https://media.api-sports.io/hockey/teams/33.png"},
					"league":      map[string]any{"id": 3, "name": "OHL", "type": "League", "logo": "https://media.api-sports.io/hockey/leagues/3.png", "season": 2024},
					"country":     map[string]any{"id": 4, "name": "Canada", "code": "CA", "flag": "https://media.api-sports.io/flags/ca.svg"},
					"games":       map[string]any{"played": 62, "win": map[string]any{"total": 36, "percentage": "0.581"}, "win_overtime": map[string]any{"total": 5, "percentage": "0.081"}, "lose": map[string]any{"total": 16, "percentage": "0.258"}, "lose_overtime": map[string]any{"total": 5, "percentage": "0.081"}},
					"goals":       map[string]any{"for": 289, "against": 225},
					"points":      87,
					"form":        "WWWWW",
					"description": "Promotion - OHL (Play Offs)",
				},
				map[string]any{
					"position":    3,
					"stage":       "OHL",
					"group":       map[string]any{"name": "Western Conference"},
					"team":        map[string]any{"id": 24, "name": "Kitchener Rangers", "logo": "https://media.api-sports.io/hockey/teams/24.png"},
					"league":      map[string]any{"id": 3, "name": "OHL", "type": "League", "logo": "https://media.api-sports.io/hockey/leagues/3.png", "season": 2024},
					"country":     map[string]any{"id": 4, "name": "Canada", "code": "CA", "flag": "https://media.api-sports.io/flags/ca.svg"},
					"games":       map[string]any{"played": 63, "win": map[string]any{"total": 35, "percentage": "0.556"}, "win_overtime": map[string]any{"total": 5, "percentage": "0.079"}, "lose": map[string]any{"total": 16, "percentage": "0.254"}, "lose_overtime": map[string]any{"total": 7, "percentage": "0.111"}},
					"goals":       map[string]any{"for": 264, "against": 213},
					"points":      87,
					"form":        "WWLLWO",
					"description": "Promotion - OHL (Play Offs)",
				},
				map[string]any{
					"position":    4,
					"stage":       "OHL",
					"group":       map[string]any{"name": "Western Conference"},
					"team":        map[string]any{"id": 19, "name": "Flint Firebirds", "logo": "https://media.api-sports.io/hockey/teams/19.png"},
					"league":      map[string]any{"id": 3, "name": "OHL", "type": "League", "logo": "https://media.api-sports.io/hockey/leagues/3.png", "season": 2024},
					"country":     map[string]any{"id": 4, "name": "Canada", "code": "CA", "flag": "https://media.api-sports.io/flags/ca.svg"},
					"games":       map[string]any{"played": 63, "win": map[string]any{"total": 30, "percentage": "0.476"}, "win_overtime": map[string]any{"total": 10, "percentage": "0.159"}, "lose": map[string]any{"total": 21, "percentage": "0.333"}, "lose_overtime": map[string]any{"total": 2, "percentage": "0.032"}},
					"goals":       map[string]any{"for": 274, "against": 243},
					"points":      82,
					"form":        "LWOLLW",
					"description": "Promotion - OHL (Play Offs)",
				},
				map[string]any{
					"position":    5,
					"stage":       "OHL",
					"group":       map[string]any{"name": "Western Conference"},
					"team":        map[string]any{"id": 37, "name": "Windsor Spitfires", "logo": "https://media.api-sports.io/hockey/teams/37.png"},
					"league":      map[string]any{"id": 3, "name": "OHL", "type": "League", "logo": "https://media.api-sports.io/hockey/leagues/3.png", "season": 2024},
					"country":     map[string]any{"id": 4, "name": "Canada", "code": "CA", "flag": "https://media.api-sports.io/flags/ca.svg"},
					"games":       map[string]any{"played": 62, "win": map[string]any{"total": 24, "percentage": "0.387"}, "win_overtime": map[string]any{"total": 10, "percentage": "0.161"}, "lose": map[string]any{"total": 20, "percentage": "0.323"}, "lose_overtime": map[string]any{"total": 8, "percentage": "0.129"}},
					"goals":       map[string]any{"for": 256, "against": 233},
					"points":      76,
					"form":        "LLOLOWW",
					"description": "Promotion - OHL (Play Offs)",
				},
				map[string]any{
					"position":    6,
					"stage":       "OHL",
					"group":       map[string]any{"name": "Western Conference"},
					"team":        map[string]any{"id": 21, "name": "Guelph Storm", "logo": "https://media.api-sports.io/hockey/teams/21.png"},
					"league":      map[string]any{"id": 3, "name": "OHL", "type": "League", "logo": "https://media.api-sports.io/hockey/leagues/3.png", "season": 2024},
					"country":     map[string]any{"id": 4, "name": "Canada", "code": "CA", "flag": "https://media.api-sports.io/flags/ca.svg"},
					"games":       map[string]any{"played": 63, "win": map[string]any{"total": 25, "percentage": "0.397"}, "win_overtime": map[string]any{"total": 7, "percentage": "0.111"}, "lose": map[string]any{"total": 23, "percentage": "0.365"}, "lose_overtime": map[string]any{"total": 8, "percentage": "0.127"}},
					"goals":       map[string]any{"for": 218, "against": 209},
					"points":      72,
					"form":        "LLWOLL",
					"description": "Promotion - OHL (Play Offs)",
				},
				map[string]any{
					"position":    7,
					"stage":       "OHL",
					"group":       map[string]any{"name": "Western Conference"},
					"team":        map[string]any{"id": 31, "name": "Owen Sound Attack", "logo": "https://media.api-sports.io/hockey/teams/31.png"},
					"league":      map[string]any{"id": 3, "name": "OHL", "type": "League", "logo": "https://media.api-sports.io/hockey/leagues/3.png", "season": 2024},
					"country":     map[string]any{"id": 4, "name": "Canada", "code": "CA", "flag": "https://media.api-sports.io/flags/ca.svg"},
					"games":       map[string]any{"played": 62, "win": map[string]any{"total": 22, "percentage": "0.355"}, "win_overtime": map[string]any{"total": 8, "percentage": "0.129"}, "lose": map[string]any{"total": 24, "percentage": "0.387"}, "lose_overtime": map[string]any{"total": 8, "percentage": "0.129"}},
					"goals":       map[string]any{"for": 235, "against": 207},
					"points":      68,
					"form":        "LLWOWLO",
					"description": "Promotion - OHL (Play Offs)",
				},
				map[string]any{
					"position":    8,
					"stage":       "OHL",
					"group":       map[string]any{"name": "Western Conference"},
					"team":        map[string]any{"id": 18, "name": "Erie Otters", "logo": "https://media.api-sports.io/hockey/teams/18.png"},
					"league":      map[string]any{"id": 3, "name": "OHL", "type": "League", "logo": "https://media.api-sports.io/hockey/leagues/3.png", "season": 2024},
					"country":     map[string]any{"id": 4, "name": "Canada", "code": "CA", "flag": "https://media.api-sports.io/flags/ca.svg"},
					"games":       map[string]any{"played": 63, "win": map[string]any{"total": 23, "percentage": "0.365"}, "win_overtime": map[string]any{"total": 3, "percentage": "0.048"}, "lose": map[string]any{"total": 26, "percentage": "0.413"}, "lose_overtime": map[string]any{"total": 11, "percentage": "0.175"}},
					"goals":       map[string]any{"for": 229, "against": 236},
					"points":      63,
					"form":        "LWWLL",
					"description": "Promotion - OHL (Play Offs)",
				},
				map[string]any{
					"position":    9,
					"stage":       "OHL",
					"group":       map[string]any{"name": "Western Conference"},
					"team":        map[string]any{"id": 35, "name": "Soo Greyhounds", "logo": "https://media.api-sports.io/hockey/teams/35.png"},
					"league":      map[string]any{"id": 3, "name": "OHL", "type": "League", "logo": "https://media.api-sports.io/hockey/leagues/3.png", "season": 2024},
					"country":     map[string]any{"id": 4, "name": "Canada", "code": "CA", "flag": "https://media.api-sports.io/flags/ca.svg"},
					"games":       map[string]any{"played": 64, "win": map[string]any{"total": 27, "percentage": "0.422"}, "win_overtime": map[string]any{"total": 2, "percentage": "0.031"}, "lose": map[string]any{"total": 31, "percentage": "0.484"}, "lose_overtime": map[string]any{"total": 4, "percentage": "0.063"}},
					"goals":       map[string]any{"for": 253, "against": 257},
					"points":      62,
					"form":        "WWWLL",
					"description": nil,
				},
				map[string]any{
					"position":    10,
					"stage":       "OHL",
					"group":       map[string]any{"name": "Western Conference"},
					"team":        map[string]any{"id": 34, "name": "Sarnia Sting", "logo": "https://media.api-sports.io/hockey/teams/34.png"},
					"league":      map[string]any{"id": 3, "name": "OHL", "type": "League", "logo": "https://media.api-sports.io/hockey/leagues/3.png", "season": 2024},
					"country":     map[string]any{"id": 4, "name": "Canada", "code": "CA", "flag": "https://media.api-sports.io/flags/ca.svg"},
					"games":       map[string]any{"played": 62, "win": map[string]any{"total": 20, "percentage": "0.323"}, "win_overtime": map[string]any{"total": 2, "percentage": "0.032"}, "lose": map[string]any{"total": 34, "percentage": "0.548"}, "lose_overtime": map[string]any{"total": 6, "percentage": "0.097"}},
					"goals":       map[string]any{"for": 244, "against": 299},
					"points":      50,
					"form":        "WLWLL",
					"description": nil,
				},
				// Eastern Conference
				map[string]any{
					"position":    1,
					"stage":       "OHL",
					"group":       map[string]any{"name": "Eastern Conference"},
					"team":        map[string]any{"id": 30, "name": "Ottawa 67s", "logo": "https://media.api-sports.io/hockey/teams/30.png"},
					"league":      map[string]any{"id": 3, "name": "OHL", "type": "League", "logo": "https://media.api-sports.io/hockey/leagues/3.png", "season": 2024},
					"country":     map[string]any{"id": 4, "name": "Canada", "code": "CA", "flag": "https://media.api-sports.io/flags/ca.svg"},
					"games":       map[string]any{"played": 62, "win": map[string]any{"total": 37, "percentage": "0.597"}, "win_overtime": map[string]any{"total": 13, "percentage": "0.210"}, "lose": map[string]any{"total": 11, "percentage": "0.177"}, "lose_overtime": map[string]any{"total": 1, "percentage": "0.016"}},
					"goals":       map[string]any{"for": 296, "against": 164},
					"points":      101,
					"form":        "WWWWOWO",
					"description": "Promotion - OHL (Play Offs)",
				},
				map[string]any{
					"position":    2,
					"stage":       "OHL",
					"group":       map[string]any{"name": "Eastern Conference"},
					"team":        map[string]any{"id": 36, "name": "Sudbury Wolves", "logo": "https://media.api-sports.io/hockey/teams/36.png"},
					"league":      map[string]any{"id": 3, "name": "OHL", "type": "League", "logo": "https://media.api-sports.io/hockey/leagues/3.png", "season": 2024},
					"country":     map[string]any{"id": 4, "name": "Canada", "code": "CA", "flag": "https://media.api-sports.io/flags/ca.svg"},
					"games":       map[string]any{"played": 63, "win": map[string]any{"total": 29, "percentage": "0.460"}, "win_overtime": map[string]any{"total": 5, "percentage": "0.079"}, "lose": map[string]any{"total": 27, "percentage": "0.429"}, "lose_overtime": map[string]any{"total": 2, "percentage": "0.032"}},
					"goals":       map[string]any{"for": 259, "against": 240},
					"points":      70,
					"form":        "WLWLOW",
					"description": "Promotion - OHL (Play Offs)",
				},
				map[string]any{
					"position":    3,
					"stage":       "OHL",
					"group":       map[string]any{"name": "Eastern Conference"},
					"team":        map[string]any{"id": 32, "name": "Peterborough Petes", "logo": "https://media.api-sports.io/hockey/teams/32.png"},
					"league":      map[string]any{"id": 3, "name": "OHL", "type": "League", "logo": "https://media.api-sports.io/hockey/leagues/3.png", "season": 2024},
					"country":     map[string]any{"id": 4, "name": "Canada", "code": "CA", "flag": "https://media.api-sports.io/flags/ca.svg"},
					"games":       map[string]any{"played": 62, "win": map[string]any{"total": 36, "percentage": "0.581"}, "win_overtime": map[string]any{"total": 1, "percentage": "0.016"}, "lose": map[string]any{"total": 21, "percentage": "0.339"}, "lose_overtime": map[string]any{"total": 4, "percentage": "0.065"}},
					"goals":       map[string]any{"for": 250, "against": 198},
					"points":      78,
					"form":        "WWWWLO",
					"description": "Promotion - OHL (Play Offs)",
				},
				map[string]any{
					"position":    4,
					"stage":       "OHL",
					"group":       map[string]any{"name": "Eastern Conference"},
					"team":        map[string]any{"id": 29, "name": "Oshawa Generals", "logo": "https://media.api-sports.io/hockey/teams/29.png"},
					"league":      map[string]any{"id": 3, "name": "OHL", "type": "League", "logo": "https://media.api-sports.io/hockey/leagues/3.png", "season": 2024},
					"country":     map[string]any{"id": 4, "name": "Canada", "code": "CA", "flag": "https://media.api-sports.io/flags/ca.svg"},
					"games":       map[string]any{"played": 62, "win": map[string]any{"total": 26, "percentage": "0.419"}, "win_overtime": map[string]any{"total": 5, "percentage": "0.081"}, "lose": map[string]any{"total": 20, "percentage": "0.323"}, "lose_overtime": map[string]any{"total": 11, "percentage": "0.177"}},
					"goals":       map[string]any{"for": 229, "against": 227},
					"points":      73,
					"form":        "LLOLOLWO",
					"description": "Promotion - OHL (Play Offs)",
				},
				map[string]any{
					"position":    5,
					"stage":       "OHL",
					"group":       map[string]any{"name": "Eastern Conference"},
					"team":        map[string]any{"id": 17, "name": "Barrie Colts", "logo": "https://media.api-sports.io/hockey/teams/17.png"},
					"league":      map[string]any{"id": 3, "name": "OHL", "type": "League", "logo": "https://media.api-sports.io/hockey/leagues/3.png", "season": 2024},
					"country":     map[string]any{"id": 4, "name": "Canada", "code": "CA", "flag": "https://media.api-sports.io/flags/ca.svg"},
					"games":       map[string]any{"played": 63, "win": map[string]any{"total": 21, "percentage": "0.333"}, "win_overtime": map[string]any{"total": 8, "percentage": "0.127"}, "lose": map[string]any{"total": 28, "percentage": "0.444"}, "lose_overtime": map[string]any{"total": 6, "percentage": "0.095"}},
					"goals":       map[string]any{"for": 220, "against": 261},
					"points":      64,
					"form":        "WLWLL",
					"description": "Promotion - OHL (Play Offs)",
				},
				map[string]any{
					"position":    6,
					"stage":       "OHL",
					"group":       map[string]any{"name": "Eastern Conference"},
					"team":        map[string]any{"id": 26, "name": "Mississauga Steelheads", "logo": "https://media.api-sports.io/hockey/teams/26.png"},
					"league":      map[string]any{"id": 3, "name": "OHL", "type": "League", "logo": "https://media.api-sports.io/hockey/leagues/3.png", "season": 2024},
					"country":     map[string]any{"id": 4, "name": "Canada", "code": "CA", "flag": "https://media.api-sports.io/flags/ca.svg"},
					"games":       map[string]any{"played": 61, "win": map[string]any{"total": 23, "percentage": "0.377"}, "win_overtime": map[string]any{"total": 4, "percentage": "0.066"}, "lose": map[string]any{"total": 29, "percentage": "0.475"}, "lose_overtime": map[string]any{"total": 5, "percentage": "0.082"}},
					"goals":       map[string]any{"for": 223, "against": 227},
					"points":      59,
					"form":        "LWLWOW",
					"description": "Promotion - OHL (Play Offs)",
				},
				map[string]any{
					"position":    7,
					"stage":       "OHL",
					"group":       map[string]any{"name": "Eastern Conference"},
					"team":        map[string]any{"id": 22, "name": "Hamilton Bulldogs", "logo": "https://media.api-sports.io/hockey/teams/22.png"},
					"league":      map[string]any{"id": 3, "name": "OHL", "type": "League", "logo": "https://media.api-sports.io/hockey/leagues/3.png", "season": 2024},
					"country":     map[string]any{"id": 4, "name": "Canada", "code": "CA", "flag": "https://media.api-sports.io/flags/ca.svg"},
					"games":       map[string]any{"played": 62, "win": map[string]any{"total": 20, "percentage": "0.323"}, "win_overtime": map[string]any{"total": 4, "percentage": "0.065"}, "lose": map[string]any{"total": 30, "percentage": "0.484"}, "lose_overtime": map[string]any{"total": 8, "percentage": "0.129"}},
					"goals":       map[string]any{"for": 235, "against": 267},
					"points":      56,
					"form":        "LLLLLO",
					"description": "Promotion - OHL (Play Offs)",
				},
				map[string]any{
					"position":    8,
					"stage":       "OHL",
					"group":       map[string]any{"name": "Eastern Conference"},
					"team":        map[string]any{"id": 23, "name": "Kingston Frontenacs", "logo": "https://media.api-sports.io/hockey/teams/23.png"},
					"league":      map[string]any{"id": 3, "name": "OHL", "type": "League", "logo": "https://media.api-sports.io/hockey/leagues/3.png", "season": 2024},
					"country":     map[string]any{"id": 4, "name": "Canada", "code": "CA", "flag": "https://media.api-sports.io/flags/ca.svg"},
					"games":       map[string]any{"played": 62, "win": map[string]any{"total": 13, "percentage": "0.210"}, "win_overtime": map[string]any{"total": 6, "percentage": "0.097"}, "lose": map[string]any{"total": 39, "percentage": "0.629"}, "lose_overtime": map[string]any{"total": 4, "percentage": "0.065"}},
					"goals":       map[string]any{"for": 198, "against": 285},
					"points":      42,
					"form":        "LLLLW",
					"description": "Promotion - OHL (Play Offs)",
				},
				map[string]any{
					"position":    9,
					"stage":       "OHL",
					"group":       map[string]any{"name": "Eastern Conference"},
					"team":        map[string]any{"id": 27, "name": "Niagara IceDogs", "logo": "https://media.api-sports.io/hockey/teams/27.png"},
					"league":      map[string]any{"id": 3, "name": "OHL", "type": "League", "logo": "https://media.api-sports.io/hockey/leagues/3.png", "season": 2024},
					"country":     map[string]any{"id": 4, "name": "Canada", "code": "CA", "flag": "https://media.api-sports.io/flags/ca.svg"},
					"games":       map[string]any{"played": 63, "win": map[string]any{"total": 12, "percentage": "0.190"}, "win_overtime": map[string]any{"total": 6, "percentage": "0.095"}, "lose": map[string]any{"total": 39, "percentage": "0.619"}, "lose_overtime": map[string]any{"total": 6, "percentage": "0.095"}},
					"goals":       map[string]any{"for": 194, "against": 320},
					"points":      42,
					"form":        "LLLLL",
					"description": nil,
				},
				map[string]any{
					"position":    10,
					"stage":       "OHL",
					"group":       map[string]any{"name": "Eastern Conference"},
					"team":        map[string]any{"id": 28, "name": "North Bay Battalion", "logo": "https://media.api-sports.io/hockey/teams/28.png"},
					"league":      map[string]any{"id": 3, "name": "OHL", "type": "League", "logo": "https://media.api-sports.io/hockey/leagues/3.png", "season": 2024},
					"country":     map[string]any{"id": 4, "name": "Canada", "code": "CA", "flag": "https://media.api-sports.io/flags/ca.svg"},
					"games":       map[string]any{"played": 62, "win": map[string]any{"total": 14, "percentage": "0.226"}, "win_overtime": map[string]any{"total": 3, "percentage": "0.048"}, "lose": map[string]any{"total": 41, "percentage": "0.661"}, "lose_overtime": map[string]any{"total": 4, "percentage": "0.065"}},
					"goals":       map[string]any{"for": 189, "against": 314},
					"points":      38,
					"form":        "LLWWW",
					"description": nil,
				},
			},
		},
	}
}

func basketballStandingsResponse(endpoint, league string) map[string]any {
	// NBA v2 API uses different structure than v1 basketball
	return map[string]any{
		"get":        "standings/",
		"parameters": map[string]any{"league": "standard", "season": "2024"},
		"errors":     []any{},
		"results":    30,
		"response": []any{
			// Eastern Conference - Southeast Division
			map[string]any{
				"league":      "standard",
				"season":      2024,
				"team":        map[string]any{"id": 20, "name": "Miami Heat", "nickname": "Heat", "code": "MIA", "logo": "https://upload.wikimedia.org/wikipedia/fr/thumb/1/1c/Miami_Heat_-_Logo.svg/1200px-Miami_Heat_-_Logo.svg.png"},
				"conference":  map[string]any{"name": "east", "rank": 1, "win": 28, "loss": 13},
				"division":    map[string]any{"name": "southeast", "rank": 1, "win": 11, "loss": 2, "gamesBehind": "0.0"},
				"win":         map[string]any{"home": 23, "away": 21, "total": 44, "percentage": ".667", "lastTen": 8},
				"loss":        map[string]any{"home": 7, "away": 15, "total": 22, "percentage": ".333", "lastTen": 2},
				"gamesBehind": "0.0",
				"streak":      3,
				"winStreak":   true,
			},
			map[string]any{
				"league":      "standard",
				"season":      2024,
				"team":        map[string]any{"id": 5, "name": "Charlotte Hornets", "nickname": "Hornets", "code": "CHA", "logo": "https://upload.wikimedia.org/wikipedia/fr/thumb/f/f3/Hornets_de_Charlotte_logo.svg/1200px-Hornets_de_Charlotte_logo.svg.png"},
				"conference":  map[string]any{"name": "east", "rank": 9, "win": 21, "loss": 21},
				"division":    map[string]any{"name": "southeast", "rank": 2, "win": 5, "loss": 7, "gamesBehind": "12.0"},
				"win":         map[string]any{"home": 16, "away": 16, "total": 32, "percentage": ".485", "lastTen": 4},
				"loss":        map[string]any{"home": 16, "away": 18, "total": 34, "percentage": ".515", "lastTen": 6},
				"gamesBehind": "12.0",
				"streak":      1,
				"winStreak":   false,
			},
			map[string]any{
				"league":      "standard",
				"season":      2024,
				"team":        map[string]any{"id": 1, "name": "Atlanta Hawks", "nickname": "Hawks", "code": "ATL", "logo": "https://upload.wikimedia.org/wikipedia/fr/e/ee/Hawks_2016.png"},
				"conference":  map[string]any{"name": "east", "rank": 10, "win": 20, "loss": 21},
				"division":    map[string]any{"name": "southeast", "rank": 3, "win": 8, "loss": 5, "gamesBehind": "12.0"},
				"win":         map[string]any{"home": 19, "away": 12, "total": 31, "percentage": ".484", "lastTen": 5},
				"loss":        map[string]any{"home": 13, "away": 20, "total": 33, "percentage": ".516", "lastTen": 5},
				"gamesBehind": "12.0",
				"streak":      1,
				"winStreak":   false,
			},
			map[string]any{
				"league":      "standard",
				"season":      2024,
				"team":        map[string]any{"id": 41, "name": "Washington Wizards", "nickname": "Wizards", "code": "WAS", "logo": "https://upload.wikimedia.org/wikipedia/fr/archive/d/d6/20161212034849%21Wizards2015.png"},
				"conference":  map[string]any{"name": "east", "rank": 11, "win": 22, "loss": 21},
				"division":    map[string]any{"name": "southeast", "rank": 4, "win": 6, "loss": 7, "gamesBehind": "13.5"},
				"win":         map[string]any{"home": 17, "away": 12, "total": 29, "percentage": ".460", "lastTen": 5},
				"loss":        map[string]any{"home": 17, "away": 17, "total": 34, "percentage": ".540", "lastTen": 5},
				"gamesBehind": "13.5",
				"streak":      1,
				"winStreak":   true,
			},
			map[string]any{
				"league":      "standard",
				"season":      2024,
				"team":        map[string]any{"id": 26, "name": "Orlando Magic", "nickname": "Magic", "code": "ORL", "logo": "https://upload.wikimedia.org/wikipedia/fr/b/bd/Orlando_Magic_logo_2010.png"},
				"conference":  map[string]any{"name": "east", "rank": 15, "win": 10, "loss": 32},
				"division":    map[string]any{"name": "southeast", "rank": 5, "win": 2, "loss": 11, "gamesBehind": "28.0"},
				"win":         map[string]any{"home": 7, "away": 9, "total": 16, "percentage": ".242", "lastTen": 3},
				"loss":        map[string]any{"home": 23, "away": 27, "total": 50, "percentage": ".758", "lastTen": 7},
				"gamesBehind": "28.0",
				"streak":      2,
				"winStreak":   false,
			},
			// Eastern Conference - Atlantic Division
			map[string]any{
				"league":      "standard",
				"season":      2024,
				"team":        map[string]any{"id": 27, "name": "Philadelphia 76ers", "nickname": "76ers", "code": "PHI", "logo": "https://upload.wikimedia.org/wikipedia/fr/4/48/76ers_2016.png"},
				"conference":  map[string]any{"name": "east", "rank": 2, "win": 24, "loss": 15},
				"division":    map[string]any{"name": "atlantic", "rank": 1, "win": 6, "loss": 7, "gamesBehind": "0.0"},
				"win":         map[string]any{"home": 19, "away": 21, "total": 40, "percentage": ".625", "lastTen": 8},
				"loss":        map[string]any{"home": 13, "away": 11, "total": 24, "percentage": ".375", "lastTen": 2},
				"gamesBehind": "3.0",
				"streak":      1,
				"winStreak":   true,
			},
			map[string]any{
				"league":      "standard",
				"season":      2024,
				"team":        map[string]any{"id": 2, "name": "Boston Celtics", "nickname": "Celtics", "code": "BOS", "logo": "https://upload.wikimedia.org/wikipedia/fr/thumb/6/65/Celtics_de_Boston_logo.svg/1024px-Celtics_de_Boston_logo.svg.png"},
				"conference":  map[string]any{"name": "east", "rank": 5, "win": 28, "loss": 16},
				"division":    map[string]any{"name": "atlantic", "rank": 2, "win": 9, "loss": 6, "gamesBehind": "2.0"},
				"win":         map[string]any{"home": 23, "away": 16, "total": 39, "percentage": ".591", "lastTen": 8},
				"loss":        map[string]any{"home": 11, "away": 16, "total": 27, "percentage": ".409", "lastTen": 2},
				"gamesBehind": "5.0",
				"streak":      3,
				"winStreak":   true,
			},
			map[string]any{
				"league":      "standard",
				"season":      2024,
				"team":        map[string]any{"id": 38, "name": "Toronto Raptors", "nickname": "Raptors", "code": "TOR", "logo": "https://upload.wikimedia.org/wikipedia/fr/8/89/Raptors2015.png"},
				"conference":  map[string]any{"name": "east", "rank": 7, "win": 23, "loss": 19},
				"division":    map[string]any{"name": "atlantic", "rank": 3, "win": 7, "loss": 5, "gamesBehind": "6.0"},
				"win":         map[string]any{"home": 17, "away": 17, "total": 34, "percentage": ".531", "lastTen": 3},
				"loss":        map[string]any{"home": 15, "away": 15, "total": 30, "percentage": ".469", "lastTen": 7},
				"gamesBehind": "9.0",
				"streak":      3,
				"winStreak":   false,
			},
			map[string]any{
				"league":      "standard",
				"season":      2024,
				"team":        map[string]any{"id": 4, "name": "Brooklyn Nets", "nickname": "Nets", "code": "BKN", "logo": "https://upload.wikimedia.org/wikipedia/commons/thumb/4/44/Brooklyn_Nets_newlogo.svg/130px-Brooklyn_Nets_newlogo.svg.png"},
				"conference":  map[string]any{"name": "east", "rank": 8, "win": 23, "loss": 18},
				"division":    map[string]any{"name": "atlantic", "rank": 4, "win": 7, "loss": 6, "gamesBehind": "8.0"},
				"win":         map[string]any{"home": 13, "away": 20, "total": 33, "percentage": ".500", "lastTen": 4},
				"loss":        map[string]any{"home": 18, "away": 15, "total": 33, "percentage": ".500", "lastTen": 6},
				"gamesBehind": "11.0",
				"streak":      1,
				"winStreak":   true,
			},
			map[string]any{
				"league":      "standard",
				"season":      2024,
				"team":        map[string]any{"id": 24, "name": "New York Knicks", "nickname": "Knicks", "code": "NYK", "logo": "https://upload.wikimedia.org/wikipedia/fr/d/dc/NY_Knicks_Logo_2011.png"},
				"conference":  map[string]any{"name": "east", "rank": 12, "win": 14, "loss": 25},
				"division":    map[string]any{"name": "atlantic", "rank": 5, "win": 4, "loss": 9, "gamesBehind": "13.5"},
				"win":         map[string]any{"home": 13, "away": 14, "total": 27, "percentage": ".415", "lastTen": 3},
				"loss":        map[string]any{"home": 19, "away": 19, "total": 38, "percentage": ".585", "lastTen": 7},
				"gamesBehind": "16.5",
				"streak":      2,
				"winStreak":   true,
			},
			// Eastern Conference - Central Division
			map[string]any{
				"league":      "standard",
				"season":      2024,
				"team":        map[string]any{"id": 21, "name": "Milwaukee Bucks", "nickname": "Bucks", "code": "MIL", "logo": "https://upload.wikimedia.org/wikipedia/fr/3/34/Bucks2015.png"},
				"conference":  map[string]any{"name": "east", "rank": 3, "win": 25, "loss": 18},
				"division":    map[string]any{"name": "central", "rank": 1, "win": 9, "loss": 3, "gamesBehind": "0.0"},
				"win":         map[string]any{"home": 23, "away": 18, "total": 41, "percentage": ".621", "lastTen": 6},
				"loss":        map[string]any{"home": 12, "away": 13, "total": 25, "percentage": ".379", "lastTen": 4},
				"gamesBehind": "3.0",
				"streak":      5,
				"winStreak":   true,
			},
			map[string]any{
				"league":      "standard",
				"season":      2024,
				"team":        map[string]any{"id": 6, "name": "Chicago Bulls", "nickname": "Bulls", "code": "CHI", "logo": "https://upload.wikimedia.org/wikipedia/fr/thumb/d/d1/Bulls_de_Chicago_logo.svg/1200px-Bulls_de_Chicago_logo.svg.png"},
				"conference":  map[string]any{"name": "east", "rank": 4, "win": 24, "loss": 17},
				"division":    map[string]any{"name": "central", "rank": 2, "win": 7, "loss": 4, "gamesBehind": "1.5"},
				"win":         map[string]any{"home": 24, "away": 15, "total": 39, "percentage": ".600", "lastTen": 5},
				"loss":        map[string]any{"home": 10, "away": 16, "total": 26, "percentage": ".400", "lastTen": 5},
				"gamesBehind": "4.5",
				"streak":      5,
				"winStreak":   false,
			},
			map[string]any{
				"league":      "standard",
				"season":      2024,
				"team":        map[string]any{"id": 7, "name": "Cleveland Cavaliers", "nickname": "Cavaliers", "code": "CLE", "logo": "https://upload.wikimedia.org/wikipedia/fr/thumb/0/06/Cavs_de_Cleveland_logo_2017.png/150px-Cavs_de_Cleveland_logo_2017.png"},
				"conference":  map[string]any{"name": "east", "rank": 6, "win": 23, "loss": 16},
				"division":    map[string]any{"name": "central", "rank": 3, "win": 8, "loss": 4, "gamesBehind": "2.5"},
				"win":         map[string]any{"home": 20, "away": 18, "total": 38, "percentage": ".585", "lastTen": 4},
				"loss":        map[string]any{"home": 11, "away": 16, "total": 27, "percentage": ".415", "lastTen": 6},
				"gamesBehind": "5.5",
				"streak":      2,
				"winStreak":   true,
			},
			map[string]any{
				"league":      "standard",
				"season":      2024,
				"team":        map[string]any{"id": 15, "name": "Indiana Pacers", "nickname": "Pacers", "code": "IND", "logo": "https://upload.wikimedia.org/wikipedia/fr/thumb/c/cf/Pacers_de_l%27Indiana_logo.svg/1180px-Pacers_de_l%27Indiana_logo.svg.png"},
				"conference":  map[string]any{"name": "east", "rank": 13, "win": 11, "loss": 33},
				"division":    map[string]any{"name": "central", "rank": 4, "win": 2, "loss": 13, "gamesBehind": "19.5"},
				"win":         map[string]any{"home": 15, "away": 7, "total": 22, "percentage": ".328", "lastTen": 3},
				"loss":        map[string]any{"home": 19, "away": 26, "total": 45, "percentage": ".672", "lastTen": 7},
				"gamesBehind": "22.5",
				"streak":      3,
				"winStreak":   false,
			},
			map[string]any{
				"league":      "standard",
				"season":      2024,
				"team":        map[string]any{"id": 10, "name": "Detroit Pistons", "nickname": "Pistons", "code": "DET", "logo": "https://upload.wikimedia.org/wikipedia/commons/thumb/6/6a/Detroit_Pistons_primary_logo_2017.png/150px-Detroit_Pistons_primary_logo_2017.png"},
				"conference":  map[string]any{"name": "east", "rank": 14, "win": 14, "loss": 25},
				"division":    map[string]any{"name": "central", "rank": 5, "win": 5, "loss": 7, "gamesBehind": "22.5"},
				"win":         map[string]any{"home": 11, "away": 7, "total": 18, "percentage": ".277", "lastTen": 6},
				"loss":        map[string]any{"home": 21, "away": 26, "total": 47, "percentage": ".723", "lastTen": 4},
				"gamesBehind": "25.5",
				"streak":      3,
				"winStreak":   true,
			},
			// Western Conference - Southwest Division
			map[string]any{
				"league":      "standard",
				"season":      2024,
				"team":        map[string]any{"id": 19, "name": "Memphis Grizzlies", "nickname": "Grizzlies", "code": "MEM", "logo": "https://upload.wikimedia.org/wikipedia/en/thumb/f/f1/Memphis_Grizzlies.svg/1200px-Memphis_Grizzlies.svg.png"},
				"conference":  map[string]any{"name": "west", "rank": 2, "win": 30, "loss": 14},
				"division":    map[string]any{"name": "southwest", "rank": 1, "win": 8, "loss": 5, "gamesBehind": "0.0"},
				"win":         map[string]any{"home": 23, "away": 22, "total": 45, "percentage": ".672", "lastTen": 6},
				"loss":        map[string]any{"home": 10, "away": 12, "total": 22, "percentage": ".328", "lastTen": 4},
				"gamesBehind": "8.0",
				"streak":      1,
				"winStreak":   true,
			},
			map[string]any{
				"league":      "standard",
				"season":      2024,
				"team":        map[string]any{"id": 8, "name": "Dallas Mavericks", "nickname": "Mavericks", "code": "DAL", "logo": "https://upload.wikimedia.org/wikipedia/fr/thumb/b/b8/Mavericks_de_Dallas_logo.svg/150px-Mavericks_de_Dallas_logo.svg.png"},
				"conference":  map[string]any{"name": "west", "rank": 5, "win": 29, "loss": 15},
				"division":    map[string]any{"name": "southwest", "rank": 2, "win": 11, "loss": 2, "gamesBehind": "4.0"},
				"win":         map[string]any{"home": 23, "away": 17, "total": 40, "percentage": ".615", "lastTen": 8},
				"loss":        map[string]any{"home": 11, "away": 14, "total": 25, "percentage": ".385", "lastTen": 2},
				"gamesBehind": "12.0",
				"streak":      5,
				"winStreak":   true,
			},
			map[string]any{
				"league":      "standard",
				"season":      2024,
				"team":        map[string]any{"id": 23, "name": "New Orleans Pelicans", "nickname": "Pelicans", "code": "NOP", "logo": "https://upload.wikimedia.org/wikipedia/fr/thumb/2/21/New_Orleans_Pelicans.png/200px-New_Orleans_Pelicans.png"},
				"conference":  map[string]any{"name": "west", "rank": 10, "win": 18, "loss": 22},
				"division":    map[string]any{"name": "southwest", "rank": 3, "win": 4, "loss": 8, "gamesBehind": "17.0"},
				"win":         map[string]any{"home": 15, "away": 12, "total": 27, "percentage": ".415", "lastTen": 5},
				"loss":        map[string]any{"home": 17, "away": 21, "total": 38, "percentage": ".585", "lastTen": 5},
				"gamesBehind": "25.0",
				"streak":      2,
				"winStreak":   false,
			},
			map[string]any{
				"league":      "standard",
				"season":      2024,
				"team":        map[string]any{"id": 31, "name": "San Antonio Spurs", "nickname": "Spurs", "code": "SAS", "logo": "https://upload.wikimedia.org/wikipedia/fr/0/0e/San_Antonio_Spurs_2018.png"},
				"conference":  map[string]any{"name": "west", "rank": 12, "win": 15, "loss": 22},
				"division":    map[string]any{"name": "southwest", "rank": 4, "win": 4, "loss": 7, "gamesBehind": "19.0"},
				"win":         map[string]any{"home": 12, "away": 13, "total": 25, "percentage": ".385", "lastTen": 5},
				"loss":        map[string]any{"home": 19, "away": 21, "total": 40, "percentage": ".615", "lastTen": 5},
				"gamesBehind": "27.0",
				"streak":      1,
				"winStreak":   true,
			},
			map[string]any{
				"league":      "standard",
				"season":      2024,
				"team":        map[string]any{"id": 14, "name": "Houston Rockets", "nickname": "Rockets", "code": "HOU", "logo": "https://upload.wikimedia.org/wikipedia/fr/thumb/d/de/Houston_Rockets_logo_2003.png/330px-Houston_Rockets_logo_2003.png"},
				"conference":  map[string]any{"name": "west", "rank": 15, "win": 8, "loss": 32},
				"division":    map[string]any{"name": "southwest", "rank": 5, "win": 3, "loss": 8, "gamesBehind": "28.0"},
				"win":         map[string]any{"home": 9, "away": 7, "total": 16, "percentage": ".246", "lastTen": 1},
				"loss":        map[string]any{"home": 21, "away": 28, "total": 49, "percentage": ".754", "lastTen": 9},
				"gamesBehind": "36.0",
				"streak":      1,
				"winStreak":   false,
			},
			// Western Conference - Pacific Division
			map[string]any{
				"league":      "standard",
				"season":      2024,
				"team":        map[string]any{"id": 28, "name": "Phoenix Suns", "nickname": "Suns", "code": "PHX", "logo": "https://upload.wikimedia.org/wikipedia/fr/5/56/Phoenix_Suns_2013.png"},
				"conference":  map[string]any{"name": "west", "rank": 1, "win": 30, "loss": 9},
				"division":    map[string]any{"name": "pacific", "rank": 1, "win": 6, "loss": 4, "gamesBehind": "0.0"},
				"win":         map[string]any{"home": 28, "away": 24, "total": 52, "percentage": ".800", "lastTen": 7},
				"loss":        map[string]any{"home": 7, "away": 6, "total": 13, "percentage": ".200", "lastTen": 3},
				"gamesBehind": "0.0",
				"streak":      1,
				"winStreak":   true,
			},
			map[string]any{
				"league":      "standard",
				"season":      2024,
				"team":        map[string]any{"id": 11, "name": "Golden State Warriors", "nickname": "Warriors", "code": "GSW", "logo": "https://upload.wikimedia.org/wikipedia/fr/thumb/d/de/Warriors_de_Golden_State_logo.svg/1200px-Warriors_de_Golden_State_logo.svg.png"},
				"conference":  map[string]any{"name": "west", "rank": 3, "win": 26, "loss": 16},
				"division":    map[string]any{"name": "pacific", "rank": 2, "win": 9, "loss": 3, "gamesBehind": "9.0"},
				"win":         map[string]any{"home": 26, "away": 17, "total": 43, "percentage": ".662", "lastTen": 2},
				"loss":        map[string]any{"home": 7, "away": 15, "total": 22, "percentage": ".338", "lastTen": 8},
				"gamesBehind": "9.0",
				"streak":      5,
				"winStreak":   false,
			},
			map[string]any{
				"league":      "standard",
				"season":      2024,
				"team":        map[string]any{"id": 16, "name": "LA Clippers", "nickname": "Clippers", "code": "LAC", "logo": "https://upload.wikimedia.org/wikipedia/fr/d/d6/Los_Angeles_Clippers_logo_2010.png"},
				"conference":  map[string]any{"name": "west", "rank": 8, "win": 21, "loss": 23},
				"division":    map[string]any{"name": "pacific", "rank": 3, "win": 7, "loss": 6, "gamesBehind": "18.5"},
				"win":         map[string]any{"home": 19, "away": 15, "total": 34, "percentage": ".515", "lastTen": 7},
				"loss":        map[string]any{"home": 14, "away": 18, "total": 32, "percentage": ".485", "lastTen": 3},
				"gamesBehind": "18.5",
				"streak":      1,
				"winStreak":   false,
			},
			map[string]any{
				"league":      "standard",
				"season":      2024,
				"team":        map[string]any{"id": 17, "name": "Los Angeles Lakers", "nickname": "Lakers", "code": "LAL", "logo": "https://upload.wikimedia.org/wikipedia/commons/thumb/3/3c/Los_Angeles_Lakers_logo.svg/220px-Los_Angeles_Lakers_logo.svg.png"},
				"conference":  map[string]any{"name": "west", "rank": 9, "win": 16, "loss": 24},
				"division":    map[string]any{"name": "pacific", "rank": 4, "win": 3, "loss": 10, "gamesBehind": "23.5"},
				"win":         map[string]any{"home": 19, "away": 9, "total": 28, "percentage": ".438", "lastTen": 2},
				"loss":        map[string]any{"home": 16, "away": 20, "total": 36, "percentage": ".562", "lastTen": 8},
				"gamesBehind": "23.5",
				"streak":      1,
				"winStreak":   false,
			},
			map[string]any{
				"league":      "standard",
				"season":      2024,
				"team":        map[string]any{"id": 30, "name": "Sacramento Kings", "nickname": "Kings", "code": "SAC", "logo": "https://upload.wikimedia.org/wikipedia/fr/thumb/9/95/Kings_de_Sacramento_logo.svg/1200px-Kings_de_Sacramento_logo.svg.png"},
				"conference":  map[string]any{"name": "west", "rank": 13, "win": 17, "loss": 26},
				"division":    map[string]any{"name": "pacific", "rank": 5, "win": 5, "loss": 7, "gamesBehind": "29.0"},
				"win":         map[string]any{"home": 15, "away": 9, "total": 24, "percentage": ".358", "lastTen": 3},
				"loss":        map[string]any{"home": 19, "away": 24, "total": 43, "percentage": ".642", "lastTen": 7},
				"gamesBehind": "29.0",
				"streak":      2,
				"winStreak":   false,
			},
			// Western Conference - Northwest Division
			map[string]any{
				"league":      "standard",
				"season":      2024,
				"team":        map[string]any{"id": 40, "name": "Utah Jazz", "nickname": "Jazz", "code": "UTA", "logo": "https://upload.wikimedia.org/wikipedia/fr/3/3b/Jazz_de_l%27Utah_logo.png"},
				"conference":  map[string]any{"name": "west", "rank": 4, "win": 26, "loss": 14},
				"division":    map[string]any{"name": "northwest", "rank": 1, "win": 12, "loss": 1, "gamesBehind": "0.0"},
				"win":         map[string]any{"home": 22, "away": 18, "total": 40, "percentage": ".625", "lastTen": 7},
				"loss":        map[string]any{"home": 10, "away": 14, "total": 24, "percentage": ".375", "lastTen": 3},
				"gamesBehind": "11.5",
				"streak":      1,
				"winStreak":   false,
			},
			map[string]any{
				"league":      "standard",
				"season":      2024,
				"team":        map[string]any{"id": 9, "name": "Denver Nuggets", "nickname": "Nuggets", "code": "DEN", "logo": "https://upload.wikimedia.org/wikipedia/fr/thumb/3/35/Nuggets_de_Denver_2018.png/180px-Nuggets_de_Denver_2018.png"},
				"conference":  map[string]any{"name": "west", "rank": 6, "win": 24, "loss": 18},
				"division":    map[string]any{"name": "northwest", "rank": 2, "win": 5, "loss": 9, "gamesBehind": "1.5"},
				"win":         map[string]any{"home": 20, "away": 19, "total": 39, "percentage": ".600", "lastTen": 9},
				"loss":        map[string]any{"home": 11, "away": 15, "total": 26, "percentage": ".400", "lastTen": 1},
				"gamesBehind": "13.0",
				"streak":      3,
				"winStreak":   true,
			},
			map[string]any{
				"league":      "standard",
				"season":      2024,
				"team":        map[string]any{"id": 22, "name": "Minnesota Timberwolves", "nickname": "Timberwolves", "code": "MIN", "logo": "https://upload.wikimedia.org/wikipedia/fr/thumb/d/d9/Timberwolves_du_Minnesota_logo_2017.png/200px-Timberwolves_du_Minnesota_logo_2017.png"},
				"conference":  map[string]any{"name": "west", "rank": 7, "win": 25, "loss": 18},
				"division":    map[string]any{"name": "northwest", "rank": 3, "win": 10, "loss": 4, "gamesBehind": "4.0"},
				"win":         map[string]any{"home": 21, "away": 16, "total": 37, "percentage": ".561", "lastTen": 8},
				"loss":        map[string]any{"home": 12, "away": 17, "total": 29, "percentage": ".439", "lastTen": 2},
				"gamesBehind": "15.5",
				"streak":      5,
				"winStreak":   true,
			},
			map[string]any{
				"league":      "standard",
				"season":      2024,
				"team":        map[string]any{"id": 29, "name": "Portland Trail Blazers", "nickname": "Trail Blazers", "code": "POR", "logo": "https://upload.wikimedia.org/wikipedia/en/thumb/2/21/Portland_Trail_Blazers_logo.svg/1200px-Portland_Trail_Blazers_logo.svg.png"},
				"conference":  map[string]any{"name": "west", "rank": 11, "win": 11, "loss": 29},
				"division":    map[string]any{"name": "northwest", "rank": 4, "win": 1, "loss": 11, "gamesBehind": "15.0"},
				"win":         map[string]any{"home": 16, "away": 9, "total": 25, "percentage": ".391", "lastTen": 4},
				"loss":        map[string]any{"home": 18, "away": 21, "total": 39, "percentage": ".609", "lastTen": 6},
				"gamesBehind": "26.5",
				"streak":      5,
				"winStreak":   false,
			},
			map[string]any{
				"league":      "standard",
				"season":      2024,
				"team":        map[string]any{"id": 25, "name": "Oklahoma City Thunder", "nickname": "Thunder", "code": "OKC", "logo": "https://upload.wikimedia.org/wikipedia/fr/thumb/4/4f/Thunder_d%27Oklahoma_City_logo.svg/1200px-Thunder_d%27Oklahoma_City_logo.svg.png"},
				"conference":  map[string]any{"name": "west", "rank": 14, "win": 14, "loss": 28},
				"division":    map[string]any{"name": "northwest", "rank": 5, "win": 4, "loss": 7, "gamesBehind": "20.5"},
				"win":         map[string]any{"home": 9, "away": 11, "total": 20, "percentage": ".308", "lastTen": 3},
				"loss":        map[string]any{"home": 24, "away": 21, "total": 45, "percentage": ".692", "lastTen": 7},
				"gamesBehind": "32.0",
				"streak":      3,
				"winStreak":   false,
			},
		},
	}
}

func baseballStandingsResponse(endpoint, league string) map[string]any {
	return map[string]any{
		"get":        "standings",
		"parameters": map[string]any{"league": "1", "season": "2024"},
		"errors":     []any{},
		"results":    1,
		"response": []any{
			[]any{
				map[string]any{
					"position": 1,
					"stage":    nil,
					"group":    map[string]any{"name": "American League"},
					"team":     map[string]any{"id": 25, "name": "New York Yankees", "logo": "https://media.api-sports.io/baseball/teams/25.png"},
					"league":   map[string]any{"id": 1, "name": "MLB", "type": "League", "logo": "https://media.api-sports.io/baseball/leagues/1.png", "season": 2024},
					"country":  map[string]any{"id": 1, "name": "USA", "code": "US", "flag": "https://media.api-sports.io/flags/us.svg"},
					"games":    map[string]any{"played": 162, "win": map[string]any{"total": 94, "percentage": "0.580"}, "lose": map[string]any{"total": 68, "percentage": "0.420"}},
					"points":   map[string]any{"for": 1041, "against": 894},
					"form":     "LWLLL",
				},
				map[string]any{
					"position": 2,
					"stage":    nil,
					"group":    map[string]any{"name": "American League"},
					"team":     map[string]any{"id": 9, "name": "Cleveland Guardians", "logo": "https://media.api-sports.io/baseball/teams/9.png"},
					"league":   map[string]any{"id": 1, "name": "MLB", "type": "League", "logo": "https://media.api-sports.io/baseball/leagues/1.png", "season": 2024},
					"country":  map[string]any{"id": 1, "name": "USA", "code": "US", "flag": "https://media.api-sports.io/flags/us.svg"},
					"games":    map[string]any{"played": 161, "win": map[string]any{"total": 92, "percentage": "0.571"}, "lose": map[string]any{"total": 69, "percentage": "0.429"}},
					"points":   map[string]any{"for": 900, "against": 813},
					"form":     "LLWLL",
				},
				map[string]any{
					"position": 3,
					"stage":    nil,
					"group":    map[string]any{"name": "American League"},
					"team":     map[string]any{"id": 4, "name": "Baltimore Orioles", "logo": "https://media.api-sports.io/baseball/teams/4.png"},
					"league":   map[string]any{"id": 1, "name": "MLB", "type": "League", "logo": "https://media.api-sports.io/baseball/leagues/1.png", "season": 2024},
					"country":  map[string]any{"id": 1, "name": "USA", "code": "US", "flag": "https://media.api-sports.io/flags/us.svg"},
					"games":    map[string]any{"played": 162, "win": map[string]any{"total": 91, "percentage": "0.562"}, "lose": map[string]any{"total": 71, "percentage": "0.438"}},
					"points":   map[string]any{"for": 970, "against": 827},
					"form":     "LLWWW",
				},
				map[string]any{
					"position": 1,
					"stage":    nil,
					"group":    map[string]any{"name": "National League"},
					"team":     map[string]any{"id": 21, "name": "Los Angeles Dodgers", "logo": "https://media.api-sports.io/baseball/teams/21.png"},
					"league":   map[string]any{"id": 1, "name": "MLB", "type": "League", "logo": "https://media.api-sports.io/baseball/leagues/1.png", "season": 2024},
					"country":  map[string]any{"id": 1, "name": "USA", "code": "US", "flag": "https://media.api-sports.io/flags/us.svg"},
					"games":    map[string]any{"played": 162, "win": map[string]any{"total": 95, "percentage": "0.586"}, "lose": map[string]any{"total": 67, "percentage": "0.414"}},
					"points":   map[string]any{"for": 874, "against": 684},
					"form":     "WWWWL",
				},
				map[string]any{
					"position": 2,
					"stage":    nil,
					"group":    map[string]any{"name": "National League"},
					"team":     map[string]any{"id": 28, "name": "Philadelphia Phillies", "logo": "https://media.api-sports.io/baseball/teams/28.png"},
					"league":   map[string]any{"id": 1, "name": "MLB", "type": "League", "logo": "https://media.api-sports.io/baseball/leagues/1.png", "season": 2024},
					"country":  map[string]any{"id": 1, "name": "USA", "code": "US", "flag": "https://media.api-sports.io/flags/us.svg"},
					"games":    map[string]any{"played": 162, "win": map[string]any{"total": 90, "percentage": "0.556"}, "lose": map[string]any{"total": 72, "percentage": "0.444"}},
					"points":   map[string]any{"for": 843, "against": 765},
					"form":     "WLWLW",
				},
				map[string]any{
					"position": 3,
					"stage":    nil,
					"group":    map[string]any{"name": "National League"},
					"team":     map[string]any{"id": 19, "name": "Atlanta Braves", "logo": "https://media.api-sports.io/baseball/teams/19.png"},
					"league":   map[string]any{"id": 1, "name": "MLB", "type": "League", "logo": "https://media.api-sports.io/baseball/leagues/1.png", "season": 2024},
					"country":  map[string]any{"id": 1, "name": "USA", "code": "US", "flag": "https://media.api-sports.io/flags/us.svg"},
					"games":    map[string]any{"played": 162, "win": map[string]any{"total": 88, "percentage": "0.543"}, "lose": map[string]any{"total": 74, "percentage": "0.457"}},
					"points":   map[string]any{"for": 914, "against": 842},
					"form":     "LWWLW",
				},
			},
		},
	}
}

func americanFootballStandingsResponse(endpoint string) map[string]any {
	return map[string]any{
		"get":        "standings",
		"parameters": map[string]any{"league": "1", "season": "2024"},
		"errors":     []any{},
		"results":    32,
		"response": []any{
			// AFC East
			map[string]any{
				"league":     map[string]any{"id": 1, "name": "NFL", "season": 2024, "logo": "https://media.api-sports.io/american-football/leagues/1.png", "country": map[string]any{"name": "USA", "code": "US", "flag": "https://media.api-sports.io/flags/us.svg"}},
				"conference": "American Football Conference",
				"division":   "East",
				"position":   1,
				"team":       map[string]any{"id": 25, "name": "Miami Dolphins", "logo": "https://media.api-sports.io/american-football/teams/25.png"},
				"won":        3,
				"lost":       1,
				"ties":       0,
				"points":     map[string]any{"for": 98, "against": 91, "difference": 7},
				"records":    map[string]any{"home": "2-0", "road": "1-1", "conference": "3-1", "division": "2-0"},
				"streak":     "L1",
			},
			map[string]any{
				"league":     map[string]any{"id": 1, "name": "NFL", "season": 2024, "logo": "https://media.api-sports.io/american-football/leagues/1.png", "country": map[string]any{"name": "USA", "code": "US", "flag": "https://media.api-sports.io/flags/us.svg"}},
				"conference": "American Football Conference",
				"division":   "East",
				"position":   2,
				"team":       map[string]any{"id": 20, "name": "Buffalo Bills", "logo": "https://media.api-sports.io/american-football/teams/20.png"},
				"won":        2,
				"lost":       1,
				"ties":       0,
				"points":     map[string]any{"for": 91, "against": 38, "difference": 53},
				"records":    map[string]any{"home": "1-0", "road": "1-1", "conference": "1-1", "division": "0-1"},
				"streak":     "L1",
			},
			map[string]any{
				"league":     map[string]any{"id": 1, "name": "NFL", "season": 2024, "logo": "https://media.api-sports.io/american-football/leagues/1.png", "country": map[string]any{"name": "USA", "code": "US", "flag": "https://media.api-sports.io/flags/us.svg"}},
				"conference": "American Football Conference",
				"division":   "East",
				"position":   3,
				"team":       map[string]any{"id": 13, "name": "New York Jets", "logo": "https://media.api-sports.io/american-football/teams/13.png"},
				"won":        1,
				"lost":       2,
				"ties":       0,
				"points":     map[string]any{"for": 52, "against": 81, "difference": -29},
				"records":    map[string]any{"home": "0-2", "road": "1-0", "conference": "1-2", "division": "0-0"},
				"streak":     "L1",
			},
			map[string]any{
				"league":     map[string]any{"id": 1, "name": "NFL", "season": 2024, "logo": "https://media.api-sports.io/american-football/leagues/1.png", "country": map[string]any{"name": "USA", "code": "US", "flag": "https://media.api-sports.io/flags/us.svg"}},
				"conference": "American Football Conference",
				"division":   "East",
				"position":   4,
				"team":       map[string]any{"id": 3, "name": "New England Patriots", "logo": "https://media.api-sports.io/american-football/teams/3.png"},
				"won":        1,
				"lost":       2,
				"ties":       0,
				"points":     map[string]any{"for": 50, "against": 71, "difference": -21},
				"records":    map[string]any{"home": "0-1", "road": "1-1", "conference": "1-2", "division": "0-1"},
				"streak":     "L1",
			},
			// AFC North
			map[string]any{
				"league":     map[string]any{"id": 1, "name": "NFL", "season": 2024, "logo": "https://media.api-sports.io/american-football/leagues/1.png", "country": map[string]any{"name": "USA", "code": "US", "flag": "https://media.api-sports.io/flags/us.svg"}},
				"conference": "American Football Conference",
				"division":   "North",
				"position":   1,
				"team":       map[string]any{"id": 9, "name": "Cleveland Browns", "logo": "https://media.api-sports.io/american-football/teams/9.png"},
				"won":        2,
				"lost":       1,
				"ties":       0,
				"points":     map[string]any{"for": 85, "against": 72, "difference": 13},
				"records":    map[string]any{"home": "1-1", "road": "1-0", "conference": "1-1", "division": "1-0"},
				"streak":     "W1",
			},
			map[string]any{
				"league":     map[string]any{"id": 1, "name": "NFL", "season": 2024, "logo": "https://media.api-sports.io/american-football/leagues/1.png", "country": map[string]any{"name": "USA", "code": "US", "flag": "https://media.api-sports.io/flags/us.svg"}},
				"conference": "American Football Conference",
				"division":   "North",
				"position":   2,
				"team":       map[string]any{"id": 5, "name": "Baltimore Ravens", "logo": "https://media.api-sports.io/american-football/teams/5.png"},
				"won":        2,
				"lost":       1,
				"ties":       0,
				"points":     map[string]any{"for": 99, "against": 77, "difference": 22},
				"records":    map[string]any{"home": "0-1", "road": "2-0", "conference": "2-1", "division": "0-0"},
				"streak":     "W1",
			},
			map[string]any{
				"league":     map[string]any{"id": 1, "name": "NFL", "season": 2024, "logo": "https://media.api-sports.io/american-football/leagues/1.png", "country": map[string]any{"name": "USA", "code": "US", "flag": "https://media.api-sports.io/flags/us.svg"}},
				"conference": "American Football Conference",
				"division":   "North",
				"position":   3,
				"team":       map[string]any{"id": 10, "name": "Cincinnati Bengals", "logo": "https://media.api-sports.io/american-football/teams/10.png"},
				"won":        2,
				"lost":       2,
				"ties":       0,
				"points":     map[string]any{"for": 91, "against": 70, "difference": 21},
				"records":    map[string]any{"home": "1-1", "road": "1-1", "conference": "2-1", "division": "0-1"},
				"streak":     "W2",
			},
			map[string]any{
				"league":     map[string]any{"id": 1, "name": "NFL", "season": 2024, "logo": "https://media.api-sports.io/american-football/leagues/1.png", "country": map[string]any{"name": "USA", "code": "US", "flag": "https://media.api-sports.io/flags/us.svg"}},
				"conference": "American Football Conference",
				"division":   "North",
				"position":   4,
				"team":       map[string]any{"id": 22, "name": "Pittsburgh Steelers", "logo": "https://media.api-sports.io/american-football/teams/22.png"},
				"won":        1,
				"lost":       2,
				"ties":       0,
				"points":     map[string]any{"for": 54, "against": 66, "difference": -12},
				"records":    map[string]any{"home": "0-1", "road": "1-1", "conference": "1-2", "division": "1-1"},
				"streak":     "L2",
			},
			// AFC South
			map[string]any{
				"league":     map[string]any{"id": 1, "name": "NFL", "season": 2024, "logo": "https://media.api-sports.io/american-football/leagues/1.png", "country": map[string]any{"name": "USA", "code": "US", "flag": "https://media.api-sports.io/flags/us.svg"}},
				"conference": "American Football Conference",
				"division":   "South",
				"position":   1,
				"team":       map[string]any{"id": 2, "name": "Jacksonville Jaguars", "logo": "https://media.api-sports.io/american-football/teams/2.png"},
				"won":        2,
				"lost":       1,
				"ties":       0,
				"points":     map[string]any{"for": 84, "against": 38, "difference": 46},
				"records":    map[string]any{"home": "1-0", "road": "1-1", "conference": "2-0", "division": "1-0"},
				"streak":     "W2",
			},
			map[string]any{
				"league":     map[string]any{"id": 1, "name": "NFL", "season": 2024, "logo": "https://media.api-sports.io/american-football/leagues/1.png", "country": map[string]any{"name": "USA", "code": "US", "flag": "https://media.api-sports.io/flags/us.svg"}},
				"conference": "American Football Conference",
				"division":   "South",
				"position":   2,
				"team":       map[string]any{"id": 21, "name": "Indianapolis Colts", "logo": "https://media.api-sports.io/american-football/teams/21.png"},
				"won":        1,
				"lost":       1,
				"ties":       1,
				"points":     map[string]any{"for": 40, "against": 61, "difference": -21},
				"records":    map[string]any{"home": "1-0", "road": "0-1-1", "conference": "1-1-1", "division": "0-1-1"},
				"streak":     "W1",
			},
			map[string]any{
				"league":     map[string]any{"id": 1, "name": "NFL", "season": 2024, "logo": "https://media.api-sports.io/american-football/leagues/1.png", "country": map[string]any{"name": "USA", "code": "US", "flag": "https://media.api-sports.io/flags/us.svg"}},
				"conference": "American Football Conference",
				"division":   "South",
				"position":   3,
				"team":       map[string]any{"id": 6, "name": "Tennessee Titans", "logo": "https://media.api-sports.io/american-football/teams/6.png"},
				"won":        1,
				"lost":       2,
				"ties":       0,
				"points":     map[string]any{"for": 51, "against": 84, "difference": -33},
				"records":    map[string]any{"home": "1-1", "road": "0-1", "conference": "1-1", "division": "0-0"},
				"streak":     "W1",
			},
			map[string]any{
				"league":     map[string]any{"id": 1, "name": "NFL", "season": 2024, "logo": "https://media.api-sports.io/american-football/leagues/1.png", "country": map[string]any{"name": "USA", "code": "US", "flag": "https://media.api-sports.io/flags/us.svg"}},
				"conference": "American Football Conference",
				"division":   "South",
				"position":   4,
				"team":       map[string]any{"id": 26, "name": "Houston Texans", "logo": "https://media.api-sports.io/american-football/teams/26.png"},
				"won":        0,
				"lost":       2,
				"ties":       1,
				"points":     map[string]any{"for": 49, "against": 59, "difference": -10},
				"records":    map[string]any{"home": "0-0-1", "road": "0-2", "conference": "0-1-1", "division": "0-0-1"},
				"streak":     "L2",
			},
			// AFC West
			map[string]any{
				"league":     map[string]any{"id": 1, "name": "NFL", "season": 2024, "logo": "https://media.api-sports.io/american-football/leagues/1.png", "country": map[string]any{"name": "USA", "code": "US", "flag": "https://media.api-sports.io/flags/us.svg"}},
				"conference": "American Football Conference",
				"division":   "West",
				"position":   1,
				"team":       map[string]any{"id": 17, "name": "Kansas City Chiefs", "logo": "https://media.api-sports.io/american-football/teams/17.png"},
				"won":        2,
				"lost":       1,
				"ties":       0,
				"points":     map[string]any{"for": 88, "against": 65, "difference": 23},
				"records":    map[string]any{"home": "1-0", "road": "1-1", "conference": "1-1", "division": "1-0"},
				"streak":     "L1",
			},
			map[string]any{
				"league":     map[string]any{"id": 1, "name": "NFL", "season": 2024, "logo": "https://media.api-sports.io/american-football/leagues/1.png", "country": map[string]any{"name": "USA", "code": "US", "flag": "https://media.api-sports.io/flags/us.svg"}},
				"conference": "American Football Conference",
				"division":   "West",
				"position":   2,
				"team":       map[string]any{"id": 28, "name": "Denver Broncos", "logo": "https://media.api-sports.io/american-football/teams/28.png"},
				"won":        2,
				"lost":       1,
				"ties":       0,
				"points":     map[string]any{"for": 43, "against": 36, "difference": 7},
				"records":    map[string]any{"home": "2-0", "road": "0-1", "conference": "1-0", "division": "0-0"},
				"streak":     "W2",
			},
			map[string]any{
				"league":     map[string]any{"id": 1, "name": "NFL", "season": 2024, "logo": "https://media.api-sports.io/american-football/leagues/1.png", "country": map[string]any{"name": "USA", "code": "US", "flag": "https://media.api-sports.io/flags/us.svg"}},
				"conference": "American Football Conference",
				"division":   "West",
				"position":   3,
				"team":       map[string]any{"id": 30, "name": "Los Angeles Chargers", "logo": "https://media.api-sports.io/american-football/teams/30.png"},
				"won":        1,
				"lost":       2,
				"ties":       0,
				"points":     map[string]any{"for": 58, "against": 84, "difference": -26},
				"records":    map[string]any{"home": "1-1", "road": "0-1", "conference": "1-2", "division": "1-1"},
				"streak":     "L2",
			},
			map[string]any{
				"league":     map[string]any{"id": 1, "name": "NFL", "season": 2024, "logo": "https://media.api-sports.io/american-football/leagues/1.png", "country": map[string]any{"name": "USA", "code": "US", "flag": "https://media.api-sports.io/flags/us.svg"}},
				"conference": "American Football Conference",
				"division":   "West",
				"position":   4,
				"team":       map[string]any{"id": 1, "name": "Las Vegas Raiders", "logo": "https://media.api-sports.io/american-football/teams/1.png"},
				"won":        0,
				"lost":       3,
				"ties":       0,
				"points":     map[string]any{"for": 64, "against": 77, "difference": -13},
				"records":    map[string]any{"home": "0-1", "road": "0-2", "conference": "0-2", "division": "0-1"},
				"streak":     "L3",
			},
			// NFC East
			map[string]any{
				"league":     map[string]any{"id": 1, "name": "NFL", "season": 2024, "logo": "https://media.api-sports.io/american-football/leagues/1.png", "country": map[string]any{"name": "USA", "code": "US", "flag": "https://media.api-sports.io/flags/us.svg"}},
				"conference": "National Football Conference",
				"division":   "East",
				"position":   1,
				"team":       map[string]any{"id": 12, "name": "Philadelphia Eagles", "logo": "https://media.api-sports.io/american-football/teams/12.png"},
				"won":        3,
				"lost":       0,
				"ties":       0,
				"points":     map[string]any{"for": 86, "against": 50, "difference": 36},
				"records":    map[string]any{"home": "1-0", "road": "2-0", "conference": "3-0", "division": "1-0"},
				"streak":     "W3",
			},
			map[string]any{
				"league":     map[string]any{"id": 1, "name": "NFL", "season": 2024, "logo": "https://media.api-sports.io/american-football/leagues/1.png", "country": map[string]any{"name": "USA", "code": "US", "flag": "https://media.api-sports.io/flags/us.svg"}},
				"conference": "National Football Conference",
				"division":   "East",
				"position":   2,
				"team":       map[string]any{"id": 29, "name": "Dallas Cowboys", "logo": "https://media.api-sports.io/american-football/teams/29.png"},
				"won":        2,
				"lost":       1,
				"ties":       0,
				"points":     map[string]any{"for": 46, "against": 52, "difference": -6},
				"records":    map[string]any{"home": "1-1", "road": "1-0", "conference": "1-1", "division": "1-0"},
				"streak":     "W2",
			},
			map[string]any{
				"league":     map[string]any{"id": 1, "name": "NFL", "season": 2024, "logo": "https://media.api-sports.io/american-football/leagues/1.png", "country": map[string]any{"name": "USA", "code": "US", "flag": "https://media.api-sports.io/flags/us.svg"}},
				"conference": "National Football Conference",
				"division":   "East",
				"position":   3,
				"team":       map[string]any{"id": 4, "name": "New York Giants", "logo": "https://media.api-sports.io/american-football/teams/4.png"},
				"won":        2,
				"lost":       1,
				"ties":       0,
				"points":     map[string]any{"for": 56, "against": 59, "difference": -3},
				"records":    map[string]any{"home": "1-1", "road": "1-0", "conference": "1-1", "division": "0-1"},
				"streak":     "L1",
			},
			map[string]any{
				"league":     map[string]any{"id": 1, "name": "NFL", "season": 2024, "logo": "https://media.api-sports.io/american-football/leagues/1.png", "country": map[string]any{"name": "USA", "code": "US", "flag": "https://media.api-sports.io/flags/us.svg"}},
				"conference": "National Football Conference",
				"division":   "East",
				"position":   4,
				"team":       map[string]any{"id": 18, "name": "Washington Commanders", "logo": "https://media.api-sports.io/american-football/teams/18.png"},
				"won":        1,
				"lost":       2,
				"ties":       0,
				"points":     map[string]any{"for": 63, "against": 82, "difference": -19},
				"records":    map[string]any{"home": "1-1", "road": "0-1", "conference": "0-2", "division": "0-1"},
				"streak":     "L2",
			},
			// NFC North
			map[string]any{
				"league":     map[string]any{"id": 1, "name": "NFL", "season": 2024, "logo": "https://media.api-sports.io/american-football/leagues/1.png", "country": map[string]any{"name": "USA", "code": "US", "flag": "https://media.api-sports.io/flags/us.svg"}},
				"conference": "National Football Conference",
				"division":   "North",
				"position":   1,
				"team":       map[string]any{"id": 32, "name": "Minnesota Vikings", "logo": "https://media.api-sports.io/american-football/teams/32.png"},
				"won":        2,
				"lost":       1,
				"ties":       0,
				"points":     map[string]any{"for": 58, "against": 55, "difference": 3},
				"records":    map[string]any{"home": "2-0", "road": "0-1", "conference": "2-1", "division": "2-0"},
				"streak":     "W1",
			},
			map[string]any{
				"league":     map[string]any{"id": 1, "name": "NFL", "season": 2024, "logo": "https://media.api-sports.io/american-football/leagues/1.png", "country": map[string]any{"name": "USA", "code": "US", "flag": "https://media.api-sports.io/flags/us.svg"}},
				"conference": "National Football Conference",
				"division":   "North",
				"position":   2,
				"team":       map[string]any{"id": 15, "name": "Green Bay Packers", "logo": "https://media.api-sports.io/american-football/teams/15.png"},
				"won":        2,
				"lost":       1,
				"ties":       0,
				"points":     map[string]any{"for": 48, "against": 45, "difference": 3},
				"records":    map[string]any{"home": "1-0", "road": "1-1", "conference": "2-1", "division": "1-1"},
				"streak":     "W2",
			},
			map[string]any{
				"league":     map[string]any{"id": 1, "name": "NFL", "season": 2024, "logo": "https://media.api-sports.io/american-football/leagues/1.png", "country": map[string]any{"name": "USA", "code": "US", "flag": "https://media.api-sports.io/flags/us.svg"}},
				"conference": "National Football Conference",
				"division":   "North",
				"position":   3,
				"team":       map[string]any{"id": 16, "name": "Chicago Bears", "logo": "https://media.api-sports.io/american-football/teams/16.png"},
				"won":        2,
				"lost":       1,
				"ties":       0,
				"points":     map[string]any{"for": 52, "against": 57, "difference": -5},
				"records":    map[string]any{"home": "2-0", "road": "0-1", "conference": "1-1", "division": "0-1"},
				"streak":     "W1",
			},
			map[string]any{
				"league":     map[string]any{"id": 1, "name": "NFL", "season": 2024, "logo": "https://media.api-sports.io/american-football/leagues/1.png", "country": map[string]any{"name": "USA", "code": "US", "flag": "https://media.api-sports.io/flags/us.svg"}},
				"conference": "National Football Conference",
				"division":   "North",
				"position":   4,
				"team":       map[string]any{"id": 7, "name": "Detroit Lions", "logo": "https://media.api-sports.io/american-football/teams/7.png"},
				"won":        1,
				"lost":       2,
				"ties":       0,
				"points":     map[string]any{"for": 95, "against": 93, "difference": 2},
				"records":    map[string]any{"home": "1-1", "road": "0-1", "conference": "1-2", "division": "0-1"},
				"streak":     "L1",
			},
			// NFC South
			map[string]any{
				"league":     map[string]any{"id": 1, "name": "NFL", "season": 2024, "logo": "https://media.api-sports.io/american-football/leagues/1.png", "country": map[string]any{"name": "USA", "code": "US", "flag": "https://media.api-sports.io/flags/us.svg"}},
				"conference": "National Football Conference",
				"division":   "South",
				"position":   1,
				"team":       map[string]any{"id": 24, "name": "Tampa Bay Buccaneers", "logo": "https://media.api-sports.io/american-football/teams/24.png"},
				"won":        2,
				"lost":       1,
				"ties":       0,
				"points":     map[string]any{"for": 51, "against": 27, "difference": 24},
				"records":    map[string]any{"home": "0-1", "road": "2-0", "conference": "2-1", "division": "1-0"},
				"streak":     "L1",
			},
			map[string]any{
				"league":     map[string]any{"id": 1, "name": "NFL", "season": 2024, "logo": "https://media.api-sports.io/american-football/leagues/1.png", "country": map[string]any{"name": "USA", "code": "US", "flag": "https://media.api-sports.io/flags/us.svg"}},
				"conference": "National Football Conference",
				"division":   "South",
				"position":   2,
				"team":       map[string]any{"id": 19, "name": "Carolina Panthers", "logo": "https://media.api-sports.io/american-football/teams/19.png"},
				"won":        1,
				"lost":       2,
				"ties":       0,
				"points":     map[string]any{"for": 62, "against": 59, "difference": 3},
				"records":    map[string]any{"home": "1-1", "road": "0-1", "conference": "1-1", "division": "1-0"},
				"streak":     "W1",
			},
			map[string]any{
				"league":     map[string]any{"id": 1, "name": "NFL", "season": 2024, "logo": "https://media.api-sports.io/american-football/leagues/1.png", "country": map[string]any{"name": "USA", "code": "US", "flag": "https://media.api-sports.io/flags/us.svg"}},
				"conference": "National Football Conference",
				"division":   "South",
				"position":   3,
				"team":       map[string]any{"id": 27, "name": "New Orleans Saints", "logo": "https://media.api-sports.io/american-football/teams/27.png"},
				"won":        1,
				"lost":       2,
				"ties":       0,
				"points":     map[string]any{"for": 51, "against": 68, "difference": -17},
				"records":    map[string]any{"home": "0-1", "road": "1-1", "conference": "1-2", "division": "1-2"},
				"streak":     "L2",
			},
			map[string]any{
				"league":     map[string]any{"id": 1, "name": "NFL", "season": 2024, "logo": "https://media.api-sports.io/american-football/leagues/1.png", "country": map[string]any{"name": "USA", "code": "US", "flag": "https://media.api-sports.io/flags/us.svg"}},
				"conference": "National Football Conference",
				"division":   "South",
				"position":   4,
				"team":       map[string]any{"id": 8, "name": "Atlanta Falcons", "logo": "https://media.api-sports.io/american-football/teams/8.png"},
				"won":        1,
				"lost":       2,
				"ties":       0,
				"points":     map[string]any{"for": 80, "against": 81, "difference": -1},
				"records":    map[string]any{"home": "0-1", "road": "1-1", "conference": "1-2", "division": "0-1"},
				"streak":     "W1",
			},
			// NFC West
			map[string]any{
				"league":     map[string]any{"id": 1, "name": "NFL", "season": 2024, "logo": "https://media.api-sports.io/american-football/leagues/1.png", "country": map[string]any{"name": "USA", "code": "US", "flag": "https://media.api-sports.io/flags/us.svg"}},
				"conference": "National Football Conference",
				"division":   "West",
				"position":   1,
				"team":       map[string]any{"id": 31, "name": "Los Angeles Rams", "logo": "https://media.api-sports.io/american-football/teams/31.png"},
				"won":        2,
				"lost":       1,
				"ties":       0,
				"points":     map[string]any{"for": 61, "against": 70, "difference": -9},
				"records":    map[string]any{"home": "1-1", "road": "1-0", "conference": "2-0", "division": "1-0"},
				"streak":     "W2",
			},
			map[string]any{
				"league":     map[string]any{"id": 1, "name": "NFL", "season": 2024, "logo": "https://media.api-sports.io/american-football/leagues/1.png", "country": map[string]any{"name": "USA", "code": "US", "flag": "https://media.api-sports.io/flags/us.svg"}},
				"conference": "National Football Conference",
				"division":   "West",
				"position":   2,
				"team":       map[string]any{"id": 14, "name": "San Francisco 49ers", "logo": "https://media.api-sports.io/american-football/teams/14.png"},
				"won":        1,
				"lost":       2,
				"ties":       0,
				"points":     map[string]any{"for": 47, "against": 37, "difference": 10},
				"records":    map[string]any{"home": "1-0", "road": "0-2", "conference": "1-1", "division": "1-0"},
				"streak":     "L1",
			},
			map[string]any{
				"league":     map[string]any{"id": 1, "name": "NFL", "season": 2024, "logo": "https://media.api-sports.io/american-football/leagues/1.png", "country": map[string]any{"name": "USA", "code": "US", "flag": "https://media.api-sports.io/flags/us.svg"}},
				"conference": "National Football Conference",
				"division":   "West",
				"position":   3,
				"team":       map[string]any{"id": 23, "name": "Seattle Seahawks", "logo": "https://media.api-sports.io/american-football/teams/23.png"},
				"won":        1,
				"lost":       2,
				"ties":       0,
				"points":     map[string]any{"for": 47, "against": 70, "difference": -23},
				"records":    map[string]any{"home": "1-1", "road": "0-1", "conference": "0-2", "division": "0-1"},
				"streak":     "L2",
			},
			map[string]any{
				"league":     map[string]any{"id": 1, "name": "NFL", "season": 2024, "logo": "https://media.api-sports.io/american-football/leagues/1.png", "country": map[string]any{"name": "USA", "code": "US", "flag": "https://media.api-sports.io/flags/us.svg"}},
				"conference": "National Football Conference",
				"division":   "West",
				"position":   4,
				"team":       map[string]any{"id": 11, "name": "Arizona Cardinals", "logo": "https://media.api-sports.io/american-football/teams/11.png"},
				"won":        1,
				"lost":       2,
				"ties":       0,
				"points":     map[string]any{"for": 62, "against": 87, "difference": -25},
				"records":    map[string]any{"home": "0-2", "road": "1-0", "conference": "0-1", "division": "0-1"},
				"streak":     "L1",
			},
		},
	}
}

func footballStandingsResponse(endpoint string) map[string]any {
	return map[string]any{
		"get":        "standings",
		"parameters": map[string]any{"league": "39", "season": "2024"},
		"errors":     []any{},
		"results":    1,
		"paging":     map[string]int{"current": 1, "total": 1},
		"response": []any{
			map[string]any{
				"league": map[string]any{
					"id":      39,
					"name":    "Premier League",
					"country": "England",
					"logo":    "https://media.api-sports.io/football/leagues/2.png",
					"flag":    "https://media.api-sports.io/flags/gb.svg",
					"season":  2024,
					"standings": []any{
						[]any{
							// Top of table
							map[string]any{
								"rank":        1,
								"team":        map[string]any{"id": 40, "name": "Liverpool", "logo": "https://media.api-sports.io/football/teams/40.png"},
								"points":      42,
								"goalsDiff":   28,
								"group":       "Premier League",
								"form":        "WWWWW",
								"status":      "same",
								"description": "Promotion - Champions League (Group Stage)",
								"all":         map[string]any{"played": 18, "win": 13, "draw": 3, "lose": 2, "goals": map[string]any{"for": 42, "against": 14}},
								"home":        map[string]any{"played": 9, "win": 7, "draw": 2, "lose": 0, "goals": map[string]any{"for": 22, "against": 6}},
								"away":        map[string]any{"played": 9, "win": 6, "draw": 1, "lose": 2, "goals": map[string]any{"for": 20, "against": 8}},
								"update":      "2024-12-15T00:00:00+00:00",
							},
							map[string]any{
								"rank":        2,
								"team":        map[string]any{"id": 33, "name": "Arsenal", "logo": "https://media.api-sports.io/football/teams/33.png"},
								"points":      39,
								"goalsDiff":   22,
								"group":       "Premier League",
								"form":        "WWLWW",
								"status":      "same",
								"description": "Promotion - Champions League (Group Stage)",
								"all":         map[string]any{"played": 18, "win": 12, "draw": 3, "lose": 3, "goals": map[string]any{"for": 38, "against": 16}},
								"home":        map[string]any{"played": 9, "win": 7, "draw": 1, "lose": 1, "goals": map[string]any{"for": 22, "against": 8}},
								"away":        map[string]any{"played": 9, "win": 5, "draw": 2, "lose": 2, "goals": map[string]any{"for": 16, "against": 8}},
								"update":      "2024-12-15T00:00:00+00:00",
							},
							map[string]any{
								"rank":        3,
								"team":        map[string]any{"id": 34, "name": "Chelsea", "logo": "https://media.api-sports.io/football/teams/34.png"},
								"points":      36,
								"goalsDiff":   15,
								"group":       "Premier League",
								"form":        "WLWWW",
								"status":      "up",
								"description": "Promotion - Champions League (Group Stage)",
								"all":         map[string]any{"played": 18, "win": 11, "draw": 3, "lose": 4, "goals": map[string]any{"for": 35, "against": 20}},
								"home":        map[string]any{"played": 9, "win": 6, "draw": 2, "lose": 1, "goals": map[string]any{"for": 19, "against": 9}},
								"away":        map[string]any{"played": 9, "win": 5, "draw": 1, "lose": 3, "goals": map[string]any{"for": 16, "against": 11}},
								"update":      "2024-12-15T00:00:00+00:00",
							},
							map[string]any{
								"rank":        4,
								"team":        map[string]any{"id": 42, "name": "Manchester City", "logo": "https://media.api-sports.io/football/teams/42.png"},
								"points":      34,
								"goalsDiff":   18,
								"group":       "Premier League",
								"form":        "LWWLW",
								"status":      "down",
								"description": "Promotion - Champions League (Group Stage)",
								"all":         map[string]any{"played": 18, "win": 10, "draw": 4, "lose": 4, "goals": map[string]any{"for": 38, "against": 20}},
								"home":        map[string]any{"played": 9, "win": 6, "draw": 2, "lose": 1, "goals": map[string]any{"for": 22, "against": 10}},
								"away":        map[string]any{"played": 9, "win": 4, "draw": 2, "lose": 3, "goals": map[string]any{"for": 16, "against": 10}},
								"update":      "2024-12-15T00:00:00+00:00",
							},
							map[string]any{
								"rank":        5,
								"team":        map[string]any{"id": 39, "name": "Manchester United", "logo": "https://media.api-sports.io/football/teams/39.png"},
								"points":      28,
								"goalsDiff":   5,
								"group":       "Premier League",
								"form":        "WLWLL",
								"status":      "same",
								"description": "Promotion - Europa League (Group Stage)",
								"all":         map[string]any{"played": 18, "win": 8, "draw": 4, "lose": 6, "goals": map[string]any{"for": 25, "against": 20}},
								"home":        map[string]any{"played": 9, "win": 5, "draw": 2, "lose": 2, "goals": map[string]any{"for": 15, "against": 9}},
								"away":        map[string]any{"played": 9, "win": 3, "draw": 2, "lose": 4, "goals": map[string]any{"for": 10, "against": 11}},
								"update":      "2024-12-15T00:00:00+00:00",
							},
							map[string]any{
								"rank":        6,
								"team":        map[string]any{"id": 65, "name": "Newcastle", "logo": "https://media.api-sports.io/football/teams/65.png"},
								"points":      26,
								"goalsDiff":   8,
								"group":       "Premier League",
								"form":        "LWWWL",
								"status":      "up",
								"description": "Promotion - Europa Conference League (Qualification)",
								"all":         map[string]any{"played": 18, "win": 7, "draw": 5, "lose": 6, "goals": map[string]any{"for": 28, "against": 20}},
								"home":        map[string]any{"played": 9, "win": 5, "draw": 2, "lose": 2, "goals": map[string]any{"for": 17, "against": 10}},
								"away":        map[string]any{"played": 9, "win": 2, "draw": 3, "lose": 4, "goals": map[string]any{"for": 11, "against": 10}},
								"update":      "2024-12-15T00:00:00+00:00",
							},
							map[string]any{
								"rank":        10,
								"team":        map[string]any{"id": 51, "name": "Brighton", "logo": "https://media.api-sports.io/football/teams/51.png"},
								"points":      21,
								"goalsDiff":   -2,
								"group":       "Premier League",
								"form":        "LLWLW",
								"status":      "down",
								"description": nil,
								"all":         map[string]any{"played": 18, "win": 5, "draw": 6, "lose": 7, "goals": map[string]any{"for": 22, "against": 24}},
								"home":        map[string]any{"played": 9, "win": 3, "draw": 3, "lose": 3, "goals": map[string]any{"for": 12, "against": 11}},
								"away":        map[string]any{"played": 9, "win": 2, "draw": 3, "lose": 4, "goals": map[string]any{"for": 10, "against": 13}},
								"update":      "2024-12-15T00:00:00+00:00",
							},
							map[string]any{
								"rank":        15,
								"team":        map[string]any{"id": 62, "name": "Wolves", "logo": "https://media.api-sports.io/football/teams/62.png"},
								"points":      16,
								"goalsDiff":   -8,
								"group":       "Premier League",
								"form":        "LLDLL",
								"status":      "same",
								"description": nil,
								"all":         map[string]any{"played": 18, "win": 4, "draw": 4, "lose": 10, "goals": map[string]any{"for": 18, "against": 26}},
								"home":        map[string]any{"played": 9, "win": 3, "draw": 2, "lose": 4, "goals": map[string]any{"for": 11, "against": 12}},
								"away":        map[string]any{"played": 9, "win": 1, "draw": 2, "lose": 6, "goals": map[string]any{"for": 7, "against": 14}},
								"update":      "2024-12-15T00:00:00+00:00",
							},
							map[string]any{
								"rank":        18,
								"team":        map[string]any{"id": 63, "name": "Southampton", "logo": "https://media.api-sports.io/football/teams/63.png"},
								"points":      12,
								"goalsDiff":   -15,
								"group":       "Premier League",
								"form":        "LLLLW",
								"status":      "down",
								"description": "Relegation - Championship",
								"all":         map[string]any{"played": 18, "win": 3, "draw": 3, "lose": 12, "goals": map[string]any{"for": 15, "against": 30}},
								"home":        map[string]any{"played": 9, "win": 2, "draw": 2, "lose": 5, "goals": map[string]any{"for": 9, "against": 14}},
								"away":        map[string]any{"played": 9, "win": 1, "draw": 1, "lose": 7, "goals": map[string]any{"for": 6, "against": 16}},
								"update":      "2024-12-15T00:00:00+00:00",
							},
							map[string]any{
								"rank":        20,
								"team":        map[string]any{"id": 68, "name": "Leicester", "logo": "https://media.api-sports.io/football/teams/68.png"},
								"points":      10,
								"goalsDiff":   -20,
								"group":       "Premier League",
								"form":        "LLLLL",
								"status":      "same",
								"description": "Relegation - Championship",
								"all":         map[string]any{"played": 18, "win": 2, "draw": 4, "lose": 12, "goals": map[string]any{"for": 12, "against": 32}},
								"home":        map[string]any{"played": 9, "win": 2, "draw": 2, "lose": 5, "goals": map[string]any{"for": 8, "against": 14}},
								"away":        map[string]any{"played": 9, "win": 1, "draw": 1, "lose": 7, "goals": map[string]any{"for": 6, "against": 16}},
								"update":      "2024-12-15T00:00:00+00:00",
							},
						},
					},
				},
			},
		},
	}
}

func handballStandingsResponse(endpoint, league string) map[string]any {
	// Handball Bundesliga (German league - league_id 39)
	if league == "39" {
		return map[string]any{
			"get":        "standings",
			"parameters": map[string]any{"league": "39", "season": "2025"},
			"errors":     []any{},
			"results":    1,
			"response": []any{
				[]any{
					map[string]any{
						"position": 1, "stage": "Bundesliga - Regular Season", "group": map[string]any{"name": nil},
						"team":        map[string]any{"id": 328, "name": "SC Magdeburg", "logo": "https://media.api-sports.io/handball/teams/328.png"},
						"league":      map[string]any{"id": 39, "name": "Bundesliga", "type": "League", "logo": "https://media.api-sports.io/handball/leagues/39.png", "season": 2025},
						"country":     map[string]any{"id": 13, "name": "Germany", "code": "DE", "flag": "https://media.api-sports.io/flags/de.svg"},
						"games":       map[string]any{"played": 27, "win": map[string]any{"total": 24, "percentage": "88.89"}, "draw": map[string]any{"total": 2, "percentage": "7.41"}, "lose": map[string]any{"total": 1, "percentage": "3.70"}},
						"goals":       map[string]any{"for": 876, "against": 724},
						"points":      50,
						"form":        "WWWWW",
						"description": "Promotion - Champions League",
					},
					map[string]any{
						"position": 2, "stage": "Bundesliga - Regular Season", "group": map[string]any{"name": nil},
						"team":        map[string]any{"id": 314, "name": "Flensburg-H.", "logo": "https://media.api-sports.io/handball/teams/314.png"},
						"league":      map[string]any{"id": 39, "name": "Bundesliga", "type": "League", "logo": "https://media.api-sports.io/handball/leagues/39.png", "season": 2025},
						"country":     map[string]any{"id": 13, "name": "Germany", "code": "DE", "flag": "https://media.api-sports.io/flags/de.svg"},
						"games":       map[string]any{"played": 27, "win": map[string]any{"total": 20, "percentage": "74.07"}, "draw": map[string]any{"total": 3, "percentage": "11.11"}, "lose": map[string]any{"total": 4, "percentage": "14.81"}},
						"goals":       map[string]any{"for": 949, "against": 849},
						"points":      43,
						"form":        "WWLWW",
						"description": "Promotion - Champions League",
					},
					map[string]any{
						"position": 3, "stage": "Bundesliga - Regular Season", "group": map[string]any{"name": nil},
						"team":        map[string]any{"id": 315, "name": "Fuchse Berlin", "logo": "https://media.api-sports.io/handball/teams/315.png"},
						"league":      map[string]any{"id": 39, "name": "Bundesliga", "type": "League", "logo": "https://media.api-sports.io/handball/leagues/39.png", "season": 2025},
						"country":     map[string]any{"id": 13, "name": "Germany", "code": "DE", "flag": "https://media.api-sports.io/flags/de.svg"},
						"games":       map[string]any{"played": 27, "win": map[string]any{"total": 21, "percentage": "77.78"}, "draw": map[string]any{"total": 0, "percentage": "0.00"}, "lose": map[string]any{"total": 6, "percentage": "22.22"}},
						"goals":       map[string]any{"for": 971, "against": 809},
						"points":      42,
						"form":        "WLWWW",
						"description": "Promotion - European League",
					},
					map[string]any{
						"position": 4, "stage": "Bundesliga - Regular Season", "group": map[string]any{"name": nil},
						"team":        map[string]any{"id": 339, "name": "Gummersbach", "logo": "https://media.api-sports.io/handball/teams/339.png"},
						"league":      map[string]any{"id": 39, "name": "Bundesliga", "type": "League", "logo": "https://media.api-sports.io/handball/leagues/39.png", "season": 2025},
						"country":     map[string]any{"id": 13, "name": "Germany", "code": "DE", "flag": "https://media.api-sports.io/flags/de.svg"},
						"games":       map[string]any{"played": 26, "win": map[string]any{"total": 18, "percentage": "69.23"}, "draw": map[string]any{"total": 3, "percentage": "11.54"}, "lose": map[string]any{"total": 5, "percentage": "19.23"}},
						"goals":       map[string]any{"for": 822, "against": 721},
						"points":      39,
						"form":        "WWWWW",
						"description": "Promotion - European League",
					},
					map[string]any{
						"position": 5, "stage": "Bundesliga - Regular Season", "group": map[string]any{"name": nil},
						"team":        map[string]any{"id": 321, "name": "Kiel", "logo": "https://media.api-sports.io/handball/teams/321.png"},
						"league":      map[string]any{"id": 39, "name": "Bundesliga", "type": "League", "logo": "https://media.api-sports.io/handball/leagues/39.png", "season": 2025},
						"country":     map[string]any{"id": 13, "name": "Germany", "code": "DE", "flag": "https://media.api-sports.io/flags/de.svg"},
						"games":       map[string]any{"played": 27, "win": map[string]any{"total": 15, "percentage": "55.56"}, "draw": map[string]any{"total": 7, "percentage": "25.93"}, "lose": map[string]any{"total": 5, "percentage": "18.52"}},
						"goals":       map[string]any{"for": 835, "against": 796},
						"points":      37,
						"form":        "DLWLL",
						"description": "Promotion - European League",
					},
					map[string]any{
						"position": 6, "stage": "Bundesliga - Regular Season", "group": map[string]any{"name": nil},
						"team":    map[string]any{"id": 323, "name": "Lemgo", "logo": "https://media.api-sports.io/handball/teams/323.png"},
						"league":  map[string]any{"id": 39, "name": "Bundesliga", "type": "League", "logo": "https://media.api-sports.io/handball/leagues/39.png", "season": 2025},
						"country": map[string]any{"id": 13, "name": "Germany", "code": "DE", "flag": "https://media.api-sports.io/flags/de.svg"},
						"games":   map[string]any{"played": 27, "win": map[string]any{"total": 16, "percentage": "59.26"}, "draw": map[string]any{"total": 3, "percentage": "11.11"}, "lose": map[string]any{"total": 8, "percentage": "29.63"}},
						"goals":   map[string]any{"for": 798, "against": 750},
						"points":  35,
						"form":    "LDLLW",
					},
					map[string]any{
						"position": 7, "stage": "Bundesliga - Regular Season", "group": map[string]any{"name": nil},
						"team":    map[string]any{"id": 324, "name": "MT Melsungen", "logo": "https://media.api-sports.io/handball/teams/324.png"},
						"league":  map[string]any{"id": 39, "name": "Bundesliga", "type": "League", "logo": "https://media.api-sports.io/handball/leagues/39.png", "season": 2025},
						"country": map[string]any{"id": 13, "name": "Germany", "code": "DE", "flag": "https://media.api-sports.io/flags/de.svg"},
						"games":   map[string]any{"played": 28, "win": map[string]any{"total": 15, "percentage": "53.57"}, "draw": map[string]any{"total": 4, "percentage": "14.29"}, "lose": map[string]any{"total": 9, "percentage": "32.14"}},
						"goals":   map[string]any{"for": 820, "against": 798},
						"points":  34,
						"form":    "WWWLW",
					},
					map[string]any{
						"position": 8, "stage": "Bundesliga - Regular Season", "group": map[string]any{"name": nil},
						"team":    map[string]any{"id": 327, "name": "Rhein-Neckar", "logo": "https://media.api-sports.io/handball/teams/327.png"},
						"league":  map[string]any{"id": 39, "name": "Bundesliga", "type": "League", "logo": "https://media.api-sports.io/handball/leagues/39.png", "season": 2025},
						"country": map[string]any{"id": 13, "name": "Germany", "code": "DE", "flag": "https://media.api-sports.io/flags/de.svg"},
						"games":   map[string]any{"played": 27, "win": map[string]any{"total": 14, "percentage": "51.85"}, "draw": map[string]any{"total": 3, "percentage": "11.11"}, "lose": map[string]any{"total": 10, "percentage": "37.04"}},
						"goals":   map[string]any{"for": 813, "against": 776},
						"points":  31,
						"form":    "LWDWL",
					},
					map[string]any{
						"position": 9, "stage": "Bundesliga - Regular Season", "group": map[string]any{"name": nil},
						"team":    map[string]any{"id": 316, "name": "Goppingen", "logo": "https://media.api-sports.io/handball/teams/316.png"},
						"league":  map[string]any{"id": 39, "name": "Bundesliga", "type": "League", "logo": "https://media.api-sports.io/handball/leagues/39.png", "season": 2025},
						"country": map[string]any{"id": 13, "name": "Germany", "code": "DE", "flag": "https://media.api-sports.io/flags/de.svg"},
						"games":   map[string]any{"played": 28, "win": map[string]any{"total": 10, "percentage": "35.71"}, "draw": map[string]any{"total": 6, "percentage": "21.43"}, "lose": map[string]any{"total": 12, "percentage": "42.86"}},
						"goals":   map[string]any{"for": 772, "against": 816},
						"points":  26,
						"form":    "WLWWW",
					},
					map[string]any{
						"position": 10, "stage": "Bundesliga - Regular Season", "group": map[string]any{"name": nil},
						"team":    map[string]any{"id": 320, "name": "Hannover-Burgdorf", "logo": "https://media.api-sports.io/handball/teams/320.png"},
						"league":  map[string]any{"id": 39, "name": "Bundesliga", "type": "League", "logo": "https://media.api-sports.io/handball/leagues/39.png", "season": 2025},
						"country": map[string]any{"id": 13, "name": "Germany", "code": "DE", "flag": "https://media.api-sports.io/flags/de.svg"},
						"games":   map[string]any{"played": 28, "win": map[string]any{"total": 10, "percentage": "35.71"}, "draw": map[string]any{"total": 4, "percentage": "14.29"}, "lose": map[string]any{"total": 14, "percentage": "50.00"}},
						"goals":   map[string]any{"for": 830, "against": 843},
						"points":  24,
						"form":    "WLLLL",
					},
					map[string]any{
						"position": 11, "stage": "Bundesliga - Regular Season", "group": map[string]any{"name": nil},
						"team":    map[string]any{"id": 319, "name": "Hamburg", "logo": "https://media.api-sports.io/handball/teams/319.png"},
						"league":  map[string]any{"id": 39, "name": "Bundesliga", "type": "League", "logo": "https://media.api-sports.io/handball/leagues/39.png", "season": 2025},
						"country": map[string]any{"id": 13, "name": "Germany", "code": "DE", "flag": "https://media.api-sports.io/flags/de.svg"},
						"games":   map[string]any{"played": 27, "win": map[string]any{"total": 11, "percentage": "40.74"}, "draw": map[string]any{"total": 1, "percentage": "3.70"}, "lose": map[string]any{"total": 15, "percentage": "55.56"}},
						"goals":   map[string]any{"for": 847, "against": 863},
						"points":  23,
						"form":    "WLWLW",
					},
					map[string]any{
						"position": 12, "stage": "Bundesliga - Regular Season", "group": map[string]any{"name": nil},
						"team":    map[string]any{"id": 329, "name": "Stuttgart", "logo": "https://media.api-sports.io/handball/teams/329.png"},
						"league":  map[string]any{"id": 39, "name": "Bundesliga", "type": "League", "logo": "https://media.api-sports.io/handball/leagues/39.png", "season": 2025},
						"country": map[string]any{"id": 13, "name": "Germany", "code": "DE", "flag": "https://media.api-sports.io/flags/de.svg"},
						"games":   map[string]any{"played": 27, "win": map[string]any{"total": 7, "percentage": "25.93"}, "draw": map[string]any{"total": 7, "percentage": "25.93"}, "lose": map[string]any{"total": 13, "percentage": "48.15"}},
						"goals":   map[string]any{"for": 803, "against": 835},
						"points":  21,
						"form":    "DDDWD",
					},
					map[string]any{
						"position": 13, "stage": "Bundesliga - Regular Season", "group": map[string]any{"name": nil},
						"team":    map[string]any{"id": 313, "name": "Erlangen", "logo": "https://media.api-sports.io/handball/teams/313.png"},
						"league":  map[string]any{"id": 39, "name": "Bundesliga", "type": "League", "logo": "https://media.api-sports.io/handball/leagues/39.png", "season": 2025},
						"country": map[string]any{"id": 13, "name": "Germany", "code": "DE", "flag": "https://media.api-sports.io/flags/de.svg"},
						"games":   map[string]any{"played": 27, "win": map[string]any{"total": 7, "percentage": "25.93"}, "draw": map[string]any{"total": 5, "percentage": "18.52"}, "lose": map[string]any{"total": 15, "percentage": "55.56"}},
						"goals":   map[string]any{"for": 762, "against": 808},
						"points":  19,
						"form":    "WLLWD",
					},
					map[string]any{
						"position": 14, "stage": "Bundesliga - Regular Season", "group": map[string]any{"name": nil},
						"team":    map[string]any{"id": 335, "name": "Eisenach", "logo": "https://media.api-sports.io/handball/teams/335.png"},
						"league":  map[string]any{"id": 39, "name": "Bundesliga", "type": "League", "logo": "https://media.api-sports.io/handball/leagues/39.png", "season": 2025},
						"country": map[string]any{"id": 13, "name": "Germany", "code": "DE", "flag": "https://media.api-sports.io/flags/de.svg"},
						"games":   map[string]any{"played": 28, "win": map[string]any{"total": 7, "percentage": "25.00"}, "draw": map[string]any{"total": 5, "percentage": "17.86"}, "lose": map[string]any{"total": 16, "percentage": "57.14"}},
						"goals":   map[string]any{"for": 781, "against": 837},
						"points":  19,
						"form":    "LDDDL",
					},
					map[string]any{
						"position": 15, "stage": "Bundesliga - Regular Season", "group": map[string]any{"name": nil},
						"team":    map[string]any{"id": 312, "name": "Bergischer", "logo": "https://media.api-sports.io/handball/teams/312.png"},
						"league":  map[string]any{"id": 39, "name": "Bundesliga", "type": "League", "logo": "https://media.api-sports.io/handball/leagues/39.png", "season": 2025},
						"country": map[string]any{"id": 13, "name": "Germany", "code": "DE", "flag": "https://media.api-sports.io/flags/de.svg"},
						"games":   map[string]any{"played": 27, "win": map[string]any{"total": 6, "percentage": "22.22"}, "draw": map[string]any{"total": 2, "percentage": "7.41"}, "lose": map[string]any{"total": 19, "percentage": "70.37"}},
						"goals":   map[string]any{"for": 784, "against": 870},
						"points":  14,
						"form":    "LWLLW",
					},
					map[string]any{
						"position": 16, "stage": "Bundesliga - Regular Season", "group": map[string]any{"name": nil},
						"team":    map[string]any{"id": 325, "name": "Minden", "logo": "https://media.api-sports.io/handball/teams/325.png"},
						"league":  map[string]any{"id": 39, "name": "Bundesliga", "type": "League", "logo": "https://media.api-sports.io/handball/leagues/39.png", "season": 2025},
						"country": map[string]any{"id": 13, "name": "Germany", "code": "DE", "flag": "https://media.api-sports.io/flags/de.svg"},
						"games":   map[string]any{"played": 28, "win": map[string]any{"total": 4, "percentage": "14.29"}, "draw": map[string]any{"total": 4, "percentage": "14.29"}, "lose": map[string]any{"total": 20, "percentage": "71.43"}},
						"goals":   map[string]any{"for": 772, "against": 912},
						"points":  12,
						"form":    "LLLLL",
					},
					map[string]any{
						"position": 17, "stage": "Bundesliga - Regular Season", "group": map[string]any{"name": nil},
						"team":        map[string]any{"id": 318, "name": "HSG Wetzlar", "logo": "https://media.api-sports.io/handball/teams/318.png"},
						"league":      map[string]any{"id": 39, "name": "Bundesliga", "type": "League", "logo": "https://media.api-sports.io/handball/leagues/39.png", "season": 2025},
						"country":     map[string]any{"id": 13, "name": "Germany", "code": "DE", "flag": "https://media.api-sports.io/flags/de.svg"},
						"games":       map[string]any{"played": 27, "win": map[string]any{"total": 4, "percentage": "14.81"}, "draw": map[string]any{"total": 3, "percentage": "11.11"}, "lose": map[string]any{"total": 20, "percentage": "74.07"}},
						"goals":       map[string]any{"for": 758, "against": 863},
						"points":      11,
						"form":        "DLWLL",
						"description": "Relegation - 2. Bundesliga",
					},
					map[string]any{
						"position": 18, "stage": "Bundesliga - Regular Season", "group": map[string]any{"name": nil},
						"team":        map[string]any{"id": 322, "name": "Leipzig", "logo": "https://media.api-sports.io/handball/teams/322.png"},
						"league":      map[string]any{"id": 39, "name": "Bundesliga", "type": "League", "logo": "https://media.api-sports.io/handball/leagues/39.png", "season": 2025},
						"country":     map[string]any{"id": 13, "name": "Germany", "code": "DE", "flag": "https://media.api-sports.io/flags/de.svg"},
						"games":       map[string]any{"played": 27, "win": map[string]any{"total": 2, "percentage": "7.41"}, "draw": map[string]any{"total": 6, "percentage": "22.22"}, "lose": map[string]any{"total": 19, "percentage": "70.37"}},
						"goals":       map[string]any{"for": 739, "against": 862},
						"points":      10,
						"form":        "LLDDL",
						"description": "Relegation - 2. Bundesliga",
					},
				},
			},
		}
	}

	// Champions League (league_id 131) - use same structure as Bundesliga
	if league == "131" {
		return map[string]any{
			"get":        "standings",
			"parameters": map[string]any{"league": "131", "season": "2025"},
			"errors":     []any{},
			"results":    1,
			"response": []any{
				[]any{
					map[string]any{
						"position": 1,
						"stage":    "Champions League - Regular Season",
						"group":    map[string]any{"name": "Group A"},
						"team":     map[string]any{"id": 315, "name": "Fuchse Berlin", "logo": "https://media.api-sports.io/handball/teams/315.png"},
						"league":   map[string]any{"id": 131, "name": "Champions League", "type": "cup", "logo": "https://media.api-sports.io/handball/leagues/131.png", "season": 2025},
						"country":  map[string]any{"id": 40, "name": "Europe", "code": nil},
						"games":    map[string]any{"played": 14, "win": map[string]any{"total": 11, "percentage": "78.57"}, "draw": map[string]any{"total": 0}, "lose": map[string]any{"total": 3}},
						"goals":    map[string]any{"for": 470, "against": 433},
						"points":   22,
						"form":     "LWLWL",
					},
					map[string]any{
						"position": 2,
						"stage":    "Champions League - Regular Season",
						"group":    map[string]any{"name": "Group A"},
						"team":     map[string]any{"id": 172, "name": "Aalborg", "logo": "https://media.api-sports.io/handball/teams/172.png"},
						"league":   map[string]any{"id": 131, "name": "Champions League", "type": "cup"},
						"games":    map[string]any{"played": 14, "win": map[string]any{"total": 10}, "draw": map[string]any{"total": 1}, "lose": map[string]any{"total": 3}},
						"goals":    map[string]any{"for": 457, "against": 407},
						"points":   21,
					},
					map[string]any{
						"position": 3, "group": map[string]any{"name": "Group A"},
						"team":   map[string]any{"id": 4252, "name": "Kielce"},
						"games":  map[string]any{"played": 14, "win": map[string]any{"total": 8}, "draw": map[string]any{"total": 1}, "lose": map[string]any{"total": 5}},
						"goals":  map[string]any{"for": 456, "against": 451},
						"points": 17,
					},
					map[string]any{
						"position": 4, "group": map[string]any{"name": "Group A"},
						"team":   map[string]any{"id": 257, "name": "Nantes"},
						"games":  map[string]any{"played": 14, "win": map[string]any{"total": 8}, "lose": map[string]any{"total": 6}},
						"goals":  map[string]any{"for": 456, "against": 416},
						"points": 16,
					},
					map[string]any{
						"position": 5, "group": map[string]any{"name": "Group A"},
						"team":   map[string]any{"id": 4616, "name": "Veszprem"},
						"games":  map[string]any{"played": 14, "win": map[string]any{"total": 7}, "lose": map[string]any{"total": 7}},
						"goals":  map[string]any{"for": 471, "against": 449},
						"points": 14,
					},
					map[string]any{
						"position": 6, "group": map[string]any{"name": "Group A"},
						"team":   map[string]any{"id": 791, "name": "Sporting"},
						"games":  map[string]any{"played": 14, "win": map[string]any{"total": 7}, "lose": map[string]any{"total": 7}},
						"goals":  map[string]any{"for": 465, "against": 476},
						"points": 14,
					},
					map[string]any{
						"position": 7, "group": map[string]any{"name": "Group A"},
						"team":   map[string]any{"id": 829, "name": "Din. Bucuresti"},
						"games":  map[string]any{"played": 14, "win": map[string]any{"total": 2}, "lose": map[string]any{"total": 12}},
						"goals":  map[string]any{"for": 395, "against": 430},
						"points": 4,
					},
					map[string]any{
						"position": 8, "group": map[string]any{"name": "Group A"},
						"team":   map[string]any{"id": 639, "name": "Kolstad"},
						"games":  map[string]any{"played": 14, "win": map[string]any{"total": 2}, "lose": map[string]any{"total": 12}},
						"goals":  map[string]any{"for": 386, "against": 494},
						"points": 4,
					},
					map[string]any{
						"position": 1,
						"stage":    "Champions League - Regular Season",
						"group":    map[string]any{"name": "Group B"},
						"team":     map[string]any{"id": 962, "name": "Barcelona"},
						"games":    map[string]any{"played": 14, "win": map[string]any{"total": 13}, "lose": map[string]any{"total": 1}},
						"goals":    map[string]any{"for": 492, "against": 382},
						"points":   26,
					},
					map[string]any{
						"position": 2, "group": map[string]any{"name": "Group B"},
						"team":   map[string]any{"id": 328, "name": "SC Magdeburg"},
						"games":  map[string]any{"played": 14, "win": map[string]any{"total": 11}, "draw": map[string]any{"total": 1}, "lose": map[string]any{"total": 2}},
						"goals":  map[string]any{"for": 457, "against": 408},
						"points": 23,
					},
					map[string]any{
						"position": 3, "group": map[string]any{"name": "Group B"},
						"team":   map[string]any{"id": 694, "name": "Wisla Plock"},
						"games":  map[string]any{"played": 14, "win": map[string]any{"total": 8}, "draw": map[string]any{"total": 2}, "lose": map[string]any{"total": 4}},
						"goals":  map[string]any{"for": 424, "against": 410},
						"points": 18,
					},
					map[string]any{
						"position": 4, "group": map[string]any{"name": "Group B"},
						"team":   map[string]any{"id": 4484, "name": "PSG"},
						"games":  map[string]any{"played": 14, "win": map[string]any{"total": 6}, "draw": map[string]any{"total": 1}, "lose": map[string]any{"total": 7}},
						"goals":  map[string]any{"for": 446, "against": 436},
						"points": 13,
					},
					map[string]any{
						"position": 5, "group": map[string]any{"name": "Group B"},
						"team":   map[string]any{"id": 178, "name": "GOG"},
						"games":  map[string]any{"played": 14, "win": map[string]any{"total": 6}, "draw": map[string]any{"total": 1}, "lose": map[string]any{"total": 7}},
						"goals":  map[string]any{"for": 443, "against": 468},
						"points": 13,
					},
					map[string]any{
						"position": 6, "group": map[string]any{"name": "Group B"},
						"team":   map[string]any{"id": 417, "name": "Szeged"},
						"games":  map[string]any{"played": 14, "win": map[string]any{"total": 5}, "draw": map[string]any{"total": 1}, "lose": map[string]any{"total": 8}},
						"goals":  map[string]any{"for": 428, "against": 424},
						"points": 11,
					},
					map[string]any{
						"position": 7, "group": map[string]any{"name": "Group B"},
						"team":   map[string]any{"id": 1157, "name": "Eurofarm Pelister"},
						"games":  map[string]any{"played": 14, "win": map[string]any{"total": 2}, "draw": map[string]any{"total": 2}, "lose": map[string]any{"total": 10}},
						"goals":  map[string]any{"for": 369, "against": 447},
						"points": 6,
					},
					map[string]any{
						"position": 8, "group": map[string]any{"name": "Group B"},
						"team":   map[string]any{"id": 4445, "name": "RK Zagreb"},
						"games":  map[string]any{"played": 14, "win": map[string]any{"total": 1}, "lose": map[string]any{"total": 13}},
						"goals":  map[string]any{"for": 375, "against": 459},
						"points": 2,
					},
				},
			},
		}
	}

	// Starligue (league_id 34)
	if league == "34" {
		return map[string]any{
			"get":        "standings",
			"parameters": map[string]any{"league": "34", "season": "2025"},
			"errors":     []any{},
			"results":    1,
			"response": []any{
				[]any{
					map[string]any{
						"position": 1,
						"stage":    "Starligue - Regular Season",
						"team":     map[string]any{"id": 4484, "name": "PSG"},
						"league":   map[string]any{"id": 34, "name": "Starligue", "type": "League"},
						"country":  map[string]any{"id": 11, "name": "France", "code": "FR"},
						"games":    map[string]any{"played": 22, "win": map[string]any{"total": 21}, "draw": map[string]any{"total": 1}, "lose": map[string]any{"total": 0}},
						"goals":    map[string]any{"for": 767, "against": 615},
						"points":   43,
					},
					map[string]any{
						"position": 2,
						"team":     map[string]any{"id": 257, "name": "Nantes"},
						"games":    map[string]any{"played": 22, "win": map[string]any{"total": 19}, "draw": map[string]any{"total": 3}},
						"goals":    map[string]any{"for": 794, "against": 660},
						"points":   41,
					},
					map[string]any{
						"position": 3,
						"team":     map[string]any{"id": 248, "name": "Montpellier"},
						"games":    map[string]any{"played": 22, "win": map[string]any{"total": 16}, "draw": map[string]any{"total": 1}, "lose": map[string]any{"total": 5}},
						"goals":    map[string]any{"for": 746, "against": 656},
						"points":   33,
					},
					map[string]any{
						"position": 4,
						"team":     map[string]any{"id": 256, "name": "Limoges"},
						"games":    map[string]any{"played": 23, "win": map[string]any{"total": 15}, "draw": map[string]any{"total": 2}, "lose": map[string]any{"total": 6}},
						"goals":    map[string]any{"for": 758, "against": 723},
						"points":   32,
					},
					map[string]any{
						"position": 5,
						"team":     map[string]any{"id": 251, "name": "Chambery Savoie"},
						"games":    map[string]any{"played": 23, "win": map[string]any{"total": 12}, "draw": map[string]any{"total": 4}, "lose": map[string]any{"total": 7}},
						"goals":    map[string]any{"for": 738, "against": 723},
						"points":   28,
					},
					map[string]any{
						"position": 6,
						"team":     map[string]any{"id": 261, "name": "Tremblay"},
						"games":    map[string]any{"played": 23, "win": map[string]any{"total": 11}, "draw": map[string]any{"total": 2}, "lose": map[string]any{"total": 10}},
						"goals":    map[string]any{"for": 742, "against": 740},
						"points":   24,
					},
					map[string]any{
						"position": 7,
						"team":     map[string]any{"id": 259, "name": "St. Raphael"},
						"games":    map[string]any{"played": 22, "win": map[string]any{"total": 11}, "draw": map[string]any{"total": 1}, "lose": map[string]any{"total": 10}},
						"goals":    map[string]any{"for": 688, "against": 708},
						"points":   23,
					},
					map[string]any{
						"position": 8,
						"team":     map[string]any{"id": 4858, "name": "Provence Aix"},
						"games":    map[string]any{"played": 23, "win": map[string]any{"total": 10}, "draw": map[string]any{"total": 3}, "lose": map[string]any{"total": 10}},
						"goals":    map[string]any{"for": 685, "against": 671},
						"points":   23,
					},
					map[string]any{
						"position": 9,
						"team":     map[string]any{"id": 260, "name": "Toulouse"},
						"games":    map[string]any{"played": 22, "win": map[string]any{"total": 11}, "draw": map[string]any{"total": 1}, "lose": map[string]any{"total": 10}},
						"goals":    map[string]any{"for": 662, "against": 659},
						"points":   23,
					},
					map[string]any{
						"position": 10,
						"team":     map[string]any{"id": 242, "name": "Cesson Rennes-Metropole"},
						"games":    map[string]any{"played": 22, "win": map[string]any{"total": 9}, "draw": map[string]any{"total": 1}, "lose": map[string]any{"total": 12}},
						"goals":    map[string]any{"for": 687, "against": 696},
						"points":   19,
					},
					map[string]any{
						"position": 11,
						"team":     map[string]any{"id": 258, "name": "Nimes"},
						"games":    map[string]any{"played": 23, "win": map[string]any{"total": 8}, "draw": map[string]any{"total": 1}, "lose": map[string]any{"total": 14}},
						"goals":    map[string]any{"for": 676, "against": 708},
						"points":   17,
					},
					map[string]any{
						"position": 12,
						"team":     map[string]any{"id": 254, "name": "Dunkerque"},
						"games":    map[string]any{"played": 22, "win": map[string]any{"total": 5}, "draw": map[string]any{"total": 2}, "lose": map[string]any{"total": 15}},
						"goals":    map[string]any{"for": 593, "against": 674},
						"points":   12,
					},
					map[string]any{
						"position": 13,
						"team":     map[string]any{"id": 252, "name": "Chartres"},
						"games":    map[string]any{"played": 22, "win": map[string]any{"total": 5}, "draw": map[string]any{"total": 2}, "lose": map[string]any{"total": 15}},
						"goals":    map[string]any{"for": 630, "against": 704},
						"points":   12,
					},
					map[string]any{
						"position": 14,
						"team":     map[string]any{"id": 243, "name": "Istres"},
						"games":    map[string]any{"played": 23, "win": map[string]any{"total": 5}, "draw": map[string]any{"total": 1}, "lose": map[string]any{"total": 17}},
						"goals":    map[string]any{"for": 642, "against": 738},
						"points":   11,
					},
					map[string]any{
						"position": 15,
						"team":     map[string]any{"id": 288, "name": "Selestat"},
						"games":    map[string]any{"played": 23, "win": map[string]any{"total": 4}, "draw": map[string]any{"total": 2}, "lose": map[string]any{"total": 17}},
						"goals":    map[string]any{"for": 688, "against": 747},
						"points":   10,
					},
					map[string]any{
						"position": 16,
						"team":     map[string]any{"id": 283, "name": "Dijon"},
						"games":    map[string]any{"played": 23, "win": map[string]any{"total": 4}, "draw": map[string]any{"total": 1}, "lose": map[string]any{"total": 18}},
						"goals":    map[string]any{"for": 667, "against": 741},
						"points":   9,
					},
				},
			},
		}
	}

	// Default: WHA Women (Austrian league) for other leagues
	return map[string]any{
		"get":        "standings",
		"parameters": map[string]any{"league": "1", "season": "2024"},
		"errors":     []any{},
		"results":    1,
		"response": []any{
			[]any{
				map[string]any{
					"position": 1,
					"stage":    "WHA Women",
					"group":    map[string]any{"name": "Regular Season"},
					"team":     map[string]any{"id": 8, "name": "Hypo NO W", "logo": "https://media.api-sports.io/handball/teams/8.png"},
					"league":   map[string]any{"id": 1, "name": "WHA Women", "type": "League", "logo": "https://media.api-sports.io/handball/leagues/1.png", "season": 2024},
					"country":  map[string]any{"id": 1, "name": "Austria", "code": "AT", "flag": "https://media.api-sports.io/flags/at.svg"},
					"games":    map[string]any{"played": 15, "win": map[string]any{"total": 12, "percentage": "0.80"}, "draw": map[string]any{"total": 2, "percentage": "0.13"}, "lose": map[string]any{"total": 1, "percentage": "0.07"}},
					"goals":    map[string]any{"for": 420, "against": 280},
					"points":   38,
					"form":     "WWW-W",
				},
				map[string]any{
					"position": 2,
					"stage":    "WHA Women",
					"group":    map[string]any{"name": "Regular Season"},
					"team":     map[string]any{"id": 12, "name": "Wr. Neustadt W", "logo": "https://media.api-sports.io/handball/teams/12.png"},
					"league":   map[string]any{"id": 1, "name": "WHA Women", "type": "League", "logo": "https://media.api-sports.io/handball/leagues/1.png", "season": 2024},
					"country":  map[string]any{"id": 1, "name": "Austria", "code": "AT", "flag": "https://media.api-sports.io/flags/at.svg"},
					"games":    map[string]any{"played": 15, "win": map[string]any{"total": 10, "percentage": "0.67"}, "draw": map[string]any{"total": 3, "percentage": "0.20"}, "lose": map[string]any{"total": 2, "percentage": "0.13"}},
					"goals":    map[string]any{"for": 385, "against": 310},
					"points":   33,
					"form":     "WWDWW",
				},
				map[string]any{
					"position": 3,
					"stage":    "WHA Women",
					"group":    map[string]any{"name": "Regular Season"},
					"team":     map[string]any{"id": 4, "name": "Feldkirch W", "logo": "https://media.api-sports.io/handball/teams/4.png"},
					"league":   map[string]any{"id": 1, "name": "WHA Women", "type": "League", "logo": "https://media.api-sports.io/handball/leagues/1.png", "season": 2024},
					"country":  map[string]any{"id": 1, "name": "Austria", "code": "AT", "flag": "https://media.api-sports.io/flags/at.svg"},
					"games":    map[string]any{"played": 15, "win": map[string]any{"total": 9, "percentage": "0.60"}, "draw": map[string]any{"total": 2, "percentage": "0.13"}, "lose": map[string]any{"total": 4, "percentage": "0.27"}},
					"goals":    map[string]any{"for": 365, "against": 320},
					"points":   29,
					"form":     "LWWLW",
				},
				map[string]any{
					"position": 4,
					"stage":    "WHA Women",
					"group":    map[string]any{"name": "Regular Season"},
					"team":     map[string]any{"id": 2, "name": "Dornbirn/Schoren W", "logo": "https://media.api-sports.io/handball/teams/2.png"},
					"league":   map[string]any{"id": 1, "name": "WHA Women", "type": "League", "logo": "https://media.api-sports.io/handball/leagues/1.png", "season": 2024},
					"country":  map[string]any{"id": 1, "name": "Austria", "code": "AT", "flag": "https://media.api-sports.io/flags/at.svg"},
					"games":    map[string]any{"played": 15, "win": map[string]any{"total": 8, "percentage": "0.53"}, "draw": map[string]any{"total": 2, "percentage": "0.13"}, "lose": map[string]any{"total": 5, "percentage": "0.33"}},
					"goals":    map[string]any{"for": 340, "against": 315},
					"points":   26,
					"form":     "WLLWW",
				},
				map[string]any{
					"position": 5,
					"stage":    "WHA Women",
					"group":    map[string]any{"name": "Regular Season"},
					"team":     map[string]any{"id": 3, "name": "Eggenburg W", "logo": "https://media.api-sports.io/handball/teams/3.png"},
					"league":   map[string]any{"id": 1, "name": "WHA Women", "type": "League", "logo": "https://media.api-sports.io/handball/leagues/1.png", "season": 2024},
					"country":  map[string]any{"id": 1, "name": "Austria", "code": "AT", "flag": "https://media.api-sports.io/flags/at.svg"},
					"games":    map[string]any{"played": 15, "win": map[string]any{"total": 7, "percentage": "0.47"}, "draw": map[string]any{"total": 3, "percentage": "0.20"}, "lose": map[string]any{"total": 5, "percentage": "0.33"}},
					"goals":    map[string]any{"for": 310, "against": 295},
					"points":   24,
					"form":     "DWLWD",
				},
				map[string]any{
					"position": 6,
					"stage":    "WHA Women",
					"group":    map[string]any{"name": "Regular Season"},
					"team":     map[string]any{"id": 1, "name": "Atzgersdorf W", "logo": "https://media.api-sports.io/handball/teams/1.png"},
					"league":   map[string]any{"id": 1, "name": "WHA Women", "type": "League", "logo": "https://media.api-sports.io/handball/leagues/1.png", "season": 2024},
					"country":  map[string]any{"id": 1, "name": "Austria", "code": "AT", "flag": "https://media.api-sports.io/flags/at.svg"},
					"games":    map[string]any{"played": 15, "win": map[string]any{"total": 6, "percentage": "0.40"}, "draw": map[string]any{"total": 2, "percentage": "0.13"}, "lose": map[string]any{"total": 7, "percentage": "0.47"}},
					"goals":    map[string]any{"for": 285, "against": 330},
					"points":   20,
					"form":     "LLWLL",
				},
				map[string]any{
					"position": 7,
					"stage":    "WHA Women",
					"group":    map[string]any{"name": "Regular Season"},
					"team":     map[string]any{"id": 10, "name": "MGA Handball W", "logo": "https://media.api-sports.io/handball/teams/10.png"},
					"league":   map[string]any{"id": 1, "name": "WHA Women", "type": "League", "logo": "https://media.api-sports.io/handball/leagues/1.png", "season": 2024},
					"country":  map[string]any{"id": 1, "name": "Austria", "code": "AT", "flag": "https://media.api-sports.io/flags/at.svg"},
					"games":    map[string]any{"played": 15, "win": map[string]any{"total": 5, "percentage": "0.33"}, "draw": map[string]any{"total": 3, "percentage": "0.20"}, "lose": map[string]any{"total": 7, "percentage": "0.47"}},
					"goals":    map[string]any{"for": 270, "against": 340},
					"points":   18,
					"form":     "LWLLD",
				},
				map[string]any{
					"position": 8,
					"stage":    "WHA Women",
					"group":    map[string]any{"name": "Regular Season"},
					"team":     map[string]any{"id": 11, "name": "Stockerau W", "logo": "https://media.api-sports.io/handball/teams/11.png"},
					"league":   map[string]any{"id": 1, "name": "WHA Women", "type": "League", "logo": "https://media.api-sports.io/handball/leagues/1.png", "season": 2024},
					"country":  map[string]any{"id": 1, "name": "Austria", "code": "AT", "flag": "https://media.api-sports.io/flags/at.svg"},
					"games":    map[string]any{"played": 15, "win": map[string]any{"total": 4, "percentage": "0.27"}, "draw": map[string]any{"total": 2, "percentage": "0.13"}, "lose": map[string]any{"total": 9, "percentage": "0.60"}},
					"goals":    map[string]any{"for": 245, "against": 365},
					"points":   14,
					"form":     "LLDLL",
				},
				map[string]any{
					"position": 9,
					"stage":    "WHA Women",
					"group":    map[string]any{"name": "Regular Season"},
					"team":     map[string]any{"id": 9, "name": "Korneuburg W", "logo": "https://media.api-sports.io/handball/teams/9.png"},
					"league":   map[string]any{"id": 1, "name": "WHA Women", "type": "League", "logo": "https://media.api-sports.io/handball/leagues/1.png", "season": 2024},
					"country":  map[string]any{"id": 1, "name": "Austria", "code": "AT", "flag": "https://media.api-sports.io/flags/at.svg"},
					"games":    map[string]any{"played": 15, "win": map[string]any{"total": 3, "percentage": "0.20"}, "draw": map[string]any{"total": 1, "percentage": "0.07"}, "lose": map[string]any{"total": 11, "percentage": "0.73"}},
					"goals":    map[string]any{"for": 220, "against": 390},
					"points":   10,
					"form":     "LLLLD",
				},
				map[string]any{
					"position": 10,
					"stage":    "WHA Women",
					"group":    map[string]any{"name": "Regular Season"},
					"team":     map[string]any{"id": 7, "name": "Graz W", "logo": "https://media.api-sports.io/handball/teams/7.png"},
					"league":   map[string]any{"id": 1, "name": "WHA Women", "type": "League", "logo": "https://media.api-sports.io/handball/leagues/1.png", "season": 2024},
					"country":  map[string]any{"id": 1, "name": "Austria", "code": "AT", "flag": "https://media.api-sports.io/flags/at.svg"},
					"games":    map[string]any{"played": 15, "win": map[string]any{"total": 2, "percentage": "0.13"}, "draw": map[string]any{"total": 1, "percentage": "0.07"}, "lose": map[string]any{"total": 12, "percentage": "0.80"}},
					"goals":    map[string]any{"for": 195, "against": 420},
					"points":   7,
					"form":     "LLLLL",
				},
			},
		},
	}
}

func rugbyStandingsResponse(endpoint, league string) map[string]any {
	// Super Rugby (league 71), Premiership Rugby (league 13), Six Nations (league 51)
	switch league {
	case "71":
		// Super Rugby 2024
		return map[string]any{
			"get":        "standings",
			"parameters": map[string]any{"league": "71", "season": "2024"},
			"errors":     []any{},
			"results":    1,
			"response": []any{
				[]any{
					map[string]any{
						"position":    1,
						"stage":       "Super Rugby Pacific",
						"group":       map[string]any{"name": "Regular Season"},
						"team":        map[string]any{"id": 70, "name": "Crusaders", "logo": "https://media.api-sports.io/rugby/teams/70.png"},
						"league":      map[string]any{"id": 71, "name": "Super Rugby", "type": "League", "logo": "https://media.api-sports.io/rugby/leagues/71.png", "season": 2024},
						"country":     map[string]any{"id": 158, "name": "New Zealand", "code": "NZ", "flag": "https://media.api-sports.io/flags/nz.svg"},
						"games":       map[string]any{"played": 14, "win": map[string]any{"total": 11, "percentage": "78.57"}, "draw": map[string]any{"total": 0, "percentage": "0.00"}, "lose": map[string]any{"total": 3, "percentage": "21.43"}},
						"goals":       map[string]any{"for": 421, "against": 298},
						"points":      48,
						"form":        "WWWWW",
						"description": "Finals",
					},
					map[string]any{
						"position":    2,
						"stage":       "Super Rugby Pacific",
						"group":       map[string]any{"name": "Regular Season"},
						"team":        map[string]any{"id": 68, "name": "Chiefs", "logo": "https://media.api-sports.io/rugby/teams/68.png"},
						"league":      map[string]any{"id": 71, "name": "Super Rugby", "type": "League", "logo": "https://media.api-sports.io/rugby/leagues/71.png", "season": 2024},
						"country":     map[string]any{"id": 158, "name": "New Zealand", "code": "NZ", "flag": "https://media.api-sports.io/flags/nz.svg"},
						"games":       map[string]any{"played": 14, "win": map[string]any{"total": 10, "percentage": "71.43"}, "draw": map[string]any{"total": 0, "percentage": "0.00"}, "lose": map[string]any{"total": 4, "percentage": "28.57"}},
						"goals":       map[string]any{"for": 398, "against": 312},
						"points":      44,
						"form":        "WWLWW",
						"description": "Finals",
					},
					map[string]any{
						"position":    3,
						"stage":       "Super Rugby Pacific",
						"group":       map[string]any{"name": "Regular Season"},
						"team":        map[string]any{"id": 73, "name": "Hurricanes", "logo": "https://media.api-sports.io/rugby/teams/73.png"},
						"league":      map[string]any{"id": 71, "name": "Super Rugby", "type": "League", "logo": "https://media.api-sports.io/rugby/leagues/71.png", "season": 2024},
						"country":     map[string]any{"id": 158, "name": "New Zealand", "code": "NZ", "flag": "https://media.api-sports.io/flags/nz.svg"},
						"games":       map[string]any{"played": 14, "win": map[string]any{"total": 9, "percentage": "64.29"}, "draw": map[string]any{"total": 0, "percentage": "0.00"}, "lose": map[string]any{"total": 5, "percentage": "35.71"}},
						"goals":       map[string]any{"for": 356, "against": 329},
						"points":      39,
						"form":        "WWLWL",
						"description": "Finals",
					},
					map[string]any{
						"position":    4,
						"stage":       "Super Rugby Pacific",
						"group":       map[string]any{"name": "Regular Season"},
						"team":        map[string]any{"id": 65, "name": "Blues", "logo": "https://media.api-sports.io/rugby/teams/65.png"},
						"league":      map[string]any{"id": 71, "name": "Super Rugby", "type": "League", "logo": "https://media.api-sports.io/rugby/leagues/71.png", "season": 2024},
						"country":     map[string]any{"id": 158, "name": "New Zealand", "code": "NZ", "flag": "https://media.api-sports.io/flags/nz.svg"},
						"games":       map[string]any{"played": 14, "win": map[string]any{"total": 8, "percentage": "57.14"}, "draw": map[string]any{"total": 1, "percentage": "7.14"}, "lose": map[string]any{"total": 5, "percentage": "35.71"}},
						"goals":       map[string]any{"for": 367, "against": 341},
						"points":      37,
						"form":        "WLWLW",
						"description": "Quarter Final",
					},
					map[string]any{
						"position":    5,
						"stage":       "Super Rugby Pacific",
						"group":       map[string]any{"name": "Regular Season"},
						"team":        map[string]any{"id": 74, "name": "Highlanders", "logo": "https://media.api-sports.io/rugby/teams/74.png"},
						"league":      map[string]any{"id": 71, "name": "Super Rugby", "type": "League", "logo": "https://media.api-sports.io/rugby/leagues/71.png", "season": 2024},
						"country":     map[string]any{"id": 158, "name": "New Zealand", "code": "NZ", "flag": "https://media.api-sports.io/flags/nz.svg"},
						"games":       map[string]any{"played": 14, "win": map[string]any{"total": 7, "percentage": "50.00"}, "draw": map[string]any{"total": 1, "percentage": "7.14"}, "lose": map[string]any{"total": 6, "percentage": "42.86"}},
						"goals":       map[string]any{"for": 301, "against": 318},
						"points":      33,
						"form":        "LWWLW",
						"description": "Quarter Final",
					},
					map[string]any{
						"position":    6,
						"stage":       "Super Rugby Pacific",
						"group":       map[string]any{"name": "Regular Season"},
						"team":        map[string]any{"id": 69, "name": "Stormers", "logo": "https://media.api-sports.io/rugby/teams/69.png"},
						"league":      map[string]any{"id": 71, "name": "Super Rugby", "type": "League", "logo": "https://media.api-sports.io/rugby/leagues/71.png", "season": 2024},
						"country":     map[string]any{"id": 202, "name": "South Africa", "code": "ZA", "flag": "https://media.api-sports.io/flags/za.svg"},
						"games":       map[string]any{"played": 14, "win": map[string]any{"total": 7, "percentage": "50.00"}, "draw": map[string]any{"total": 0, "percentage": "0.00"}, "lose": map[string]any{"total": 7, "percentage": "50.00"}},
						"goals":       map[string]any{"for": 312, "against": 334},
						"points":      31,
						"form":        "LWWLL",
						"description": nil,
					},
					map[string]any{
						"position":    7,
						"stage":       "Super Rugby Pacific",
						"group":       map[string]any{"name": "Regular Season"},
						"team":        map[string]any{"id": 76, "name": "Waratahs", "logo": "https://media.api-sports.io/rugby/teams/76.png"},
						"league":      map[string]any{"id": 71, "name": "Super Rugby", "type": "League", "logo": "https://media.api-sports.io/rugby/leagues/71.png", "season": 2024},
						"country":     map[string]any{"id": 2, "name": "Australia", "code": "AU", "flag": "https://media.api-sports.io/flags/au.svg"},
						"games":       map[string]any{"played": 14, "win": map[string]any{"total": 6, "percentage": "42.86"}, "draw": map[string]any{"total": 0, "percentage": "0.00"}, "lose": map[string]any{"total": 8, "percentage": "57.14"}},
						"goals":       map[string]any{"for": 298, "against": 356},
						"points":      27,
						"form":        "LLWLW",
						"description": nil,
					},
					map[string]any{
						"position":    8,
						"stage":       "Super Rugby Pacific",
						"group":       map[string]any{"name": "Regular Season"},
						"team":        map[string]any{"id": 77, "name": "Brumbies", "logo": "https://media.api-sports.io/rugby/teams/77.png"},
						"league":      map[string]any{"id": 71, "name": "Super Rugby", "type": "League", "logo": "https://media.api-sports.io/rugby/leagues/71.png", "season": 2024},
						"country":     map[string]any{"id": 2, "name": "Australia", "code": "AU", "flag": "https://media.api-sports.io/flags/au.svg"},
						"games":       map[string]any{"played": 14, "win": map[string]any{"total": 5, "percentage": "35.71"}, "draw": map[string]any{"total": 1, "percentage": "7.14"}, "lose": map[string]any{"total": 8, "percentage": "57.14"}},
						"goals":       map[string]any{"for": 294, "against": 358},
						"points":      25,
						"form":        "LWLLL",
						"description": nil,
					},
					map[string]any{
						"position":    9,
						"stage":       "Super Rugby Pacific",
						"group":       map[string]any{"name": "Regular Season"},
						"team":        map[string]any{"id": 71, "name": " Reds", "logo": "https://media.api-sports.io/rugby/teams/71.png"},
						"league":      map[string]any{"id": 71, "name": "Super Rugby", "type": "League", "logo": "https://media.api-sports.io/rugby/leagues/71.png", "season": 2024},
						"country":     map[string]any{"id": 2, "name": "Australia", "code": "AU", "flag": "https://media.api-sports.io/flags/au.svg"},
						"games":       map[string]any{"played": 14, "win": map[string]any{"total": 4, "percentage": "28.57"}, "draw": map[string]any{"total": 1, "percentage": "7.14"}, "lose": map[string]any{"total": 9, "percentage": "64.29"}},
						"goals":       map[string]any{"for": 276, "against": 378},
						"points":      20,
						"form":        "LLLLW",
						"description": nil,
					},
					map[string]any{
						"position":    10,
						"stage":       "Super Rugby Pacific",
						"group":       map[string]any{"name": "Regular Season"},
						"team":        map[string]any{"id": 67, "name": "Force", "logo": "https://media.api-sports.io/rugby/teams/67.png"},
						"league":      map[string]any{"id": 71, "name": "Super Rugby", "type": "League", "logo": "https://media.api-sports.io/rugby/leagues/71.png", "season": 2024},
						"country":     map[string]any{"id": 2, "name": "Australia", "code": "AU", "flag": "https://media.api-sports.io/flags/au.svg"},
						"games":       map[string]any{"played": 14, "win": map[string]any{"total": 4, "percentage": "28.57"}, "draw": map[string]any{"total": 0, "percentage": "0.00"}, "lose": map[string]any{"total": 10, "percentage": "71.43"}},
						"goals":       map[string]any{"for": 261, "against": 372},
						"points":      18,
						"form":        "LLLLL",
						"description": nil,
					},
					map[string]any{
						"position":    11,
						"stage":       "Super Rugby Pacific",
						"group":       map[string]any{"name": "Regular Season"},
						"team":        map[string]any{"id": 72, "name": "Moana Pasifika", "logo": "https://media.api-sports.io/rugby/teams/72.png"},
						"league":      map[string]any{"id": 71, "name": "Super Rugby", "type": "League", "logo": "https://media.api-sports.io/rugby/leagues/71.png", "season": 2024},
						"country":     map[string]any{"id": 158, "name": "New Zealand", "code": "NZ", "flag": "https://media.api-sports.io/flags/nz.svg"},
						"games":       map[string]any{"played": 14, "win": map[string]any{"total": 3, "percentage": "21.43"}, "draw": map[string]any{"total": 1, "percentage": "7.14"}, "lose": map[string]any{"total": 10, "percentage": "71.43"}},
						"goals":       map[string]any{"for": 247, "against": 389},
						"points":      16,
						"form":        "LLWLL",
						"description": nil,
					},
					map[string]any{
						"position":    12,
						"stage":       "Super Rugby Pacific",
						"group":       map[string]any{"name": "Regular Season"},
						"team":        map[string]any{"id": 66, "name": "Jaguares", "logo": "https://media.api-sports.io/rugby/teams/66.png"},
						"league":      map[string]any{"id": 71, "name": "Super Rugby", "type": "League", "logo": "https://media.api-sports.io/rugby/leagues/71.png", "season": 2024},
						"country":     map[string]any{"id": 11, "name": "Argentina", "code": "AR", "flag": "https://media.api-sports.io/flags/ar.svg"},
						"games":       map[string]any{"played": 14, "win": map[string]any{"total": 2, "percentage": "14.29"}, "draw": map[string]any{"total": 0, "percentage": "0.00"}, "lose": map[string]any{"total": 12, "percentage": "85.71"}},
						"goals":       map[string]any{"for": 198, "against": 418},
						"points":      10,
						"form":        "LLLLL",
						"description": nil,
					},
				},
			},
		}
	case "13":
		// Premiership Rugby 2024
		return map[string]any{
			"get":        "standings",
			"parameters": map[string]any{"league": "13", "season": "2024"},
			"errors":     []any{},
			"results":    1,
			"response": []any{
				[]any{
					map[string]any{
						"position":    1,
						"stage":       "Premiership",
						"group":       map[string]any{"name": "Regular Season"},
						"team":        map[string]any{"id": 10, "name": "Saracens", "logo": "https://media.api-sports.io/rugby/teams/10.png"},
						"league":      map[string]any{"id": 13, "name": "Premiership Rugby", "type": "League", "logo": "https://media.api-sports.io/rugby/leagues/13.png", "season": 2024},
						"country":     map[string]any{"id": 68, "name": "England", "code": "GB", "flag": "https://media.api-sports.io/flags/gb.svg"},
						"games":       map[string]any{"played": 22, "win": map[string]any{"total": 17, "percentage": "77.27"}, "draw": map[string]any{"total": 0, "percentage": "0.00"}, "lose": map[string]any{"total": 5, "percentage": "22.73"}},
						"goals":       map[string]any{"for": 639, "against": 412},
						"points":      76,
						"form":        "WWWWW",
						"description": "Championship Final",
					},
					map[string]any{
						"position":    2,
						"stage":       "Premiership",
						"group":       map[string]any{"name": "Regular Season"},
						"team":        map[string]any{"id": 1, "name": "Leicester Tigers", "logo": "https://media.api-sports.io/rugby/teams/1.png"},
						"league":      map[string]any{"id": 13, "name": "Premiership Rugby", "type": "League", "logo": "https://media.api-sports.io/rugby/leagues/13.png", "season": 2024},
						"country":     map[string]any{"id": 68, "name": "England", "code": "GB", "flag": "https://media.api-sports.io/flags/gb.svg"},
						"games":       map[string]any{"played": 22, "win": map[string]any{"total": 15, "percentage": "68.18"}, "draw": map[string]any{"total": 1, "percentage": "4.55"}, "lose": map[string]any{"total": 6, "percentage": "27.27"}},
						"goals":       map[string]any{"for": 587, "against": 456},
						"points":      69,
						"form":        "WWLWW",
						"description": "Playoffs",
					},
					map[string]any{
						"position":    3,
						"stage":       "Premiership",
						"group":       map[string]any{"name": "Regular Season"},
						"team":        map[string]any{"id": 6, "name": "Sale Sharks", "logo": "https://media.api-sports.io/rugby/teams/6.png"},
						"league":      map[string]any{"id": 13, "name": "Premiership Rugby", "type": "League", "logo": "https://media.api-sports.io/rugby/leagues/13.png", "season": 2024},
						"country":     map[string]any{"id": 68, "name": "England", "code": "GB", "flag": "https://media.api-sports.io/flags/gb.svg"},
						"games":       map[string]any{"played": 22, "win": map[string]any{"total": 14, "percentage": "63.64"}, "draw": map[string]any{"total": 0, "percentage": "0.00"}, "lose": map[string]any{"total": 8, "percentage": "36.36"}},
						"goals":       map[string]any{"for": 558, "against": 478},
						"points":      62,
						"form":        "WWLWL",
						"description": "Playoffs",
					},
					map[string]any{
						"position":    4,
						"stage":       "Premiership",
						"group":       map[string]any{"name": "Regular Season"},
						"team":        map[string]any{"id": 4, "name": "Bristol Bears", "logo": "https://media.api-sports.io/rugby/teams/4.png"},
						"league":      map[string]any{"id": 13, "name": "Premiership Rugby", "type": "League", "logo": "https://media.api-sports.io/rugby/leagues/13.png", "season": 2024},
						"country":     map[string]any{"id": 68, "name": "England", "code": "GB", "flag": "https://media.api-sports.io/flags/gb.svg"},
						"games":       map[string]any{"played": 22, "win": map[string]any{"total": 12, "percentage": "54.55"}, "draw": map[string]any{"total": 2, "percentage": "9.09"}, "lose": map[string]any{"total": 8, "percentage": "36.36"}},
						"goals":       map[string]any{"for": 561, "against": 489},
						"points":      56,
						"form":        "WLWWL",
						"description": "Playoffs",
					},
					map[string]any{
						"position":    5,
						"stage":       "Premiership",
						"group":       map[string]any{"name": "Regular Season"},
						"team":        map[string]any{"id": 7, "name": "Harlequins", "logo": "https://media.api-sports.io/rugby/teams/7.png"},
						"league":      map[string]any{"id": 13, "name": "Premiership Rugby", "type": "League", "logo": "https://media.api-sports.io/rugby/leagues/13.png", "season": 2024},
						"country":     map[string]any{"id": 68, "name": "England", "code": "GB", "flag": "https://media.api-sports.io/flags/gb.svg"},
						"games":       map[string]any{"played": 22, "win": map[string]any{"total": 11, "percentage": "50.00"}, "draw": map[string]any{"total": 2, "percentage": "9.09"}, "lose": map[string]any{"total": 9, "percentage": "40.91"}},
						"goals":       map[string]any{"for": 512, "against": 498},
						"points":      52,
						"form":        "LWWLW",
						"description": "Playoffs",
					},
					map[string]any{
						"position":    6,
						"stage":       "Premiership",
						"group":       map[string]any{"name": "Regular Season"},
						"team":        map[string]any{"id": 8, "name": "Gloucester", "logo": "https://media.api-sports.io/rugby/teams/8.png"},
						"league":      map[string]any{"id": 13, "name": "Premiership Rugby", "type": "League", "logo": "https://media.api-sports.io/rugby/leagues/13.png", "season": 2024},
						"country":     map[string]any{"id": 68, "name": "England", "code": "GB", "flag": "https://media.api-sports.io/flags/gb.svg"},
						"games":       map[string]any{"played": 22, "win": map[string]any{"total": 10, "percentage": "45.45"}, "draw": map[string]any{"total": 1, "percentage": "4.55"}, "lose": map[string]any{"total": 11, "percentage": "50.00"}},
						"goals":       map[string]any{"for": 467, "against": 512},
						"points":      47,
						"form":        "LWLLW",
						"description": nil,
					},
					map[string]any{
						"position":    7,
						"stage":       "Premiership",
						"group":       map[string]any{"name": "Regular Season"},
						"team":        map[string]any{"id": 2, "name": "Exeter Chiefs", "logo": "https://media.api-sports.io/rugby/teams/2.png"},
						"league":      map[string]any{"id": 13, "name": "Premiership Rugby", "type": "League", "logo": "https://media.api-sports.io/rugby/leagues/13.png", "season": 2024},
						"country":     map[string]any{"id": 68, "name": "England", "code": "GB", "flag": "https://media.api-sports.io/flags/gb.svg"},
						"games":       map[string]any{"played": 22, "win": map[string]any{"total": 10, "percentage": "45.45"}, "draw": map[string]any{"total": 0, "percentage": "0.00"}, "lose": map[string]any{"total": 12, "percentage": "54.55"}},
						"goals":       map[string]any{"for": 498, "against": 534},
						"points":      45,
						"form":        "WWLLL",
						"description": nil,
					},
					map[string]any{
						"position":    8,
						"stage":       "Premiership",
						"group":       map[string]any{"name": "Regular Season"},
						"team":        map[string]any{"id": 3, "name": "Bath Rugby", "logo": "https://media.api-sports.io/rugby/teams/3.png"},
						"league":      map[string]any{"id": 13, "name": "Premiership Rugby", "type": "League", "logo": "https://media.api-sports.io/rugby/leagues/13.png", "season": 2024},
						"country":     map[string]any{"id": 68, "name": "England", "code": "GB", "flag": "https://media.api-sports.io/flags/gb.svg"},
						"games":       map[string]any{"played": 22, "win": map[string]any{"total": 9, "percentage": "40.91"}, "draw": map[string]any{"total": 1, "percentage": "4.55"}, "lose": map[string]any{"total": 12, "percentage": "54.55"}},
						"goals":       map[string]any{"for": 489, "against": 521},
						"points":      43,
						"form":        "LLWLW",
						"description": nil,
					},
					map[string]any{
						"position":    9,
						"stage":       "Premiership",
						"group":       map[string]any{"name": "Regular Season"},
						"team":        map[string]any{"id": 5, "name": "Northampton Saints", "logo": "https://media.api-sports.io/rugby/teams/5.png"},
						"league":      map[string]any{"id": 13, "name": "Premiership Rugby", "type": "League", "logo": "https://media.api-sports.io/rugby/leagues/13.png", "season": 2024},
						"country":     map[string]any{"id": 68, "name": "England", "code": "GB", "flag": "https://media.api-sports.io/flags/gb.svg"},
						"games":       map[string]any{"played": 22, "win": map[string]any{"total": 9, "percentage": "40.91"}, "draw": map[string]any{"total": 0, "percentage": "0.00"}, "lose": map[string]any{"total": 13, "percentage": "59.09"}},
						"goals":       map[string]any{"for": 478, "against": 534},
						"points":      41,
						"form":        "LLWLL",
						"description": nil,
					},
					map[string]any{
						"position":    10,
						"stage":       "Premiership",
						"group":       map[string]any{"name": "Regular Season"},
						"team":        map[string]any{"id": 9, "name": "Newcastle Falcons", "logo": "https://media.api-sports.io/rugby/teams/9.png"},
						"league":      map[string]any{"id": 13, "name": "Premiership Rugby", "type": "League", "logo": "https://media.api-sports.io/rugby/leagues/13.png", "season": 2024},
						"country":     map[string]any{"id": 68, "name": "England", "code": "GB", "flag": "https://media.api-sports.io/flags/gb.svg"},
						"games":       map[string]any{"played": 22, "win": map[string]any{"total": 6, "percentage": "27.27"}, "draw": map[string]any{"total": 1, "percentage": "4.55"}, "lose": map[string]any{"total": 15, "percentage": "68.18"}},
						"goals":       map[string]any{"for": 398, "against": 578},
						"points":      30,
						"form":        "LLLLL",
						"description": "Relegated",
					},
				},
			},
		}
	case "51":
		// Six Nations 2024
		return map[string]any{
			"get":        "standings",
			"parameters": map[string]any{"league": "51", "season": "2024"},
			"errors":     []any{},
			"results":    1,
			"response": []any{
				[]any{
					map[string]any{
						"position":    1,
						"stage":       "Six Nations",
						"group":       map[string]any{"name": "Championship"},
						"team":        map[string]any{"id": 15, "name": "Ireland", "logo": "https://media.api-sports.io/rugby/teams/15.png"},
						"league":      map[string]any{"id": 51, "name": "Six Nations", "type": "Tournament", "logo": "https://media.api-sports.io/rugby/leagues/51.png", "season": 2024},
						"country":     map[string]any{"id": 104, "name": "Ireland", "code": "IE", "flag": "https://media.api-sports.io/flags/ie.svg"},
						"games":       map[string]any{"played": 5, "win": map[string]any{"total": 5, "percentage": "100.00"}, "draw": map[string]any{"total": 0, "percentage": "0.00"}, "lose": map[string]any{"total": 0, "percentage": "0.00"}},
						"goals":       map[string]any{"for": 156, "against": 65},
						"points":      10,
						"form":        "WWWWW",
						"description": "Champion",
					},
					map[string]any{
						"position":    2,
						"stage":       "Six Nations",
						"group":       map[string]any{"name": "Championship"},
						"team":        map[string]any{"id": 12, "name": "France", "logo": "https://media.api-sports.io/rugby/teams/12.png"},
						"league":      map[string]any{"id": 51, "name": "Six Nations", "type": "Tournament", "logo": "https://media.api-sports.io/rugby/leagues/51.png", "season": 2024},
						"country":     map[string]any{"id": 52, "name": "France", "code": "FR", "flag": "https://media.api-sports.io/flags/fr.svg"},
						"games":       map[string]any{"played": 5, "win": map[string]any{"total": 4, "percentage": "80.00"}, "draw": map[string]any{"total": 0, "percentage": "0.00"}, "lose": map[string]any{"total": 1, "percentage": "20.00"}},
						"goals":       map[string]any{"for": 164, "against": 91},
						"points":      8,
						"form":        "WWLWW",
						"description": nil,
					},
					map[string]any{
						"position":    3,
						"stage":       "Six Nations",
						"group":       map[string]any{"name": "Championship"},
						"team":        map[string]any{"id": 17, "name": "Scotland", "logo": "https://media.api-sports.io/rugby/teams/17.png"},
						"league":      map[string]any{"id": 51, "name": "Six Nations", "type": "Tournament", "logo": "https://media.api-sports.io/rugby/leagues/51.png", "season": 2024},
						"country":     map[string]any{"id": 222, "name": "Scotland", "code": "GB", "flag": "https://media.api-sports.io/flags/gb.svg"},
						"games":       map[string]any{"played": 5, "win": map[string]any{"total": 3, "percentage": "60.00"}, "draw": map[string]any{"total": 0, "percentage": "0.00"}, "lose": map[string]any{"total": 2, "percentage": "40.00"}},
						"goals":       map[string]any{"for": 109, "against": 102},
						"points":      6,
						"form":        "WWLLW",
						"description": nil,
					},
					map[string]any{
						"position":    4,
						"stage":       "Six Nations",
						"group":       map[string]any{"name": "Championship"},
						"team":        map[string]any{"id": 16, "name": "England", "logo": "https://media.api-sports.io/rugby/teams/16.png"},
						"league":      map[string]any{"id": 51, "name": "Six Nations", "type": "Tournament", "logo": "https://media.api-sports.io/rugby/leagues/51.png", "season": 2024},
						"country":     map[string]any{"id": 68, "name": "England", "code": "GB", "flag": "https://media.api-sports.io/flags/gb.svg"},
						"games":       map[string]any{"played": 5, "win": map[string]any{"total": 2, "percentage": "40.00"}, "draw": map[string]any{"total": 0, "percentage": "0.00"}, "lose": map[string]any{"total": 3, "percentage": "60.00"}},
						"goals":       map[string]any{"for": 104, "against": 132},
						"points":      4,
						"form":        "LWWLL",
						"description": nil,
					},
					map[string]any{
						"position":    5,
						"stage":       "Six Nations",
						"group":       map[string]any{"name": "Championship"},
						"team":        map[string]any{"id": 13, "name": "Wales", "logo": "https://media.api-sports.io/rugby/teams/13.png"},
						"league":      map[string]any{"id": 51, "name": "Six Nations", "type": "Tournament", "logo": "https://media.api-sports.io/rugby/leagues/51.png", "season": 2024},
						"country":     map[string]any{"id": 233, "name": "Wales", "code": "GB", "flag": "https://media.api-sports.io/flags/gb.svg"},
						"games":       map[string]any{"played": 5, "win": map[string]any{"total": 1, "percentage": "20.00"}, "draw": map[string]any{"total": 0, "percentage": "0.00"}, "lose": map[string]any{"total": 4, "percentage": "80.00"}},
						"goals":       map[string]any{"for": 67, "against": 157},
						"points":      2,
						"form":        "LLLLW",
						"description": nil,
					},
					map[string]any{
						"position":    6,
						"stage":       "Six Nations",
						"group":       map[string]any{"name": "Championship"},
						"team":        map[string]any{"id": 14, "name": "Italy", "logo": "https://media.api-sports.io/rugby/teams/14.png"},
						"league":      map[string]any{"id": 51, "name": "Six Nations", "type": "Tournament", "logo": "https://media.api-sports.io/rugby/leagues/51.png", "season": 2024},
						"country":     map[string]any{"id": 105, "name": "Italy", "code": "IT", "flag": "https://media.api-sports.io/flags/it.svg"},
						"games":       map[string]any{"played": 5, "win": map[string]any{"total": 0, "percentage": "0.00"}, "draw": map[string]any{"total": 0, "percentage": "0.00"}, "lose": map[string]any{"total": 5, "percentage": "100.00"}},
						"goals":       map[string]any{"for": 57, "against": 210},
						"points":      0,
						"form":        "LLLLL",
						"description": nil,
					},
				},
			},
		}
	default:
		// Default to NRC (Australian NRC - league 3)
		return map[string]any{
			"get":        "standings",
			"parameters": map[string]any{"league": "3", "season": "2024"},
			"errors":     []any{},
			"results":    1,
			"response": []any{
				[]any{
					map[string]any{
						"position":    1,
						"stage":       "NRC",
						"group":       map[string]any{"name": "Regular Season"},
						"team":        map[string]any{"id": 33, "name": "Western Force", "logo": "https://media.api-sports.io/rugby/teams/33.png"},
						"league":      map[string]any{"id": 3, "name": "NRC", "type": "League", "logo": "https://media.api-sports.io/rugby/leagues/3.png", "season": 2024},
						"country":     map[string]any{"id": 2, "name": "Australia", "code": "AU", "flag": "https://media.api-sports.io/flags/au.svg"},
						"games":       map[string]any{"played": 7, "win": map[string]any{"total": 6, "percentage": "85.71"}, "draw": map[string]any{"total": 0, "percentage": "0.00"}, "lose": map[string]any{"total": 1, "percentage": "14.29"}},
						"goals":       map[string]any{"for": 285, "against": 213},
						"points":      28,
						"form":        "WWLWW",
						"description": "Promotion - NRC (Play Offs)",
					},
					map[string]any{
						"position":    2,
						"stage":       "NRC",
						"group":       map[string]any{"name": "Regular Season"},
						"team":        map[string]any{"id": 27, "name": "Canberra Vikings", "logo": "https://media.api-sports.io/rugby/teams/27.png"},
						"league":      map[string]any{"id": 3, "name": "NRC", "type": "League", "logo": "https://media.api-sports.io/rugby/leagues/3.png", "season": 2024},
						"country":     map[string]any{"id": 2, "name": "Australia", "code": "AU", "flag": "https://media.api-sports.io/flags/au.svg"},
						"games":       map[string]any{"played": 7, "win": map[string]any{"total": 5, "percentage": "71.43"}, "draw": map[string]any{"total": 0, "percentage": "0.00"}, "lose": map[string]any{"total": 2, "percentage": "28.57"}},
						"goals":       map[string]any{"for": 238, "against": 211},
						"points":      22,
						"form":        "WWWLW",
						"description": "Promotion - NRC (Play Offs)",
					},
					map[string]any{
						"position":    3,
						"stage":       "NRC",
						"group":       map[string]any{"name": "Regular Season"},
						"team":        map[string]any{"id": 28, "name": "Fijian Drua", "logo": "https://media.api-sports.io/rugby/teams/28.png"},
						"league":      map[string]any{"id": 3, "name": "NRC", "type": "League", "logo": "https://media.api-sports.io/rugby/leagues/3.png", "season": 2024},
						"country":     map[string]any{"id": 2, "name": "Australia", "code": "AU", "flag": "https://media.api-sports.io/flags/au.svg"},
						"games":       map[string]any{"played": 7, "win": map[string]any{"total": 3, "percentage": "42.86"}, "draw": map[string]any{"total": 2, "percentage": "28.57"}, "lose": map[string]any{"total": 2, "percentage": "28.57"}},
						"goals":       map[string]any{"for": 231, "against": 214},
						"points":      17,
						"form":        "WWLW",
						"description": "Promotion - NRC (Play Offs)",
					},
					map[string]any{
						"position":    4,
						"stage":       "NRC",
						"group":       map[string]any{"name": "Regular Season"},
						"team":        map[string]any{"id": 26, "name": "Brisbane City", "logo": "https://media.api-sports.io/rugby/teams/26.png"},
						"league":      map[string]any{"id": 3, "name": "NRC", "type": "League", "logo": "https://media.api-sports.io/rugby/leagues/3.png", "season": 2024},
						"country":     map[string]any{"id": 2, "name": "Australia", "code": "AU", "flag": "https://media.api-sports.io/flags/au.svg"},
						"games":       map[string]any{"played": 7, "win": map[string]any{"total": 3, "percentage": "42.86"}, "draw": map[string]any{"total": 1, "percentage": "14.29"}, "lose": map[string]any{"total": 3, "percentage": "42.86"}},
						"goals":       map[string]any{"for": 214, "against": 199},
						"points":      17,
						"form":        "WLLWL",
						"description": "Promotion - NRC (Play Offs)",
					},
					map[string]any{
						"position":    5,
						"stage":       "NRC",
						"group":       map[string]any{"name": "Regular Season"},
						"team":        map[string]any{"id": 30, "name": "NSW Country Eagles", "logo": "https://media.api-sports.io/rugby/teams/30.png"},
						"league":      map[string]any{"id": 3, "name": "NRC", "type": "League", "logo": "https://media.api-sports.io/rugby/leagues/3.png", "season": 2024},
						"country":     map[string]any{"id": 2, "name": "Australia", "code": "AU", "flag": "https://media.api-sports.io/flags/au.svg"},
						"games":       map[string]any{"played": 7, "win": map[string]any{"total": 3, "percentage": "42.86"}, "draw": map[string]any{"total": 1, "percentage": "14.29"}, "lose": map[string]any{"total": 3, "percentage": "42.86"}},
						"goals":       map[string]any{"for": 181, "against": 172},
						"points":      16,
						"form":        "LLWL",
						"description": nil,
					},
					map[string]any{
						"position":    6,
						"stage":       "NRC",
						"group":       map[string]any{"name": "Regular Season"},
						"team":        map[string]any{"id": 31, "name": "Queensland Country", "logo": "https://media.api-sports.io/rugby/teams/31.png"},
						"league":      map[string]any{"id": 3, "name": "NRC", "type": "League", "logo": "https://media.api-sports.io/rugby/leagues/3.png", "season": 2024},
						"country":     map[string]any{"id": 2, "name": "Australia", "code": "AU", "flag": "https://media.api-sports.io/flags/au.svg"},
						"games":       map[string]any{"played": 7, "win": map[string]any{"total": 3, "percentage": "42.86"}, "draw": map[string]any{"total": 0, "percentage": "0.00"}, "lose": map[string]any{"total": 4, "percentage": "57.14"}},
						"goals":       map[string]any{"for": 205, "against": 235},
						"points":      15,
						"form":        "LWWLL",
						"description": nil,
					},
					map[string]any{
						"position":    7,
						"stage":       "NRC",
						"group":       map[string]any{"name": "Regular Season"},
						"team":        map[string]any{"id": 29, "name": "Melbourne Rising", "logo": "https://media.api-sports.io/rugby/teams/29.png"},
						"league":      map[string]any{"id": 3, "name": "NRC", "type": "League", "logo": "https://media.api-sports.io/rugby/leagues/3.png", "season": 2024},
						"country":     map[string]any{"id": 2, "name": "Australia", "code": "AU", "flag": "https://media.api-sports.io/flags/au.svg"},
						"games":       map[string]any{"played": 7, "win": map[string]any{"total": 2, "percentage": "28.57"}, "draw": map[string]any{"total": 0, "percentage": "0.00"}, "lose": map[string]any{"total": 5, "percentage": "71.43"}},
						"goals":       map[string]any{"for": 206, "against": 211},
						"points":      11,
						"form":        "LLWWL",
						"description": nil,
					},
					map[string]any{
						"position":    8,
						"stage":       "NRC",
						"group":       map[string]any{"name": "Regular Season"},
						"team":        map[string]any{"id": 32, "name": "Sydney Rays", "logo": "https://media.api-sports.io/rugby/teams/32.png"},
						"league":      map[string]any{"id": 3, "name": "NRC", "type": "League", "logo": "https://media.api-sports.io/rugby/leagues/3.png", "season": 2024},
						"country":     map[string]any{"id": 2, "name": "Australia", "code": "AU", "flag": "https://media.api-sports.io/flags/au.svg"},
						"games":       map[string]any{"played": 7, "win": map[string]any{"total": 1, "percentage": "14.29"}, "draw": map[string]any{"total": 0, "percentage": "0.00"}, "lose": map[string]any{"total": 6, "percentage": "85.71"}},
						"goals":       map[string]any{"for": 220, "against": 325},
						"points":      6,
						"form":        "LLLLW",
						"description": nil,
					},
				},
			},
		}
	}
}

func volleyballStandingsResponse(endpoint string) map[string]any {
	return map[string]any{
		"get":        "standings",
		"parameters": map[string]any{"league": "3", "season": "2021"},
		"errors":     []any{},
		"results":    2,
		"response": []any{
			[]any{
				map[string]any{
					"position": 1, "stage": "Liga Women - First stage", "group": map[string]any{"name": "Group 1"},
					"team":    map[string]any{"id": 13, "name": "Boca Juniors W", "logo": "https://media.api-sports.io/volley/teams/13.png"},
					"league":  map[string]any{"id": 3, "name": "Liga Women", "type": "League", "logo": "https://media.api-sports.io/volley/leagues/3.png", "season": 2021},
					"country": map[string]any{"id": 1, "name": "Argentina", "code": "AR", "flag": "https://media.api-sports.io/flags/ar.svg"},
					"games":   map[string]any{"played": 7, "win": map[string]any{"total": 7, "percentage": "100.00"}, "lose": map[string]any{"total": 0, "percentage": "0.00"}},
					"goals":   map[string]any{"for": 21, "against": 1},
					"points":  21, "form": "WWWWW", "description": "Promotion - Liga Women (Second stage)",
				},
				map[string]any{
					"position": 2, "stage": "Liga Women - First stage", "group": map[string]any{"name": "Group 1"},
					"team":    map[string]any{"id": 21, "name": "River Plate W", "logo": "https://media.api-sports.io/volley/teams/21.png"},
					"league":  map[string]any{"id": 3, "name": "Liga Women", "type": "League", "logo": "https://media.api-sports.io/volley/leagues/3.png", "season": 2021},
					"country": map[string]any{"id": 1, "name": "Argentina", "code": "AR", "flag": "https://media.api-sports.io/flags/ar.svg"},
					"games":   map[string]any{"played": 7, "win": map[string]any{"total": 5, "percentage": "71.43"}, "lose": map[string]any{"total": 2, "percentage": "28.57"}},
					"goals":   map[string]any{"for": 18, "against": 6},
					"points":  16, "form": "WLWLW", "description": "Promotion - Liga Women (Second stage)",
				},
				map[string]any{
					"position": 3, "stage": "Liga Women - First stage", "group": map[string]any{"name": "Group 1"},
					"team":    map[string]any{"id": 19, "name": "La Rioja W", "logo": "https://media.api-sports.io/volley/teams/19.png"},
					"league":  map[string]any{"id": 3, "name": "Liga Women", "type": "League", "logo": "https://media.api-sports.io/volley/leagues/3.png", "season": 2021},
					"country": map[string]any{"id": 1, "name": "Argentina", "code": "AR", "flag": "https://media.api-sports.io/flags/ar.svg"},
					"games":   map[string]any{"played": 7, "win": map[string]any{"total": 5, "percentage": "71.43"}, "lose": map[string]any{"total": 2, "percentage": "28.57"}},
					"goals":   map[string]any{"for": 15, "against": 11},
					"points":  13, "form": "WLWWW", "description": "Promotion - Liga Women (Second stage)",
				},
				map[string]any{
					"position": 4, "stage": "Liga Women - First stage", "group": map[string]any{"name": "Group 1"},
					"team":    map[string]any{"id": 24, "name": "Velez Sarsfield W", "logo": "https://media.api-sports.io/volley/teams/24.png"},
					"league":  map[string]any{"id": 3, "name": "Liga Women", "type": "League", "logo": "https://media.api-sports.io/volley/leagues/3.png", "season": 2021},
					"country": map[string]any{"id": 1, "name": "Argentina", "code": "AR", "flag": "https://media.api-sports.io/flags/ar.svg"},
					"games":   map[string]any{"played": 7, "win": map[string]any{"total": 4, "percentage": "57.14"}, "lose": map[string]any{"total": 3, "percentage": "42.86"}},
					"goals":   map[string]any{"for": 14, "against": 10},
					"points":  13, "form": "LWWWL", "description": "Promotion - Liga Women (Second stage)",
				},
				map[string]any{
					"position": 5, "stage": "Liga Women - First stage", "group": map[string]any{"name": "Group 1"},
					"team":    map[string]any{"id": 11, "name": "Avellaneda W", "logo": "https://media.api-sports.io/volley/teams/11.png"},
					"league":  map[string]any{"id": 3, "name": "Liga Women", "type": "League", "logo": "https://media.api-sports.io/volley/leagues/3.png", "season": 2021},
					"country": map[string]any{"id": 1, "name": "Argentina", "code": "AR", "flag": "https://media.api-sports.io/flags/ar.svg"},
					"games":   map[string]any{"played": 7, "win": map[string]any{"total": 3, "percentage": "42.86"}, "lose": map[string]any{"total": 4, "percentage": "57.14"}},
					"goals":   map[string]any{"for": 10, "against": 16},
					"points":  8, "form": "WWLWL", "description": "Promotion - Liga Women (Second stage)",
				},
				map[string]any{
					"position": 6, "stage": "Liga Women - First stage", "group": map[string]any{"name": "Group 1"},
					"team":    map[string]any{"id": 17, "name": "Ferro Carril Oeste W", "logo": "https://media.api-sports.io/volley/teams/17.png"},
					"league":  map[string]any{"id": 3, "name": "Liga Women", "type": "League", "logo": "https://media.api-sports.io/volley/leagues/3.png", "season": 2021},
					"country": map[string]any{"id": 1, "name": "Argentina", "code": "AR", "flag": "https://media.api-sports.io/flags/ar.svg"},
					"games":   map[string]any{"played": 7, "win": map[string]any{"total": 2, "percentage": "28.57"}, "lose": map[string]any{"total": 5, "percentage": "71.43"}},
					"goals":   map[string]any{"for": 9, "against": 16},
					"points":  7, "form": "LWLLW", "description": "Promotion - Liga Women (Second stage)",
				},
				map[string]any{
					"position": 7, "stage": "Liga Women - First stage", "group": map[string]any{"name": "Group 1"},
					"team":    map[string]any{"id": 20, "name": "Mupol W", "logo": "https://media.api-sports.io/volley/teams/20.png"},
					"league":  map[string]any{"id": 3, "name": "Liga Women", "type": "League", "logo": "https://media.api-sports.io/volley/leagues/3.png", "season": 2021},
					"country": map[string]any{"id": 1, "name": "Argentina", "code": "AR", "flag": "https://media.api-sports.io/flags/ar.svg"},
					"games":   map[string]any{"played": 7, "win": map[string]any{"total": 2, "percentage": "28.57"}, "lose": map[string]any{"total": 5, "percentage": "71.43"}},
					"goals":   map[string]any{"for": 8, "against": 18},
					"points":  5, "form": "LLLLL", "description": "Promotion - Liga Women (Second stage)",
				},
				map[string]any{
					"position": 8, "stage": "Liga Women - First stage", "group": map[string]any{"name": "Group 1"},
					"team":    map[string]any{"id": 15, "name": "Echague Parana W", "logo": "https://media.api-sports.io/volley/teams/15.png"},
					"league":  map[string]any{"id": 3, "name": "Liga Women", "type": "League", "logo": "https://media.api-sports.io/volley/leagues/3.png", "season": 2021},
					"country": map[string]any{"id": 1, "name": "Argentina", "code": "AR", "flag": "https://media.api-sports.io/flags/ar.svg"},
					"games":   map[string]any{"played": 7, "win": map[string]any{"total": 0, "percentage": "0.00"}, "lose": map[string]any{"total": 7, "percentage": "100.00"}},
					"goals":   map[string]any{"for": 4, "against": 21},
					"points":  1, "form": "LLLLL", "description": "Promotion - Liga Women (Second stage)",
				},
				map[string]any{
					"position": 1, "stage": "Liga Women - First stage", "group": map[string]any{"name": "Group 2"},
					"team":    map[string]any{"id": 22, "name": "San Lorenzo W", "logo": "https://media.api-sports.io/volley/teams/22.png"},
					"league":  map[string]any{"id": 3, "name": "Liga Women", "type": "League", "logo": "https://media.api-sports.io/volley/leagues/3.png", "season": 2021},
					"country": map[string]any{"id": 1, "name": "Argentina", "code": "AR", "flag": "https://media.api-sports.io/flags/ar.svg"},
					"games":   map[string]any{"played": 7, "win": map[string]any{"total": 7, "percentage": "100.00"}, "lose": map[string]any{"total": 0, "percentage": "0.00"}},
					"goals":   map[string]any{"for": 21, "against": 2},
					"points":  20, "form": "WWWWW", "description": "Promotion - Liga Women (Second stage)",
				},
				map[string]any{
					"position": 2, "stage": "Liga Women - First stage", "group": map[string]any{"name": "Group 2"},
					"team":    map[string]any{"id": 18, "name": "Gimnasia Esgrima W", "logo": "https://media.api-sports.io/volley/teams/18.png"},
					"league":  map[string]any{"id": 3, "name": "Liga Women", "type": "League", "logo": "https://media.api-sports.io/volley/leagues/3.png", "season": 2021},
					"country": map[string]any{"id": 1, "name": "Argentina", "code": "AR", "flag": "https://media.api-sports.io/flags/ar.svg"},
					"games":   map[string]any{"played": 7, "win": map[string]any{"total": 6, "percentage": "85.71"}, "lose": map[string]any{"total": 1, "percentage": "14.29"}},
					"goals":   map[string]any{"for": 18, "against": 3},
					"points":  18, "form": "LWWWW", "description": "Promotion - Liga Women (Second stage)",
				},
				map[string]any{
					"position": 3, "stage": "Liga Women - First stage", "group": map[string]any{"name": "Group 2"},
					"team":    map[string]any{"id": 9, "name": "Andalgala W", "logo": "https://media.api-sports.io/volley/teams/9.png"},
					"league":  map[string]any{"id": 3, "name": "Liga Women", "type": "League", "logo": "https://media.api-sports.io/volley/leagues/3.png", "season": 2021},
					"country": map[string]any{"id": 1, "name": "Argentina", "code": "AR", "flag": "https://media.api-sports.io/flags/ar.svg"},
					"games":   map[string]any{"played": 7, "win": map[string]any{"total": 4, "percentage": "57.14"}, "lose": map[string]any{"total": 3, "percentage": "42.86"}},
					"goals":   map[string]any{"for": 15, "against": 14},
					"points":  11, "form": "WLLLW", "description": "Promotion - Liga Women (Second stage)",
				},
				map[string]any{
					"position": 4, "stage": "Liga Women - First stage", "group": map[string]any{"name": "Group 2"},
					"team":    map[string]any{"id": 10, "name": "Atenas W", "logo": "https://media.api-sports.io/volley/teams/10.png"},
					"league":  map[string]any{"id": 3, "name": "Liga Women", "type": "League", "logo": "https://media.api-sports.io/volley/leagues/3.png", "season": 2021},
					"country": map[string]any{"id": 1, "name": "Argentina", "code": "AR", "flag": "https://media.api-sports.io/flags/ar.svg"},
					"games":   map[string]any{"played": 7, "win": map[string]any{"total": 4, "percentage": "57.14"}, "lose": map[string]any{"total": 3, "percentage": "42.86"}},
					"goals":   map[string]any{"for": 13, "against": 12},
					"points":  11, "form": "LWWLW", "description": "Promotion - Liga Women (Second stage)",
				},
				map[string]any{
					"position": 5, "stage": "Liga Women - First stage", "group": map[string]any{"name": "Group 2"},
					"team":    map[string]any{"id": 16, "name": "Estud. de La Plata W", "logo": "https://media.api-sports.io/volley/teams/16.png"},
					"league":  map[string]any{"id": 3, "name": "Liga Women", "type": "League", "logo": "https://media.api-sports.io/volley/leagues/3.png", "season": 2021},
					"country": map[string]any{"id": 1, "name": "Argentina", "code": "AR", "flag": "https://media.api-sports.io/flags/ar.svg"},
					"games":   map[string]any{"played": 7, "win": map[string]any{"total": 3, "percentage": "42.86"}, "lose": map[string]any{"total": 4, "percentage": "57.14"}},
					"goals":   map[string]any{"for": 12, "against": 13},
					"points":  10, "form": "WWLWL", "description": "Promotion - Liga Women (Second stage)",
				},
				map[string]any{
					"position": 6, "stage": "Liga Women - First stage", "group": map[string]any{"name": "Group 2"},
					"team":    map[string]any{"id": 12, "name": "Banco Provincia W", "logo": "https://media.api-sports.io/volley/teams/12.png"},
					"league":  map[string]any{"id": 3, "name": "Liga Women", "type": "League", "logo": "https://media.api-sports.io/volley/leagues/3.png", "season": 2021},
					"country": map[string]any{"id": 1, "name": "Argentina", "code": "AR", "flag": "https://media.api-sports.io/flags/ar.svg"},
					"games":   map[string]any{"played": 7, "win": map[string]any{"total": 3, "percentage": "42.86"}, "lose": map[string]any{"total": 4, "percentage": "57.14"}},
					"goals":   map[string]any{"for": 12, "against": 13},
					"points":  10, "form": "WLLWL", "description": "Promotion - Liga Women (Second stage)",
				},
				map[string]any{
					"position": 7, "stage": "Liga Women - First stage", "group": map[string]any{"name": "Group 2"},
					"team":    map[string]any{"id": 14, "name": "Douglas Haig W", "logo": "https://media.api-sports.io/volley/teams/14.png"},
					"league":  map[string]any{"id": 3, "name": "Liga Women", "type": "League", "logo": "https://media.api-sports.io/volley/leagues/3.png", "season": 2021},
					"country": map[string]any{"id": 1, "name": "Argentina", "code": "AR", "flag": "https://media.api-sports.io/flags/ar.svg"},
					"games":   map[string]any{"played": 7, "win": map[string]any{"total": 1, "percentage": "14.29"}, "lose": map[string]any{"total": 6, "percentage": "85.71"}},
					"goals":   map[string]any{"for": 5, "against": 18},
					"points":  4, "form": "LLWLL", "description": "Promotion - Liga Women (Second stage)",
				},
				map[string]any{
					"position": 8, "stage": "Liga Women - First stage", "group": map[string]any{"name": "Group 2"},
					"team":    map[string]any{"id": 23, "name": "Tucuman W", "logo": "https://media.api-sports.io/volley/teams/23.png"},
					"league":  map[string]any{"id": 3, "name": "Liga Women", "type": "League", "logo": "https://media.api-sports.io/volley/leagues/3.png", "season": 2021},
					"country": map[string]any{"id": 1, "name": "Argentina", "code": "AR", "flag": "https://media.api-sports.io/flags/ar.svg"},
					"games":   map[string]any{"played": 7, "win": map[string]any{"total": 0, "percentage": "0.00"}, "lose": map[string]any{"total": 7, "percentage": "100.00"}},
					"goals":   map[string]any{"for": 0, "against": 21},
					"points":  0, "form": "LLLLL", "description": "Promotion - Liga Women (Second stage)",
				},
			},
			[]any{
				map[string]any{
					"position": 1, "stage": "Liga Women - Second stage", "group": map[string]any{"name": "Group 1"},
					"team":    map[string]any{"id": 21, "name": "River Plate W", "logo": "https://media.api-sports.io/volley/teams/21.png"},
					"league":  map[string]any{"id": 3, "name": "Liga Women", "type": "League", "logo": "https://media.api-sports.io/volley/leagues/3.png", "season": 2021},
					"country": map[string]any{"id": 1, "name": "Argentina", "code": "AR", "flag": "https://media.api-sports.io/flags/ar.svg"},
					"games":   map[string]any{"played": 3, "win": map[string]any{"total": 3, "percentage": "100.00"}, "lose": map[string]any{"total": 0, "percentage": "0.00"}},
					"goals":   map[string]any{"for": 9, "against": 3},
					"points":  8, "form": "WWW", "description": "Promotion - Liga Women (Play Offs)",
				},
				map[string]any{
					"position": 2, "stage": "Liga Women - Second stage", "group": map[string]any{"name": "Group 1"},
					"team":    map[string]any{"id": 24, "name": "Velez Sarsfield W", "logo": "https://media.api-sports.io/volley/teams/24.png"},
					"league":  map[string]any{"id": 3, "name": "Liga Women", "type": "League", "logo": "https://media.api-sports.io/volley/leagues/3.png", "season": 2021},
					"country": map[string]any{"id": 1, "name": "Argentina", "code": "AR", "flag": "https://media.api-sports.io/flags/ar.svg"},
					"games":   map[string]any{"played": 3, "win": map[string]any{"total": 2, "percentage": "66.67"}, "lose": map[string]any{"total": 1, "percentage": "33.33"}},
					"goals":   map[string]any{"for": 7, "against": 4},
					"points":  6, "form": "WWL", "description": "Promotion - Liga Women (Play Offs)",
				},
				map[string]any{
					"position": 3, "stage": "Liga Women - Second stage", "group": map[string]any{"name": "Group 1"},
					"team":    map[string]any{"id": 19, "name": "La Rioja W", "logo": "https://media.api-sports.io/volley/teams/19.png"},
					"league":  map[string]any{"id": 3, "name": "Liga Women", "type": "League", "logo": "https://media.api-sports.io/volley/leagues/3.png", "season": 2021},
					"country": map[string]any{"id": 1, "name": "Argentina", "code": "AR", "flag": "https://media.api-sports.io/flags/ar.svg"},
					"games":   map[string]any{"played": 3, "win": map[string]any{"total": 1, "percentage": "33.33"}, "lose": map[string]any{"total": 2, "percentage": "66.67"}},
					"goals":   map[string]any{"for": 5, "against": 6},
					"points":  4, "form": "LLW", "description": nil,
				},
				map[string]any{
					"position": 4, "stage": "Liga Women - Second stage", "group": map[string]any{"name": "Group 1"},
					"team":    map[string]any{"id": 23, "name": "Tucuman W", "logo": "https://media.api-sports.io/volley/teams/23.png"},
					"league":  map[string]any{"id": 3, "name": "Liga Women", "type": "League", "logo": "https://media.api-sports.io/volley/leagues/3.png", "season": 2021},
					"country": map[string]any{"id": 1, "name": "Argentina", "code": "AR", "flag": "https://media.api-sports.io/flags/ar.svg"},
					"games":   map[string]any{"played": 3, "win": map[string]any{"total": 0, "percentage": "0.00"}, "lose": map[string]any{"total": 3, "percentage": "100.00"}},
					"goals":   map[string]any{"for": 1, "against": 9},
					"points":  0, "form": "LLL", "description": nil,
				},
				map[string]any{
					"position": 1, "stage": "Liga Women - Second stage", "group": map[string]any{"name": "Group 2"},
					"team":    map[string]any{"id": 18, "name": "Gimnasia Esgrima W", "logo": "https://media.api-sports.io/volley/teams/18.png"},
					"league":  map[string]any{"id": 3, "name": "Liga Women", "type": "League", "logo": "https://media.api-sports.io/volley/leagues/3.png", "season": 2021},
					"country": map[string]any{"id": 1, "name": "Argentina", "code": "AR", "flag": "https://media.api-sports.io/flags/ar.svg"},
					"games":   map[string]any{"played": 3, "win": map[string]any{"total": 3, "percentage": "100.00"}, "lose": map[string]any{"total": 0, "percentage": "0.00"}},
					"goals":   map[string]any{"for": 9, "against": 1},
					"points":  9, "form": "WWW", "description": "Promotion - Liga Women (Play Offs)",
				},
				map[string]any{
					"position": 2, "stage": "Liga Women - Second stage", "group": map[string]any{"name": "Group 2"},
					"team":    map[string]any{"id": 10, "name": "Atenas W", "logo": "https://media.api-sports.io/volley/teams/10.png"},
					"league":  map[string]any{"id": 3, "name": "Liga Women", "type": "League", "logo": "https://media.api-sports.io/volley/leagues/3.png", "season": 2021},
					"country": map[string]any{"id": 1, "name": "Argentina", "code": "AR", "flag": "https://media.api-sports.io/flags/ar.svg"},
					"games":   map[string]any{"played": 3, "win": map[string]any{"total": 2, "percentage": "66.67"}, "lose": map[string]any{"total": 1, "percentage": "33.33"}},
					"goals":   map[string]any{"for": 7, "against": 3},
					"points":  6, "form": "LWW", "description": "Promotion - Liga Women (Play Offs)",
				},
				map[string]any{
					"position": 3, "stage": "Liga Women - Second stage", "group": map[string]any{"name": "Group 2"},
					"team":    map[string]any{"id": 9, "name": "Andalgala W", "logo": "https://media.api-sports.io/volley/teams/9.png"},
					"league":  map[string]any{"id": 3, "name": "Liga Women", "type": "League", "logo": "https://media.api-sports.io/volley/leagues/3.png", "season": 2021},
					"country": map[string]any{"id": 1, "name": "Argentina", "code": "AR", "flag": "https://media.api-sports.io/flags/ar.svg"},
					"games":   map[string]any{"played": 3, "win": map[string]any{"total": 1, "percentage": "33.33"}, "lose": map[string]any{"total": 2, "percentage": "66.67"}},
					"goals":   map[string]any{"for": 3, "against": 6},
					"points":  3, "form": "WLL", "description": nil,
				},
				map[string]any{
					"position": 4, "stage": "Liga Women - Second stage", "group": map[string]any{"name": "Group 2"},
					"team":    map[string]any{"id": 15, "name": "Echague Parana W", "logo": "https://media.api-sports.io/volley/teams/15.png"},
					"league":  map[string]any{"id": 3, "name": "Liga Women", "type": "League", "logo": "https://media.api-sports.io/volley/leagues/3.png", "season": 2021},
					"country": map[string]any{"id": 1, "name": "Argentina", "code": "AR", "flag": "https://media.api-sports.io/flags/ar.svg"},
					"games":   map[string]any{"played": 3, "win": map[string]any{"total": 0, "percentage": "0.00"}, "lose": map[string]any{"total": 3, "percentage": "100.00"}},
					"goals":   map[string]any{"for": 0, "against": 9},
					"points":  0, "form": "LLL", "description": nil,
				},
				map[string]any{
					"position": 1, "stage": "Liga Women - Second stage", "group": map[string]any{"name": "Group 3"},
					"team":    map[string]any{"id": 22, "name": "San Lorenzo W", "logo": "https://media.api-sports.io/volley/teams/22.png"},
					"league":  map[string]any{"id": 3, "name": "Liga Women", "type": "League", "logo": "https://media.api-sports.io/volley/leagues/3.png", "season": 2021},
					"country": map[string]any{"id": 1, "name": "Argentina", "code": "AR", "flag": "https://media.api-sports.io/flags/ar.svg"},
					"games":   map[string]any{"played": 3, "win": map[string]any{"total": 3, "percentage": "100.00"}, "lose": map[string]any{"total": 0, "percentage": "0.00"}},
					"goals":   map[string]any{"for": 9, "against": 0},
					"points":  9, "form": "WWW", "description": "Promotion - Liga Women (Play Offs)",
				},
				map[string]any{
					"position": 2, "stage": "Liga Women - Second stage", "group": map[string]any{"name": "Group 3"},
					"team":    map[string]any{"id": 16, "name": "Estud. de La Plata W", "logo": "https://media.api-sports.io/volley/teams/16.png"},
					"league":  map[string]any{"id": 3, "name": "Liga Women", "type": "League", "logo": "https://media.api-sports.io/volley/leagues/3.png", "season": 2021},
					"country": map[string]any{"id": 1, "name": "Argentina", "code": "AR", "flag": "https://media.api-sports.io/flags/ar.svg"},
					"games":   map[string]any{"played": 3, "win": map[string]any{"total": 2, "percentage": "66.67"}, "lose": map[string]any{"total": 1, "percentage": "33.33"}},
					"goals":   map[string]any{"for": 6, "against": 3},
					"points":  6, "form": "WWL", "description": "Promotion - Liga Women (Play Offs)",
				},
				map[string]any{
					"position": 3, "stage": "Liga Women - Second stage", "group": map[string]any{"name": "Group 3"},
					"team":    map[string]any{"id": 11, "name": "Avellaneda W", "logo": "https://media.api-sports.io/volley/teams/11.png"},
					"league":  map[string]any{"id": 3, "name": "Liga Women", "type": "League", "logo": "https://media.api-sports.io/volley/leagues/3.png", "season": 2021},
					"country": map[string]any{"id": 1, "name": "Argentina", "code": "AR", "flag": "https://media.api-sports.io/flags/ar.svg"},
					"games":   map[string]any{"played": 3, "win": map[string]any{"total": 1, "percentage": "33.33"}, "lose": map[string]any{"total": 2, "percentage": "66.67"}},
					"goals":   map[string]any{"for": 3, "against": 7},
					"points":  3, "form": "LLW", "description": nil,
				},
				map[string]any{
					"position": 4, "stage": "Liga Women - Second stage", "group": map[string]any{"name": "Group 3"},
					"team":    map[string]any{"id": 20, "name": "Mupol W", "logo": "https://media.api-sports.io/volley/teams/20.png"},
					"league":  map[string]any{"id": 3, "name": "Liga Women", "type": "League", "logo": "https://media.api-sports.io/volley/leagues/3.png", "season": 2021},
					"country": map[string]any{"id": 1, "name": "Argentina", "code": "AR", "flag": "https://media.api-sports.io/flags/ar.svg"},
					"games":   map[string]any{"played": 3, "win": map[string]any{"total": 0, "percentage": "0.00"}, "lose": map[string]any{"total": 3, "percentage": "100.00"}},
					"goals":   map[string]any{"for": 1, "against": 9},
					"points":  0, "form": "LLL", "description": nil,
				},
				map[string]any{
					"position": 1, "stage": "Liga Women - Second stage", "group": map[string]any{"name": "Group 4"},
					"team":    map[string]any{"id": 13, "name": "Boca Juniors W", "logo": "https://media.api-sports.io/volley/teams/13.png"},
					"league":  map[string]any{"id": 3, "name": "Liga Women", "type": "League", "logo": "https://media.api-sports.io/volley/leagues/3.png", "season": 2021},
					"country": map[string]any{"id": 1, "name": "Argentina", "code": "AR", "flag": "https://media.api-sports.io/flags/ar.svg"},
					"games":   map[string]any{"played": 3, "win": map[string]any{"total": 3, "percentage": "100.00"}, "lose": map[string]any{"total": 0, "percentage": "0.00"}},
					"goals":   map[string]any{"for": 9, "against": 0},
					"points":  9, "form": "WWW", "description": "Promotion - Liga Women (Play Offs)",
				},
				map[string]any{
					"position": 2, "stage": "Liga Women - Second stage", "group": map[string]any{"name": "Group 4"},
					"team":    map[string]any{"id": 12, "name": "Banco Provincia W", "logo": "https://media.api-sports.io/volley/teams/12.png"},
					"league":  map[string]any{"id": 3, "name": "Liga Women", "type": "League", "logo": "https://media.api-sports.io/volley/leagues/3.png", "season": 2021},
					"country": map[string]any{"id": 1, "name": "Argentina", "code": "AR", "flag": "https://media.api-sports.io/flags/ar.svg"},
					"games":   map[string]any{"played": 3, "win": map[string]any{"total": 2, "percentage": "66.67"}, "lose": map[string]any{"total": 1, "percentage": "33.33"}},
					"goals":   map[string]any{"for": 6, "against": 4},
					"points":  6, "form": "WWL", "description": "Promotion - Liga Women (Play Offs)",
				},
				map[string]any{
					"position": 3, "stage": "Liga Women - Second stage", "group": map[string]any{"name": "Group 4"},
					"team":    map[string]any{"id": 14, "name": "Douglas Haig W", "logo": "https://media.api-sports.io/volley/teams/14.png"},
					"league":  map[string]any{"id": 3, "name": "Liga Women", "type": "League", "logo": "https://media.api-sports.io/volley/leagues/3.png", "season": 2021},
					"country": map[string]any{"id": 1, "name": "Argentina", "code": "AR", "flag": "https://media.api-sports.io/flags/ar.svg"},
					"games":   map[string]any{"played": 3, "win": map[string]any{"total": 1, "percentage": "33.33"}, "lose": map[string]any{"total": 2, "percentage": "66.67"}},
					"goals":   map[string]any{"for": 3, "against": 8},
					"points":  2, "form": "LLW", "description": nil,
				},
				map[string]any{
					"position": 4, "stage": "Liga Women - Second stage", "group": map[string]any{"name": "Group 4"},
					"team":    map[string]any{"id": 17, "name": "Ferro Carril Oeste W", "logo": "https://media.api-sports.io/volley/teams/17.png"},
					"league":  map[string]any{"id": 3, "name": "Liga Women", "type": "League", "logo": "https://media.api-sports.io/volley/leagues/3.png", "season": 2021},
					"country": map[string]any{"id": 1, "name": "Argentina", "code": "AR", "flag": "https://media.api-sports.io/flags/ar.svg"},
					"games":   map[string]any{"played": 3, "win": map[string]any{"total": 0, "percentage": "0.00"}, "lose": map[string]any{"total": 3, "percentage": "100.00"}},
					"goals":   map[string]any{"for": 3, "against": 9},
					"points":  1, "form": "LLL", "description": nil,
				},
			},
		},
	}
}

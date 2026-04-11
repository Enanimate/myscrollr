package main

import (
	"encoding/json"
	"log"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"
)

type state struct {
	mu             sync.RWMutex
	scenario       string
	requestCount   int // per-sport, keyed by sport_api name
	rateLimitAfter int // return 429 after this many requests (0 = unlimited)
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

func (s *state) incrementAndCheck(sport string) (bool, int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.requestCount++
	count := s.requestCount
	if s.rateLimitAfter > 0 && count > s.rateLimitAfter {
		return false, count
	}
	return true, count
}

var globalState = &state{scenario: "normal"}

func main() {
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
		After int `json:"after"` // return 429 after this many requests; 0 = never
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	globalState.mu.Lock()
	globalState.rateLimitAfter = req.After
	globalState.mu.Unlock()
	log.Printf("[mock-apisports] Rate limit set: after %d requests", req.After)
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{"rate_limit_after": req.After, "status": "ok"})
}

func controlResetHandler(w http.ResponseWriter, r *http.Request) {
	globalState.mu.Lock()
	globalState.requestCount = 0
	globalState.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{"status": "ok", "message": "counters reset"})
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
	if ok, count := globalState.incrementAndCheck(sportFromHost(r.Host)); !ok {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("x-ratelimit-requests-remaining", "0")
		w.WriteHeader(http.StatusTooManyRequests)
		json.NewEncoder(w).Encode(map[string]any{
			"get":    "error",
			"errors": map[string]string{"rate_limit": "rate limit exceeded"},
		})
		log.Printf("[mock-apisports] Rate limited (request #%d)", count)
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

	// Check query param first (for mock usage), then fall back to Host header
	sport := r.URL.Query().Get("sport")
	if sport == "" {
		sport = sportFromHost(r.Host)
	}
	ep := endpointFromPath(r.URL.Path)
	resp := buildResponse(sport, ep, "", scenario)

	w.Header().Set("Content-Type", "application/json")
	// Simulate rate limit headers
	w.Header().Set("x-ratelimit-requests-remaining", "7420")
	w.Header().Set("x-ratelimit-requests-limit", "7500")
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
		return standingsResponse(sport, endpoint, scenario)
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
func standingsResponse(sport, endpoint, scenario string) map[string]any {
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
		return basketballStandingsResponse(endpoint)
	case "baseball":
		return baseballStandingsResponse(endpoint)
	case "american-football":
		return americanFootballStandingsResponse(endpoint)
	case "football":
		return footballStandingsResponse(endpoint)
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
		"get":      endpoint,
		"results":  0,
		"response": []any{},
	}
}

func basketballStandingsResponse(endpoint string) map[string]any {
	return map[string]any{
		"get":        "standings",
		"parameters": map[string]any{"league": "12", "season": "2024-2025"},
		"errors":     []any{},
		"results":    1,
		"response": []any{
			// NBA - Regular Season
			[]any{
				// Eastern Conference
				map[string]any{
					"position": 1,
					"stage":    "NBA - Regular Season",
					"group":    map[string]any{"name": "Eastern Conference", "points": nil},
					"team":     map[string]any{"id": 136, "name": "Boston Celtics", "logo": "https://media.api-sports.io/basketball/teams/136.png"},
					"league":   map[string]any{"id": 12, "name": "NBA", "type": "League", "season": "2024-2025", "logo": "https://media.api-sports.io/basketball/leagues/12.png"},
					"country":  map[string]any{"id": 5, "name": "USA", "code": "US", "flag": "https://media.api-football.com/flags/us.svg"},
					"games":    map[string]any{"played": 30, "win": map[string]any{"total": 22, "percentage": "0.733"}, "lose": map[string]any{"total": 8, "percentage": "0.267"}},
					"points":   map[string]any{"for": 3450, "against": 3180},
					"form":     "W-W-W-W-L",
				},
				map[string]any{
					"position": 1,
					"stage":    "NBA - Regular Season",
					"group":    map[string]any{"name": "Atlantic Division"},
					"team":     map[string]any{"id": 136, "name": "Boston Celtics", "logo": "https://media.api-sports.io/basketball/teams/136.png"},
					"league":   map[string]any{"id": 12, "name": "NBA", "type": "League", "season": "2024-2025", "logo": "https://media.api-sports.io/basketball/leagues/12.png"},
					"country":  map[string]any{"id": 5, "name": "USA", "code": "US", "flag": "https://media.api-football.com/flags/us.svg"},
					"games":    map[string]any{"played": 30, "win": map[string]any{"total": 22, "percentage": "0.733"}, "lose": map[string]any{"total": 8, "percentage": "0.267"}},
					"points":   map[string]any{"for": 3450, "against": 3180},
					"form":     "W-W-W-W-L",
				},
				map[string]any{
					"position": 8,
					"stage":    "NBA - Regular Season",
					"group":    map[string]any{"name": "Eastern Conference", "points": nil},
					"team":     map[string]any{"id": 137, "name": "Cleveland Cavaliers", "logo": "https://media.api-sports.io/basketball/teams/137.png"},
					"league":   map[string]any{"id": 12, "name": "NBA", "type": "League", "season": "2024-2025", "logo": "https://media.api-sports.io/basketball/leagues/12.png"},
					"country":  map[string]any{"id": 5, "name": "USA", "code": "US", "flag": "https://media.api-football.com/flags/us.svg"},
					"games":    map[string]any{"played": 30, "win": map[string]any{"total": 18, "percentage": "0.600"}, "lose": map[string]any{"total": 12, "percentage": "0.400"}},
					"points":   map[string]any{"for": 3420, "against": 3350},
					"form":     "L-W-W-L-W",
				},
				map[string]any{
					"position": 2,
					"stage":    "NBA - Regular Season",
					"group":    map[string]any{"name": "Central Division"},
					"team":     map[string]any{"id": 137, "name": "Cleveland Cavaliers", "logo": "https://media.api-sports.io/basketball/teams/137.png"},
					"league":   map[string]any{"id": 12, "name": "NBA", "type": "League", "season": "2024-2025", "logo": "https://media.api-sports.io/basketball/leagues/12.png"},
					"country":  map[string]any{"id": 5, "name": "USA", "code": "US", "flag": "https://media.api-football.com/flags/us.svg"},
					"games":    map[string]any{"played": 30, "win": map[string]any{"total": 18, "percentage": "0.600"}, "lose": map[string]any{"total": 12, "percentage": "0.400"}},
					"points":   map[string]any{"for": 3420, "against": 3350},
					"form":     "L-W-W-L-W",
				},
				map[string]any{
					"position": 12,
					"stage":    "NBA - Regular Season",
					"group":    map[string]any{"name": "Eastern Conference", "points": nil},
					"team":     map[string]any{"id": 138, "name": "Washington Wizards", "logo": "https://media.api-sports.io/basketball/teams/138.png"},
					"league":   map[string]any{"id": 12, "name": "NBA", "type": "League", "season": "2024-2025", "logo": "https://media.api-sports.io/basketball/leagues/12.png"},
					"country":  map[string]any{"id": 5, "name": "USA", "code": "US", "flag": "https://media.api-football.com/flags/us.svg"},
					"games":    map[string]any{"played": 30, "win": map[string]any{"total": 6, "percentage": "0.200"}, "lose": map[string]any{"total": 24, "percentage": "0.800"}},
					"points":   map[string]any{"for": 2850, "against": 3250},
					"form":     "L-L-L-L-L",
				},
				map[string]any{
					"position": 5,
					"stage":    "NBA - Regular Season",
					"group":    map[string]any{"name": "Southeast Division"},
					"team":     map[string]any{"id": 138, "name": "Washington Wizards", "logo": "https://media.api-sports.io/basketball/teams/138.png"},
					"league":   map[string]any{"id": 12, "name": "NBA", "type": "League", "season": "2024-2025", "logo": "https://media.api-sports.io/basketball/leagues/12.png"},
					"country":  map[string]any{"id": 5, "name": "USA", "code": "US", "flag": "https://media.api-football.com/flags/us.svg"},
					"games":    map[string]any{"played": 30, "win": map[string]any{"total": 6, "percentage": "0.200"}, "lose": map[string]any{"total": 24, "percentage": "0.800"}},
					"points":   map[string]any{"for": 2850, "against": 3250},
					"form":     "L-L-L-L-L",
				},
				// Western Conference
				map[string]any{
					"position": 2,
					"stage":    "NBA - Regular Season",
					"group":    map[string]any{"name": "Western Conference", "points": nil},
					"team":     map[string]any{"id": 139, "name": "Los Angeles Lakers", "logo": "https://media.api-sports.io/basketball/teams/139.png"},
					"league":   map[string]any{"id": 12, "name": "NBA", "type": "League", "season": "2024-2025", "logo": "https://media.api-sports.io/basketball/leagues/12.png"},
					"country":  map[string]any{"id": 5, "name": "USA", "code": "US", "flag": "https://media.api-football.com/flags/us.svg"},
					"games":    map[string]any{"played": 30, "win": map[string]any{"total": 21, "percentage": "0.700"}, "lose": map[string]any{"total": 9, "percentage": "0.300"}},
					"points":   map[string]any{"for": 3480, "against": 3280},
					"form":     "W-W-L-W-W",
				},
				map[string]any{
					"position": 1,
					"stage":    "NBA - Regular Season",
					"group":    map[string]any{"name": "Pacific Division"},
					"team":     map[string]any{"id": 139, "name": "Los Angeles Lakers", "logo": "https://media.api-sports.io/basketball/teams/139.png"},
					"league":   map[string]any{"id": 12, "name": "NBA", "type": "League", "season": "2024-2025", "logo": "https://media.api-sports.io/basketball/leagues/12.png"},
					"country":  map[string]any{"id": 5, "name": "USA", "code": "US", "flag": "https://media.api-football.com/flags/us.svg"},
					"games":    map[string]any{"played": 30, "win": map[string]any{"total": 21, "percentage": "0.700"}, "lose": map[string]any{"total": 9, "percentage": "0.300"}},
					"points":   map[string]any{"for": 3480, "against": 3280},
					"form":     "W-W-L-W-W",
				},
				map[string]any{
					"position": 3,
					"stage":    "NBA - Regular Season",
					"group":    map[string]any{"name": "Western Conference", "points": nil},
					"team":     map[string]any{"id": 140, "name": "Phoenix Suns", "logo": "https://media.api-sports.io/basketball/teams/140.png"},
					"league":   map[string]any{"id": 12, "name": "NBA", "type": "League", "season": "2024-2025", "logo": "https://media.api-sports.io/basketball/leagues/12.png"},
					"country":  map[string]any{"id": 5, "name": "USA", "code": "US", "flag": "https://media.api-football.com/flags/us.svg"},
					"games":    map[string]any{"played": 30, "win": map[string]any{"total": 19, "percentage": "0.633"}, "lose": map[string]any{"total": 11, "percentage": "0.367"}},
					"points":   map[string]any{"for": 3400, "against": 3300},
					"form":     "W-L-W-W-L",
				},
				map[string]any{
					"position": 1,
					"stage":    "NBA - Regular Season",
					"group":    map[string]any{"name": "Pacific Division"},
					"team":     map[string]any{"id": 140, "name": "Phoenix Suns", "logo": "https://media.api-sports.io/basketball/teams/140.png"},
					"league":   map[string]any{"id": 12, "name": "NBA", "type": "League", "season": "2024-2025", "logo": "https://media.api-sports.io/basketball/leagues/12.png"},
					"country":  map[string]any{"id": 5, "name": "USA", "code": "US", "flag": "https://media.api-football.com/flags/us.svg"},
					"games":    map[string]any{"played": 30, "win": map[string]any{"total": 19, "percentage": "0.633"}, "lose": map[string]any{"total": 11, "percentage": "0.367"}},
					"points":   map[string]any{"for": 3400, "against": 3300},
					"form":     "W-L-W-W-L",
				},
				map[string]any{
					"position": 10,
					"stage":    "NBA - Regular Season",
					"group":    map[string]any{"name": "Western Conference", "points": nil},
					"team":     map[string]any{"id": 141, "name": "Portland Trail Blazers", "logo": "https://media.api-sports.io/basketball/teams/141.png"},
					"league":   map[string]any{"id": 12, "name": "NBA", "type": "League", "season": "2024-2025", "logo": "https://media.api-sports.io/basketball/leagues/12.png"},
					"country":  map[string]any{"id": 5, "name": "USA", "code": "US", "flag": "https://media.api-football.com/flags/us.svg"},
					"games":    map[string]any{"played": 30, "win": map[string]any{"total": 10, "percentage": "0.333"}, "lose": map[string]any{"total": 20, "percentage": "0.667"}},
					"points":   map[string]any{"for": 3050, "against": 3280},
					"form":     "L-L-W-L-L",
				},
				map[string]any{
					"position": 5,
					"stage":    "NBA - Regular Season",
					"group":    map[string]any{"name": "Northwest Division"},
					"team":     map[string]any{"id": 141, "name": "Portland Trail Blazers", "logo": "https://media.api-sports.io/basketball/teams/141.png"},
					"league":   map[string]any{"id": 12, "name": "NBA", "type": "League", "season": "2024-2025", "logo": "https://media.api-sports.io/basketball/leagues/12.png"},
					"country":  map[string]any{"id": 5, "name": "USA", "code": "US", "flag": "https://media.api-football.com/flags/us.svg"},
					"games":    map[string]any{"played": 30, "win": map[string]any{"total": 10, "percentage": "0.333"}, "lose": map[string]any{"total": 20, "percentage": "0.667"}},
					"points":   map[string]any{"for": 3050, "against": 3280},
					"form":     "L-L-W-L-L",
				},
			},
		},
	}
}

func baseballStandingsResponse(endpoint string) map[string]any {
	return map[string]any{
		"get":        "standings",
		"parameters": map[string]any{"league": "1", "season": "2024"},
		"errors":     []any{},
		"results":    2,
		"response": []any{
			// MLB - Regular Season
			[]any{
				map[string]any{
					"position": 1,
					"stage":    "MLB - Regular Season",
					"group":    map[string]any{"name": "American League"},
					"team":     map[string]any{"id": 1, "name": "New York Yankees", "logo": "https://media.api-sports.io/baseball/teams/1.png"},
					"league":   map[string]any{"id": 1, "name": "MLB", "type": "League", "logo": "https://media.api-sports.io/baseball/leagues/1.png", "season": 2024},
					"country":  map[string]any{"id": 1, "name": "USA", "code": "US", "flag": "https://media.api-sports.io/flags/us.svg"},
					"games":    map[string]any{"played": 130, "win": map[string]any{"total": 72, "percentage": "0.554"}, "lose": map[string]any{"total": 58, "percentage": "0.446"}},
					"points":   map[string]any{"for": 512, "against": 458},
					"form":     "W-L-W-W-W",
				},
				map[string]any{
					"position": 2,
					"stage":    "MLB - Regular Season",
					"group":    map[string]any{"name": "AL East"},
					"team":     map[string]any{"id": 1, "name": "New York Yankees", "logo": "https://media.api-sports.io/baseball/teams/1.png"},
					"league":   map[string]any{"id": 1, "name": "MLB", "type": "League", "logo": "https://media.api-sports.io/baseball/leagues/1.png", "season": 2024},
					"country":  map[string]any{"id": 1, "name": "USA", "code": "US", "flag": "https://media.api-sports.io/flags/us.svg"},
					"games":    map[string]any{"played": 32, "win": map[string]any{"total": 18, "percentage": "0.562"}, "lose": map[string]any{"total": 14, "percentage": "0.438"}},
					"points":   map[string]any{"for": 125, "against": 108},
					"form":     "W-L-W-W-W",
				},
				map[string]any{
					"position": 5,
					"stage":    "MLB - Regular Season",
					"group":    map[string]any{"name": "American League"},
					"team":     map[string]any{"id": 5, "name": "Boston Red Sox", "logo": "https://media.api-sports.io/baseball/teams/5.png"},
					"league":   map[string]any{"id": 1, "name": "MLB", "type": "League", "logo": "https://media.api-sports.io/baseball/leagues/1.png", "season": 2024},
					"country":  map[string]any{"id": 1, "name": "USA", "code": "US", "flag": "https://media.api-sports.io/flags/us.svg"},
					"games":    map[string]any{"played": 130, "win": map[string]any{"total": 62, "percentage": "0.477"}, "lose": map[string]any{"total": 68, "percentage": "0.523"}},
					"points":   map[string]any{"for": 485, "against": 512},
					"form":     nil,
				},
				map[string]any{
					"position": 2,
					"stage":    "MLB - Regular Season",
					"group":    map[string]any{"name": "AL East"},
					"team":     map[string]any{"id": 5, "name": "Boston Red Sox", "logo": "https://media.api-sports.io/baseball/teams/5.png"},
					"league":   map[string]any{"id": 1, "name": "MLB", "type": "League", "logo": "https://media.api-sports.io/baseball/leagues/1.png", "season": 2024},
					"country":  map[string]any{"id": 1, "name": "USA", "code": "US", "flag": "https://media.api-sports.io/flags/us.svg"},
					"games":    map[string]any{"played": 32, "win": map[string]any{"total": 18, "percentage": "0.562"}, "lose": map[string]any{"total": 14, "percentage": "0.438"}},
					"points":   map[string]any{"for": 125, "against": 108},
					"form":     "W-L-W-W-L",
				},
				map[string]any{
					"position": 3,
					"stage":    "MLB - Regular Season",
					"group":    map[string]any{"name": "National League"},
					"team":     map[string]any{"id": 20, "name": "Los Angeles Dodgers", "logo": "https://media.api-sports.io/baseball/teams/20.png"},
					"league":   map[string]any{"id": 1, "name": "MLB", "type": "League", "logo": "https://media.api-sports.io/baseball/leagues/1.png", "season": 2024},
					"country":  map[string]any{"id": 1, "name": "USA", "code": "US", "flag": "https://media.api-sports.io/flags/us.svg"},
					"games":    map[string]any{"played": 130, "win": map[string]any{"total": 78, "percentage": "0.600"}, "lose": map[string]any{"total": 52, "percentage": "0.400"}},
					"points":   map[string]any{"for": 580, "against": 420},
					"form":     "W-W-W-W-L",
				},
				map[string]any{
					"position": 1,
					"stage":    "MLB - Regular Season",
					"group":    map[string]any{"name": "NL West"},
					"team":     map[string]any{"id": 20, "name": "Los Angeles Dodgers", "logo": "https://media.api-sports.io/baseball/teams/20.png"},
					"league":   map[string]any{"id": 1, "name": "MLB", "type": "League", "logo": "https://media.api-sports.io/baseball/leagues/1.png", "season": 2024},
					"country":  map[string]any{"id": 1, "name": "USA", "code": "US", "flag": "https://media.api-sports.io/flags/us.svg"},
					"games":    map[string]any{"played": 32, "win": map[string]any{"total": 22, "percentage": "0.688"}, "lose": map[string]any{"total": 10, "percentage": "0.312"}},
					"points":   map[string]any{"for": 145, "against": 95},
					"form":     "W-W-W-W-L",
				},
			},
			// MLB - Spring Training
			[]any{
				map[string]any{
					"position": 5,
					"stage":    "MLB - Spring Training",
					"group":    map[string]any{"name": "American League"},
					"team":     map[string]any{"id": 5, "name": "Boston Red Sox", "logo": "https://media.api-sports.io/baseball/teams/5.png"},
					"league":   map[string]any{"id": 1, "name": "MLB", "type": "League", "logo": "https://media.api-sports.io/baseball/leagues/1.png", "season": 2024},
					"country":  map[string]any{"id": 1, "name": "USA", "code": "US", "flag": "https://media.api-sports.io/flags/us.svg"},
					"games":    map[string]any{"played": 15, "win": map[string]any{"total": 8, "percentage": "0.533"}, "lose": map[string]any{"total": 7, "percentage": "0.467"}},
					"points":   map[string]any{"for": 72, "against": 65},
					"form":     nil,
				},
				map[string]any{
					"position": 8,
					"stage":    "MLB - Spring Training",
					"group":    map[string]any{"name": "National League"},
					"team":     map[string]any{"id": 20, "name": "Los Angeles Dodgers", "logo": "https://media.api-sports.io/baseball/teams/20.png"},
					"league":   map[string]any{"id": 1, "name": "MLB", "type": "League", "logo": "https://media.api-sports.io/baseball/leagues/1.png", "season": 2024},
					"country":  map[string]any{"id": 1, "name": "USA", "code": "US", "flag": "https://media.api-sports.io/flags/us.svg"},
					"games":    map[string]any{"played": 14, "win": map[string]any{"total": 9, "percentage": "0.643"}, "lose": map[string]any{"total": 5, "percentage": "0.357"}},
					"points":   map[string]any{"for": 68, "against": 52},
					"form":     nil,
				},
			},
		},
	}
}

func americanFootballStandingsResponse(endpoint string) map[string]any {
	return map[string]any{
		"get":      endpoint,
		"results":  0,
		"response": []any{},
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
								"away":        map[string]any{"played": 9, "win": 0, "draw": 2, "lose": 7, "goals": map[string]any{"for": 4, "against": 18}},
								"update":      "2024-12-15T00:00:00+00:00",
							},
						},
					},
				},
			},
		},
	}
}

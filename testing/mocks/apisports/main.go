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
	mu              sync.RWMutex
	scenario        string
	requestCount    int // per-sport, keyed by sport_api name
	rateLimitAfter  int // return 429 after this many requests (0 = unlimited)
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

	// Return sport-specific canned data
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
		"get":      endpoint,
		"results":  2,
		"paging":   map[string]int{"current": 1, "total": 1},
		"response": []any{
			map[string]any{
				"fixture": map[string]any{
					"id":       1,
					"timestamp": time.Now().Unix(),
					"date":     now,
					"status":   map[string]any{"short": "IN1", "long": "1st Half", "elapsed": 23},
					"venue":    map[string]any{"name": "Mock Stadium"},
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
					"id":       2,
					"timestamp": time.Now().Add(2 * time.Hour).Unix(),
					"date":     time.Now().Add(2 * time.Hour).Format(time.RFC3339),
					"status":   map[string]any{"short": "NS", "long": "Not Started"},
					"venue":    map[string]any{"name": "Mock Arena"},
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
		"get":      endpoint,
		"results":  1,
		"response": []any{
			map[string]any{
				"id":      1001,
				"date":    time.Now().Format(time.RFC3339),
				"timestamp": time.Now().Unix(),
				"status":  map[string]any{"short": "Q3", "long": "3rd Quarter", "timer": "5:42"},
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
		"get":      endpoint,
		"results":  1,
		"response": []any{
			map[string]any{
				"game": map[string]any{
					"id":   2001,
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
		"get":      endpoint,
		"results":  1,
		"response": []any{
			map[string]any{
				"id":      3001,
				"date":    time.Now().Format(time.RFC3339),
				"timestamp": time.Now().Unix(),
				"status":  map[string]any{"short": "3rd", "long": "3rd Period", "timer": "12:30"},
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
		"get":      endpoint,
		"results":  1,
		"response": []any{
			map[string]any{
				"id":      4001,
				"date":    time.Now().Format(time.RFC3339),
				"timestamp": time.Now().Unix(),
				"status":  map[string]any{"short": "Inn 5", "long": "Middle 5th"},
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
		"get":      endpoint,
		"results":  1,
		"response": []any{
			map[string]any{
				"id":      5001,
				"date":    time.Now().Format(time.RFC3339),
				"timestamp": time.Now().Unix(),
				"status":  map[string]any{"short": "1H", "long": "First Half", "timer": "35:00"},
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
		"get":      endpoint,
		"results":  1,
		"response": []any{
			map[string]any{
				"id":      6001,
				"date":    time.Now().Format(time.RFC3339),
				"timestamp": time.Now().Unix(),
				"status":  map[string]any{"short": "S2", "long": "Set 2", "timer": ""},
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
		"get":      endpoint,
		"results":  1,
		"response": []any{
			map[string]any{
				"id":      7001,
				"date":    time.Now().Format(time.RFC3339),
				"timestamp": time.Now().Unix(),
				"status":  map[string]any{"short": "2nd", "long": "2nd Half", "timer": "45:00"},
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
		"get":      endpoint,
		"results":  1,
		"response": []any{
			map[string]any{
				"game": map[string]any{
					"id":       8001,
					"timestamp": time.Now().Unix(),
					"date":     time.Now().Format(time.RFC3339),
					"status":   map[string]any{"short": "Q3", "long": "3rd Quarter"},
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
		"get":      endpoint,
		"results":  1,
		"response": []any{
			map[string]any{
				"id":      9001,
				"date":    time.Now().Format(time.RFC3339),
				"timestamp": time.Now().Unix(),
				"status":  map[string]any{"short": "F", "long": "Finished"},
				"fighters": map[string]any{
					"first": map[string]any{"id": 90, "name": "Fighter Alpha", "logo": "https://example.com/fa.png"},
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
		"get":      endpoint,
		"results":  1,
		"response": []any{
			map[string]any{
				"id":      10001,
				"date":    time.Now().Format("2006-01-02"),
				"type":    "Race",
				"status":  "Live",
				"competition": map[string]any{"id": 100, "name": "Mock Grand Prix"},
				"circuit": map[string]any{"name": "Mock Circuit International"},
			},
		},
	}
}

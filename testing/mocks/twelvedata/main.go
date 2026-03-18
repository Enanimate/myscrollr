package main

import (
	"encoding/json"
	"fmt"
	"log"
	"math"
	"math/rand"
	"net/http"
	"os"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

var upgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool { return true },
}

type state struct {
	mu         sync.RWMutex
	scenario   string
	basePrices map[string]float64
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

func (s *state) getBasePrice(symbol string) float64 {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if p, ok := s.basePrices[symbol]; ok {
		return p
	}
	defaults := map[string]float64{
		"AAPL":  175.00, "MSFT": 380.00, "GOOGL": 140.00,
		"AMZN":  180.00, "NVDA":  880.00, "TSLA":  250.00,
		"META":  500.00, "SPY":   510.00, "QQQ":   440.00,
		"IWM":   200.00,
	}
	if p, ok := defaults[symbol]; ok {
		return p
	}
	return 100.00
}

func (s *state) setBasePrice(symbol string, price float64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.basePrices[symbol] = price
}

var globalState = &state{
	scenario:   "normal",
	basePrices: make(map[string]float64),
}

func main() {
	restPort := os.Getenv("REST_PORT")
	if restPort == "" {
		restPort = "9002"
	}
	wsPort := os.Getenv("WS_PORT")
	if wsPort == "" {
		wsPort = "9003"
	}

	// WebSocket server on its own port
	wsMux := http.NewServeMux()
	wsMux.HandleFunc("/", wsHandler)
	wsMux.HandleFunc("/control/scenario", controlScenarioHandler)
	wsMux.HandleFunc("/control/price", controlPriceHandler)
	go func() {
		log.Printf("[mock-twelvedata] WebSocket server on :%s", wsPort)
		http.ListenAndServe(":"+wsPort, wsMux)
	}()

	// REST server
	restMux := http.NewServeMux()
	restMux.HandleFunc("/health", healthHandler)
	restMux.HandleFunc("/quote", quoteHandler)

	log.Printf("[mock-twelvedata] REST server on :%s", restPort)
	log.Fatalf("[mock-twelvedata] REST error: %v", http.ListenAndServe(":"+restPort, restMux))
}

func healthHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{"status": "ok", "service": "mock-twelvedata"})
}

func quoteHandler(w http.ResponseWriter, r *http.Request) {
	symbol := r.URL.Query().Get("symbol")
	if symbol == "" {
		http.Error(w, "symbol is required", http.StatusBadRequest)
		return
	}

	base := globalState.getBasePrice(symbol)

	// Simulate price variation: ±2% from base
	variation := (rand.Float64()*0.04 - 0.02)
	close := base * (1 + variation)
	change := close - base
	pctChange := (change / base) * 100

	resp := map[string]any{
		"symbol":         symbol,
		"name":           symbol + " Inc.",
		"exchange":       "NASDAQ",
		"close":          fmt.Sprintf("%.2f", close),
		"previous_close": fmt.Sprintf("%.2f", base),
		"open":           fmt.Sprintf("%.2f", base*(1+rand.Float64()*0.02-0.01)),
		"high":           fmt.Sprintf("%.2f", close*1.02),
		"low":            fmt.Sprintf("%.2f", close*0.98),
		"volume":         fmt.Sprintf("%d", int(50_000_000+rand.Float64()*10_000_000)),
		"change":         fmt.Sprintf("%.2f", change),
		"percent_change": fmt.Sprintf("%.2f", pctChange),
		"average_volume": "45000000",
		"market_cap":     fmt.Sprintf("%.0f", base*1_000_000_000),
		"pe_ratio":       fmt.Sprintf("%.2f", 25.0+rand.Float64()*5),
		"52week_high":    fmt.Sprintf("%.2f", base*1.3),
		"52week_low":     fmt.Sprintf("%.2f", base*0.7),
		"status":         "ok",
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

// ---------------------------------------------------------------------------
// WebSocket server
// ---------------------------------------------------------------------------

func wsHandler(w http.ResponseWriter, r *http.Request) {
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("[mock-twelvedata] WS upgrade error: %v", err)
		return
	}
	defer conn.Close()

	log.Printf("[mock-twelvedata] WS client connected from %s", r.RemoteAddr)

	// Read subscription message
	_, msg, err := conn.ReadMessage()
	if err != nil {
		log.Printf("[mock-twelvedata] WS read error: %v", err)
		return
	}

	var sub struct {
		Action string `json:"action"`
		Params struct {
			Symbols string `json:"symbols"`
		} `json:"params"`
	}
	json.Unmarshal(msg, &sub)

	symbols := parseSymbols(sub.Params.Symbols)
	log.Printf("[mock-twelvedata] Subscribed to %d symbols: %v", len(symbols), symbols)

	// Track price per symbol
	prices := make(map[string]float64)
	for _, sym := range symbols {
		prices[sym] = globalState.getBasePrice(sym)
	}

	msgCount := 0
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			msgCount++
			scenario := globalState.getScenario()

			switch scenario {
			case "normal":
				sendPriceUpdates(conn, prices, symbols, false)
			case "spike":
				if msgCount == 5 {
					// Spike on message 5
					sendPriceUpdates(conn, prices, symbols, true)
				} else {
					sendPriceUpdates(conn, prices, symbols, false)
				}
			case "disconnect":
				if msgCount == 3 {
					log.Printf("[mock-twelvedata] Disconnecting per 'disconnect' scenario")
					return
				}
				sendPriceUpdates(conn, prices, symbols, false)
			case "slow":
				// Slow scenario is handled by the ticker interval (already 500ms)
				sendPriceUpdates(conn, prices, symbols, false)
			case "empty":
				// Send nothing
			default:
				sendPriceUpdates(conn, prices, symbols, false)
			}

		case <-r.Context().Done():
			return
		}
	}
}

func sendPriceUpdates(conn *websocket.Conn, prices map[string]float64, symbols []string, spike bool) {
	for _, sym := range symbols {
		base := prices[sym]
		if spike {
			base *= 1.05
		}
		walk := (rand.Float64()*0.002 - 0.001)
		newPrice := base * (1 + walk)
		prices[sym] = newPrice

		event := map[string]any{
			"event":      "price",
			"symbol":     sym,
			"price":      math.Round(newPrice*100) / 100,
			"timestamp":  time.Now().Unix(),
			"day_volume": int(1_000_000 + rand.Float64()*500_000),
		}
		data, _ := json.Marshal(event)
		if err := conn.WriteMessage(websocket.TextMessage, data); err != nil {
			return
		}
	}
}

func parseSymbols(syms string) []string {
	if syms == "" {
		return nil
	}
	var out []string
	for _, s := range splitComma(syms) {
		out = append(out, s)
	}
	return out
}

func splitComma(s string) []string {
	var result []string
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == ',' {
			result = append(result, s[start:i])
			start = i + 1
		}
	}
	if start < len(s) {
		result = append(result, s[start:])
	}
	return result
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
	log.Printf("[mock-twelvedata] Scenario set to: %s", req.Scenario)
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{"scenario": req.Scenario, "status": "ok"})
}

func controlPriceHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST required", http.StatusMethodNotAllowed)
		return
	}
	var req struct {
		Symbol string  `json:"symbol"`
		Price  float64 `json:"price"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if req.Symbol == "" || req.Price <= 0 {
		http.Error(w, "symbol and price are required", http.StatusBadRequest)
		return
	}
	globalState.setBasePrice(req.Symbol, req.Price)
	log.Printf("[mock-twelvedata] Base price set: %s=%.2f", req.Symbol, req.Price)
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{"symbol": req.Symbol, "price": req.Price, "status": "ok"})
}

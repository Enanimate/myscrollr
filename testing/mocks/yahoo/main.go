package main

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log"
	"math/rand"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"
)

type state struct {
	mu             sync.RWMutex
	scenario       string
	accessTokens   map[string]bool // valid tokens
	refreshTokens  map[string]bool // valid refresh tokens
	tokenExpAfter int             // token expires after N API calls (0 = never)
	callCount     int             // total calls since last reset
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

func (s *state) checkAndRecord(token string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.callCount++

	if s.tokenExpAfter > 0 && s.callCount > s.tokenExpAfter {
		return false // token expired
	}
	return s.accessTokens[token]
}

func (s *state) isValidRefresh(token string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.refreshTokens[token]
}

func (s *state) resetCounters() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.callCount = 0
}

func (s *state) addToken(access, refresh string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.accessTokens[access] = true
	s.refreshTokens[refresh] = true
}

var globalState = &state{
	scenario:      "normal",
	accessTokens:  make(map[string]bool),
	refreshTokens: make(map[string]bool),
}

func main() {
	port := os.Getenv("PORT")
	if port == "" {
		port = "9005"
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/health", healthHandler)
	mux.HandleFunc("/oauth2/get_token", tokenHandler)
	mux.HandleFunc("/oauth2/request_auth", authHandler)
	mux.HandleFunc("/fantasy/v2/", fantasyAPIHandler)
	mux.HandleFunc("/control/scenario", controlScenarioHandler)
	mux.HandleFunc("/control/token-expires", controlTokenExpiresHandler)
	mux.HandleFunc("/control/reset", controlResetHandler)

	log.Printf("[mock-yahoo] Listening on :%s", port)
	log.Fatalf("[mock-yahoo] Error: %v", http.ListenAndServe(":"+port, mux))
}

func healthHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{"status": "ok", "service": "mock-yahoo"})
}

func tokenHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST required", http.StatusMethodNotAllowed)
		return
	}

	if err := r.ParseForm(); err != nil {
		http.Error(w, "parse error", http.StatusBadRequest)
		return
	}

	clientID := r.FormValue("client_id")
	clientSecret := r.FormValue("client_secret")
	refreshToken := r.FormValue("refresh_token")
	grantType := r.FormValue("grant_type")

	if clientID == "" || clientSecret == "" {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]any{
			"error":             "invalid_client",
			"error_description": "client_id and client_secret are required",
		})
		return
	}

	if grantType == "refresh_token" {
		if refreshToken == "" {
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(map[string]any{"error": "invalid_request"})
			return
		}

		// Issue a new token pair
		accessToken := generateToken()
		newRefresh := generateToken()
		globalState.addToken(accessToken, newRefresh)

		log.Printf("[mock-yahoo] Refreshed token for client_id=%s", clientID)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"access_token":  accessToken,
			"token_type":   "bearer",
			"refresh_token": newRefresh,
			"expires_in":   3600,
			"xoauth_yahoo_guid": "mock-guid-" + clientID,
		})
		return
	}

	// Authorization code exchange (first-time OAuth)
	accessToken := generateToken()
	refreshTok := generateToken()
	globalState.addToken(accessToken, refreshTok)

	log.Printf("[mock-yahoo] Issued token for client_id=%s", clientID)
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		"access_token":          accessToken,
		"token_type":            "bearer",
		"refresh_token":          refreshTok,
		"expires_in":             3600,
		"xoauth_yahoo_guid":     "mock-guid-" + clientID,
	})
}

func authHandler(w http.ResponseWriter, r *http.Request) {
	// Redirect back to callback URL with an auth code
	redirectURI := r.URL.Query().Get("redirect_uri")
	if redirectURI == "" {
		redirectURI = "http://localhost:8084/yahoo/callback"
	}
	code := base64.StdEncoding.EncodeToString([]byte(fmt.Sprintf("mock-code-%d", time.Now().UnixNano())))
	w.Header().Set("Location", redirectURI+"?code="+code+"&state="+r.URL.Query().Get("state"))
	w.WriteHeader(http.StatusFound)
}

func fantasyAPIHandler(w http.ResponseWriter, r *http.Request) {
	auth := r.Header.Get("Authorization")
	if !strings.HasPrefix(auth, "Bearer ") {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	token := strings.TrimPrefix(auth, "Bearer ")

	// Check token validity
	scenario := globalState.getScenario()
	if scenario == "token-expired" || scenario == "rate-limited" {
		if !globalState.checkAndRecord(token) {
			if scenario == "rate-limited" {
				w.Header().Set("Retry-After", "60")
				http.Error(w, "rate limit exceeded", http.StatusTooManyRequests)
				return
			}
			http.Error(w, " unauthorized", http.StatusUnauthorized)
			return
		}
	}

	path := r.URL.Path

	switch {
	case strings.Contains(path, "users;use_login=1"):
		handleUsers(w)
	case strings.Contains(path, "/leagues"):
		handleLeagues(w, path)
	case strings.Contains(path, "/standings"):
		handleStandings(w, path)
	case strings.Contains(path, "/scoreboard"):
		handleScoreboard(w, path)
	case strings.Contains(path, "/teams"):
		handleTeams(w, path)
	case strings.Contains(path, "/roster"):
		handleRoster(w, path)
	default:
		w.Header().Set("Content-Type", "application/xml")
		fmt.Fprint(w, fantasyXML("fantasy_content", ""))
	}
}

func handleUsers(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/xml")
	xml := `<?xml version="1.0" encoding="UTF-8"?>
<fantasy_content xml:lang="en-us" yahoo:uri="http://fantasysports.yahooapis.com/fantasy/v2/users;use_login=1" xmlns:yahoo="http://yahooapis.com/v1/base.rng" login="mockuser">
 <users>
  <user>
   <guid>mock-guid-12345</guid>
   <display_name>Mock Yahoo User</display_name>
   <given_name>Mock</given_name>
   <family_name>User</family_name>
   <email>mock@example.com</email>
   <sex>M</sex>
   <img_url>https://example.com/avatar.png</img_url>
  </user>
 </users>
</fantasy_content>`
	fmt.Fprint(w, xml)
}

func handleLeagues(w http.ResponseWriter, path string) {
	w.Header().Set("Content-Type", "application/xml")
	xml := `<?xml version="1.0" encoding="UTF-8"?>
<fantasy_content xml:lang="en-us" yahoo:uri="` + path + `" xmlns:yahoo="http://yahooapis.com/v1/base.rng">
 <users>
  <user>
   <guid>mock-guid-12345</guid>
   <games>
    <game>
     <game_key>449</game_key>
     <game_id>449</game_id>
     <name>Mock Fantasy Football</name>
     <code>nfl</code>
     <type>full</type>
     <url>http://football.fantasysports.yahoo.com/f1</url>
     <leagues>
      <league>
       <league_key>449.l.12345</league_key>
       <league_id>12345</league_id>
       <name>Mock League Alpha</name>
       <url>http://football.fantasysports.yahoo.com/f1/12345</url>
       <logo_url>https://example.com/league-logo.png</logo_url>
       <draft_status>postdraft</draft_status>
       <num_teams>10</num_teams>
       <scoring_type>head</scoring_type>
       <league_type>private</league_type>
       <current_week>10</current_week>
       <start_week>1</start_week>
       <end_week>17</end_week>
       <is_finished>0</is_finished>
       <season>2024</season>
      </league>
      <league>
       <league_key>449.l.67890</league_key>
       <league_id>67890</league_id>
       <name>Mock League Beta</name>
       <url>http://football.fantasysports.yahoo.com/f1/67890</url>
       <draft_status>postdraft</draft_status>
       <num_teams>8</num_teams>
       <scoring_type>head</scoring_type>
       <league_type>private</league_type>
       <current_week>10</current_week>
       <start_week>1</start_week>
       <end_week>17</end_week>
       <is_finished>0</is_finished>
       <season>2024</season>
      </league>
     </leagues>
    </game>
   </games>
  </user>
 </users>
</fantasy_content>`
	fmt.Fprint(w, xml)
}

func handleStandings(w http.ResponseWriter, path string) {
	w.Header().Set("Content-Type", "application/xml")
	xml := `<?xml version="1.0" encoding="UTF-8"?>
<fantasy_content xml:lang="en-us" yahoo:uri="` + path + `" xmlns:yahoo="http://yahooapis.com/v1/base.rng">
 <league>
  <league_key>449.l.12345</league_key>
  <name>Mock League Alpha</name>
  <standings>
   <teams>
    <team>
     <team_key>449.l.12345.t.1</team_key>
     <team_id>1</team_id>
     <name>First Place FC</name>
     <url>http://football.fantasysports.yahoo.com/f1/12345/1</url>
     <team_logos><team_logo><url>https://example.com/team1.png</url></team_logo></team_logos>
     <managers><manager><nickname>Manager1</nickname><guid>guid-1</guid></manager></managers>
     <team_standings>
      <rank>1</rank>
      <games_back>0.0</games_back>
      <points_for>1250.50</points_for>
      <points_against>980.25</points_against>
      <outcome_totals><wins>8</wins><losses>2</losses><ties>0</ties><percentage>.800</percentage></outcome_totals>
      <streak><type>W</type><value>3</value></streak>
     </team_standings>
     <clinch_playoffs>1</clinch_playoffs>
     <playoff_seed>1</playoff_seed>
    </team>
    <team>
     <team_key>449.l.12345.t.2</team_key>
     <team_id>2</team_id>
     <name>Second Place United</name>
     <url>http://football.fantasysports.yahoo.com/f1/12345/2</url>
     <team_logos><team_logo><url>https://example.com/team2.png</url></team_logo></team_logos>
     <managers><manager><nickname>Manager2</nickname><guid>guid-2</guid></manager></managers>
     <team_standings>
      <rank>2</rank>
      <games_back>1.5</games_back>
      <points_for>1180.00</points_for>
      <points_against>1050.00</points_against>
      <outcome_totals><wins>7</wins><losses>3</losses><ties>0</ties><percentage>.700</percentage></outcome_totals>
     </team_standings>
     <playoff_seed>2</playoff_seed>
    </team>
    <team>
     <team_key>449.l.12345.t.3</team_key>
     <team_id>3</team_id>
     <name>Third Place SC</name>
     <url>http://football.fantasysports.yahoo.com/f1/12345/3</url>
     <managers><manager><nickname>Manager3</nickname><guid>guid-3</guid></manager></managers>
     <team_standings>
      <rank>3</rank>
      <games_back>2.0</games_back>
      <points_for>1100.00</points_for>
      <points_against>1100.00</points_against>
      <outcome_totals><wins>6</wins><losses>4</losses><ties>0</ties><percentage>.600</percentage></outcome_totals>
     </team_standings>
     <playoff_seed>3</playoff_seed>
    </team>
   </teams>
  </standings>
 </league>
</fantasy_content>`
	fmt.Fprint(w, xml)
}

func handleScoreboard(w http.ResponseWriter, path string) {
	w.Header().Set("Content-Type", "application/xml")
	week := "10"
	if strings.Contains(path, "week=9") {
		week = "9"
	}
	xml := `<?xml version="1.0" encoding="UTF-8"?>
<fantasy_content xml:lang="en-us" yahoo:uri="` + path + `" xmlns:yahoo="http://yahooapis.com/v1/base.rng">
 <league>
  <league_key>449.l.12345</league_key>
  <scoreboard week="` + week + `">
   <matchups>
    <matchup>
     <week>` + week + `</week>
     <week_start>2024-11-07</week_start>
     <week_end>2024-11-13</week_end>
     <status>postevent</status>
     <is_playoffs>0</is_playoffs>
     <is_consolation>0</is_consolation>
     <is_tied>0</is_tied>
     <winner_team_key>449.l.12345.t.1</winner_team_key>
     <teams>
      <team>
       <team_key>449.l.12345.t.1</team_key>
       <team_id>1</team_id>
       <name>First Place FC</name>
       <team_points><total>125.50</total></team_points>
       <team_projected_points><total>110.00</total></team_projected_points>
      </team>
      <team>
       <team_key>449.l.12345.t.2</team_key>
       <team_id>2</team_id>
       <name>Second Place United</name>
       <team_points><total>118.75</total></team_points>
       <team_projected_points><total>120.00</total></team_projected_points>
      </team>
     </teams>
    </matchup>
    <matchup>
     <week>` + week + `</week>
     <week_start>2024-11-07</week_start>
     <week_end>2024-11-13</week_end>
     <status>postevent</status>
     <is_playoffs>0</is_playoffs>
     <is_consolation>0</is_consolation>
     <is_tied>0</is_tied>
     <winner_team_key>449.l.12345.t.3</winner_team_key>
     <teams>
      <team>
       <team_key>449.l.12345.t.3</team_key>
       <team_id>3</team_id>
       <name>Third Place SC</name>
       <team_points><total>105.25</total></team_points>
      </team>
      <team>
       <team_key>449.l.12345.t.4</team_key>
       <team_id>4</team_id>
       <name>Fourth Place FC</name>
       <team_points><total>98.00</total></team_points>
      </team>
     </teams>
    </matchup>
   </matchups>
  </scoreboard>
 </league>
</fantasy_content>`
	fmt.Fprint(w, xml)
}

func handleTeams(w http.ResponseWriter, path string) {
	w.Header().Set("Content-Type", "application/xml")
	xml := `<?xml version="1.0" encoding="UTF-8"?>
<fantasy_content xml:lang="en-us" yahoo:uri="` + path + `" xmlns:yahoo="http://yahooapis.com/v1/base.rng">
 <league>
  <league_key>449.l.12345</league_key>
  <teams>
   <team>
    <team_key>449.l.12345.t.1</team_key>
    <team_id>1</team_id>
    <name>First Place FC</name>
    <team_logos><team_logo><url>https://example.com/team1.png</url></team_logo></team_logos>
    <managers><manager><nickname>Manager1</nickname><guid>mock-guid-12345</guid></manager></managers>
   </team>
   <team>
    <team_key>449.l.12345.t.2</team_key>
    <team_id>2</team_id>
    <name>Second Place United</name>
    <team_logos><team_logo><url>https://example.com/team2.png</url></team_logo></team_logos>
    <managers><manager><nickname>Manager2</nickname><guid>guid-other</guid></manager></managers>
   </team>
  </teams>
 </league>
</fantasy_content>`
	fmt.Fprint(w, xml)
}

func handleRoster(w http.ResponseWriter, path string) {
	w.Header().Set("Content-Type", "application/xml")
	teamKey := "449.l.12345.t.1"
	if strings.Contains(path, "t.2") {
		teamKey = "449.l.12345.t.2"
	}
	xml := `<?xml version="1.0" encoding="UTF-8"?>
<fantasy_content xml:lang="en-us" yahoo:uri="` + path + `" xmlns:yahoo="http://yahooapis.com/v1/base.rng">
 <team>
  <team_key>` + teamKey + `</team_key>
  <name>Mock Team</name>
  <roster>
   <players>
    <player>
     <player_key>449.p.1</player_key>
     <player_id>1</player_id>
     <name><full>Mock Player 1</full><first>Mock</first><last>Player1</last></name>
     <editorial_team_abbr>MOC</editorial_team_abbr>
     <editorial_team_full_name>Mock City Mockers</editorial_team_full_name>
     <display_position>QB</display_position>
     <position_type>P</position_type>
     <selected_position><position>QB</position></selected_position>
     <eligible_positions><position>QB</position><position>W/R/T</position></eligible_positions>
     <status></status>
     <image_url>https://example.com/player1.png</image_url>
     <player_points><total>24.75</total></player_points>
    </player>
    <player>
     <player_key>449.p.2</player_key>
     <player_id>2</player_id>
     <name><full>Mock Player 2</full><first>Mock</first><last>Player2</last></name>
     <editorial_team_abbr>MOC</editorial_team_abbr>
     <editorial_team_full_name>Mock City Mockers</editorial_team_full_name>
     <display_position>RB</display_position>
     <position_type>O</position_type>
     <selected_position><position>RB</position></selected_position>
     <eligible_positions><position>RB</position><position>W/R/T</position><position>Flex</position></eligible_positions>
     <image_url>https://example.com/player2.png</image_url>
     <player_points><total>18.50</total></player_points>
    </player>
   </players>
  </roster>
 </team>
</fantasy_content>`
	fmt.Fprint(w, xml)
}

// ---------------------------------------------------------------------------
// Control API
// ---------------------------------------------------------------------------

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
	log.Printf("[mock-yahoo] Scenario set to: %s", req.Scenario)
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{"scenario": req.Scenario, "status": "ok"})
}

func controlTokenExpiresHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST required", http.StatusMethodNotAllowed)
		return
	}
	var req struct {
		After int `json:"after"` // token expires after N API calls
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	globalState.mu.Lock()
	globalState.tokenExpAfter = req.After
	globalState.mu.Unlock()
	log.Printf("[mock-yahoo] Token expires after %d calls", req.After)
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{"token_expires_after": req.After, "status": "ok"})
}

func controlResetHandler(w http.ResponseWriter, r *http.Request) {
	globalState.resetCounters()
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{"status": "ok", "message": "counters reset"})
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func generateToken() string {
	b := make([]byte, 32)
	rand.Read(b)
	return base64.URLEncoding.EncodeToString(b)
}

func fantasyXML(tag, content string) string {
	return fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<fantasy_content xml:lang="en-us" xmlns:yahoo="http://yahooapis.com/v1/base.rng">
 <%s>%s</%s>
</fantasy_content>`, tag, content, tag)
}

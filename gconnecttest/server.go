// Package gconnecttest is a fake Garmin for tests: SSO (widget and mobile login), diauth,
// the OAuth consumer file, the oauth-service OAuth1 to OAuth2 exchange, connectapi and the
// Personal API (key to token exchange, and data on the paths Garmin allows), all on one
// httptest server. Point a gconnect.Auth at it with Hosts(). It issues synthetic
// tokens only, and connectapi accepts only the tokens it issued.
package gconnecttest

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"maps"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/danihrndzld/gconnect"
)

// Control holds the knobs that pick how the fake answers. The zero value of each field is
// replaced by its default in New and Reset.
type Control struct {
	Password     string // default "right-password"
	MobileLogin  string // "ok" | "mfa" | "cloudflare" | "429" | "captcha"
	DIAuth       string // "ok" | "429" | "fail"
	RefreshFail  int    // when not 0, the DI refresh grant and the OAuth1 to OAuth2 exchange answer this status
	TokenTTL     int    // seconds, default 3600
	Widget429    bool
	WidgetStatus int
	APIKey       string         // the Personal API key the fake accepts, default "right-api-key"
	APIKeyStatus int            // when not 0, the Personal API token request answers this status
	DataStatus   map[string]int // path prefix -> status, every time; the longest matching prefix wins
	FailOnce     map[string]int // path prefix -> status, once; the longest matching prefix wins
}

// Call is one request the fake received.
type Call struct {
	Method, Path, Query string
	Header              http.Header
	Body                string
}

// Server is the fake. Its methods are safe for concurrent use.
type Server struct {
	mu      sync.Mutex
	control Control
	calls   []Call
	issued  int
	valid   map[string]bool
	persona map[string]bool // access tokens bought with an API key: data only on personalPaths
	routes  map[string]http.HandlerFunc
	srv     *httptest.Server
}

func defaults() Control {
	return Control{Password: "right-password", APIKey: "right-api-key", MobileLogin: "ok", DIAuth: "ok", TokenTTL: 3600, DataStatus: map[string]int{}, FailOnce: map[string]int{}}
}

// New starts a fake with the default knobs. Close it when the test ends.
func New() *Server {
	s := &Server{control: defaults(), valid: map[string]bool{}, persona: map[string]bool{}, routes: map[string]http.HandlerFunc{}}
	s.srv = httptest.NewServer(http.HandlerFunc(s.serve))
	return s
}

// URL is the fake's base URL, such as http://127.0.0.1:54321.
func (s *Server) URL() string { return s.srv.URL }

// Hosts points every Garmin host at the fake.
func (s *Server) Hosts() gconnect.Hosts { return gconnect.SameHost(s.srv.URL) }

// Close shuts the fake down; later requests fail with a network error.
func (s *Server) Close() { s.srv.Close() }

// Handle serves a connectapi data route at exactly path, or, when path ends in "/", at every
// path under it (as in http.ServeMux; the longest match wins). The fake still answers 401 to
// a token it did not issue, and DataStatus and FailOnce still apply.
func (s *Server) Handle(path string, h http.HandlerFunc) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.routes[path] = h
}

// Set changes the knobs under the lock, so it is safe while requests are in flight.
func (s *Server) Set(f func(*Control)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	f(&s.control)
}

// Control returns a copy of the knobs. Change them with Set.
func (s *Server) Control() Control {
	s.mu.Lock()
	defer s.mu.Unlock()
	c := s.control
	c.DataStatus = maps.Clone(c.DataStatus)
	c.FailOnce = maps.Clone(c.FailOnce)
	return c
}

// Calls returns the recorded requests to path, or all of them when path is "".
func (s *Server) Calls(path string) []Call {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []Call
	for _, c := range s.calls {
		if path == "" || c.Path == path {
			out = append(out, c)
		}
	}
	return out
}

// Reset restores the default knobs and clears the calls. Issued tokens and Handle routes stay.
func (s *Server) Reset() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.control = defaults()
	s.calls = nil
}

// JWT builds an unsigned token whose payload is claims, the shape diauth issues.
func JWT(claims any) string {
	b64 := func(v any) string {
		b, _ := json.Marshal(v)
		return base64.RawURLEncoding.EncodeToString(b)
	}
	return b64(map[string]string{"alg": "RS256", "typ": "JWT"}) + "." + b64(claims) + ".sig"
}

func send(w http.ResponseWriter, status int, body any, headers ...string) {
	text, isHTML := "", false
	switch b := body.(type) {
	case nil:
	case string:
		text, isHTML = b, true
	default:
		j, _ := json.Marshal(b)
		text = string(j)
	}
	if isHTML {
		w.Header().Set("Content-Type", "text/html")
	} else {
		w.Header().Set("Content-Type", "application/json")
	}
	for i := 0; i+1 < len(headers); i += 2 {
		w.Header().Set(headers[i], headers[i+1])
	}
	w.WriteHeader(status)
	if status != 204 {
		io.WriteString(w, text)
	}
}

type obj = map[string]any

func (s *Server) newDIToken(ttl int) obj {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.issued++
	access := JWT(obj{"client_id": "GARMIN_CONNECT_MOBILE_ANDROID_DI_2025Q2", "exp": time.Now().Unix() + int64(ttl), "n": s.issued})
	s.valid[access] = true
	return obj{"access_token": access, "token_type": "bearer", "refresh_token": fmt.Sprintf("refresh-%d", s.issued), "expires_in": ttl, "refresh_token_expires_in": 2592000, "scope": "x", "jti": fmt.Sprint(s.issued)}
}

// personalPaths are the path prefixes the real Personal API answered on 2026-10-24 (its
// documented endpoints). A Personal API token gets 404 anywhere else, as from Garmin.
var personalPaths = []string{
	"/usersummary-service/stats/stress/daily/", "/usersummary-service/stats/steps/daily/",
	"/usersummary-service/stats/hydration/daily/", "/usersummary-service/v2/dailysummaries",
	"/goal-service/goal/user/effective/weightgoal/", "/weight-service/weight/range/",
	"/nutrition-service/food/logs/", "/userprofile-service/userprofile/settings",
	"/sleep-service/sleep/dailySleep", "/wellness-service/wellness/bodyBattery/reports/daily",
	"/hrv-service/hrv/", "/biometric-service/heartRateZones/v2", "/fitnessage-service/fitnessage/v2/",
	"/periodichealth-service/menstrualcycle/",
	"/wellness-service/wellness/daily/spo2acclimation/", "/wellnessactivity-service/activity/summary/range/",
	"/activitylist-service/activities/search/activities", "/download-service/files/activity/",
}

func personalAllows(p string) bool {
	if strings.HasPrefix(p, "/activity-service/activity/") { // the activity and its workouts, not its splits
		return !strings.HasSuffix(p, "/splits")
	}
	if p == "/wellness-service/wellness/dailyHeartRate" { // only without the display name segment
		return true
	}
	for _, prefix := range personalPaths {
		if strings.HasPrefix(p, prefix) {
			return true
		}
	}
	return false
}

// serve answers one request.
func (s *Server) serve(w http.ResponseWriter, r *http.Request) {
	raw, _ := io.ReadAll(r.Body)
	body, p := string(raw), r.URL.Path
	s.mu.Lock()
	s.calls = append(s.calls, Call{Method: r.Method, Path: p, Query: r.URL.RawQuery, Header: r.Header.Clone(), Body: body})
	c := s.control
	s.mu.Unlock()
	form, _ := url.ParseQuery(body)

	switch {
	case p == "/mobile/api/login" && r.Method == "POST":
		switch c.MobileLogin {
		case "429":
			send(w, 429, obj{"error": "Too Many Requests"})
			return
		case "cloudflare":
			send(w, 403, "<html>Just a moment...</html>")
			return
		case "captcha":
			send(w, 200, obj{"responseStatus": obj{"type": "CAPTCHA_REQUIRED"}})
			return
		}
		var in struct{ Password string }
		_ = json.Unmarshal(raw, &in)
		switch {
		case in.Password != c.Password:
			send(w, 200, obj{"responseStatus": obj{"type": "INVALID_USERNAME_PASSWORD"}})
		case c.MobileLogin == "mfa":
			send(w, 200, obj{"responseStatus": obj{"type": "MFA_REQUIRED"}, "customerMfaInfo": obj{"mfaLastMethodUsed": "email"}})
		default:
			send(w, 200, obj{"responseStatus": obj{"type": "SUCCESSFUL"}, "serviceTicketId": "ST-mobile-1"})
		}
		return

	case p == "/di-oauth2-service/oauth/token" && r.Method == "POST":
		switch {
		case c.DIAuth == "429":
			send(w, 429, obj{"error": "rate"})
		case c.DIAuth == "fail":
			send(w, 400, obj{"error": "invalid_grant"})
		case form.Get("grant_type") == "refresh_token" && c.RefreshFail != 0:
			send(w, c.RefreshFail, obj{"error": "forced"})
		case form.Get("grant_type") == "refresh_token" && !strings.HasPrefix(form.Get("refresh_token"), "refresh-"):
			send(w, 400, obj{"error": "invalid_grant"})
		case form.Get("grant_type") != "refresh_token" && !strings.HasPrefix(form.Get("service_ticket"), "ST-"):
			send(w, 400, obj{"error": "invalid_grant"})
		default:
			send(w, 200, s.newDIToken(c.TokenTTL))
		}
		return

	case p == gconnect.PersonalTokenPath && r.Method == "POST":
		switch {
		case c.APIKeyStatus != 0:
			send(w, c.APIKeyStatus, obj{"error": "forced"})
		case r.Header.Get("Accept") == "application/json": // the token is text, as on the real service
			send(w, 406, obj{"error": "NotAcceptableException"})
		case r.Header.Get("Authorization") == "":
			send(w, 400, obj{"error": "Authentication missing", "status": 400})
		case r.Header.Get("Authorization") != "ApiKey "+c.APIKey:
			send(w, 401, nil)
		default:
			s.mu.Lock()
			s.issued++
			access := JWT(obj{"client_id": "personal-api", "exp": time.Now().Unix() + int64(c.TokenTTL), "n": s.issued})
			s.valid[access], s.persona[access] = true, true
			s.mu.Unlock()
			w.Header().Set("Content-Type", "text/plain")
			io.WriteString(w, access) // Garmin answers with the bare JWT, not JSON
		}
		return

	case p == "/oauth_consumer.json":
		send(w, 200, obj{"consumer_key": "ck", "consumer_secret": "cs"})
		return
	case p == "/sso/embed":
		send(w, 200, "<html>embed</html>", "Set-Cookie", "GARMIN-SSO=1; Path=/")
		return
	case p == "/sso/signin" && r.Method == "GET":
		send(w, 200, `<input name="_csrf" value="csrf-1">`)
		return
	case p == "/sso/signin" && r.Method == "POST":
		switch {
		case c.Widget429:
			send(w, 429, "<html>Too Many Requests</html>")
		case c.WidgetStatus != 0:
			send(w, c.WidgetStatus, "<html>Blocked</html>")
		case form.Get("password") != c.Password:
			send(w, 200, "<title>GARMIN Authentication Application</title> invalid")
		default:
			send(w, 200, `<title>Success</title><script>var response_url = "https://sso.garmin.com/sso/embed?ticket=ST-widget-1";</script>`)
		}
		return
	case p == "/oauth-service/oauth/preauthorized":
		send(w, 200, "oauth_token=ot&oauth_token_secret=os")
		return
	case p == "/oauth-service/oauth/exchange/user/2.0":
		if c.RefreshFail != 0 {
			send(w, c.RefreshFail, obj{"error": "forced"})
			return
		}
		s.mu.Lock()
		s.issued++
		access := fmt.Sprintf("legacy-%d", s.issued)
		s.valid[access] = true
		s.mu.Unlock()
		send(w, 200, obj{"access_token": access, "token_type": "Bearer", "refresh_token": "lr", "expires_in": c.TokenTTL, "refresh_token_expires_in": 2592000})
		return
	}

	// connectapi data endpoints
	s.mu.Lock()
	access := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	ok := s.valid[access]
	blocked := s.persona[access] && !personalAllows(p)
	once, always := 0, 0
	if prefix, ok2 := longest(s.control.FailOnce, p); ok && ok2 {
		once = s.control.FailOnce[prefix]
		delete(s.control.FailOnce, prefix)
	}
	if prefix, ok2 := longest(s.control.DataStatus, p); ok2 {
		always = s.control.DataStatus[prefix]
	}
	s.mu.Unlock()
	retry := func(status int) []string {
		if status == 429 {
			return []string{"Retry-After", "1"}
		}
		return nil
	}
	switch {
	case !ok:
		send(w, 401, obj{"message": "token not active"})
		return
	case blocked:
		send(w, 404, obj{"message": "Not Found"})
		return
	case once != 0:
		send(w, once, obj{"message": "forced once"}, retry(once)...)
		return
	}
	if always != 0 {
		send(w, always, obj{"message": "forced"}, retry(always)...)
		return
	}
	s.mu.Lock()
	h := s.routes[p]
	if h == nil {
		best := ""
		for prefix, rh := range s.routes {
			if strings.HasSuffix(prefix, "/") && strings.HasPrefix(p, prefix) && len(prefix) > len(best) {
				best, h = prefix, rh
			}
		}
	}
	s.mu.Unlock()
	if h != nil {
		h(w, r)
		return
	}
	if p == "/userprofile-service/socialProfile" { // a default profile, so Client.Profile works without a route
		send(w, 200, obj{"displayName": "displayName-redacted", "id": 1})
		return
	}
	send(w, 404, obj{"message": "fake garmin has no route for " + p + "?" + r.URL.RawQuery})
}

// longest returns the longest key of m that is a prefix of p.
func longest(m map[string]int, p string) (string, bool) {
	best, found := "", false
	for prefix := range m {
		if strings.HasPrefix(p, prefix) && (!found || len(prefix) > len(best)) {
			best, found = prefix, true
		}
	}
	return best, found
}

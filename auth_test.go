package gconnect_test

import (
	"context"
	"crypto/hmac"
	"crypto/sha1"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/danihrndzld/gconnect"
	"github.com/danihrndzld/gconnect/gconnecttest"
)

func fakeGarmin(t *testing.T) *gconnecttest.Server {
	t.Helper()
	srv := gconnecttest.New()
	t.Cleanup(srv.Close)
	return srv
}

func parse(t *testing.T, body string) *gconnect.Tokens {
	t.Helper()
	var tok gconnect.Tokens
	if err := json.Unmarshal([]byte(body), &tok); err != nil {
		t.Fatal(err)
	}
	return &tok
}

func posts(calls []gconnecttest.Call) int {
	n := 0
	for _, c := range calls {
		if c.Method == "POST" {
			n++
		}
	}
	return n
}

// front puts a server before srv. It answers the "METHOD /path" keys of override itself and
// passes every other request to srv, so a test can script a Garmin answer the fake has no
// knob for.
func front(t *testing.T, srv *gconnecttest.Server, override map[string]http.HandlerFunc) gconnect.Hosts {
	t.Helper()
	target, _ := url.Parse(srv.URL())
	proxy := httputil.NewSingleHostReverseProxy(target)
	fs := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if h := override[r.Method+" "+r.URL.Path]; h != nil {
			h(w, r)
			return
		}
		proxy.ServeHTTP(w, r)
	}))
	t.Cleanup(fs.Close)
	return gconnect.SameHost(fs.URL)
}

func reply(status int, body string) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(status)
		io.WriteString(w, body)
	}
}

// hangUp closes the connection without an answer: a network error for the client.
func hangUp(w http.ResponseWriter, _ *http.Request) {
	c, _, _ := w.(http.Hijacker).Hijack()
	c.Close()
}

const password = "right-password"

func TestLoginDecisionTable(t *testing.T) {
	t.Parallel()
	cases := []struct {
		id          string
		set         func(*gconnecttest.Control)
		password    string
		wantCode    gconnect.LoginCode // "" means success
		wantKind    gconnect.Kind
		widgetPosts int
		diCalls     int
	}{
		{"DT-R411 mobile and DI succeed: a DI login, no widget request", nil, password, "", gconnect.KindDI, 0, 1},
		{"DT-R412 wrong password on mobile: BadPassword, the chain stops", nil, "nope", gconnect.BadPassword, gconnect.KindNone, 0, 0},
		{"DT-R413 MFA on mobile: MFARequired, the chain stops", func(c *gconnecttest.Control) { c.MobileLogin = "mfa" }, password, gconnect.MFARequired, gconnect.KindNone, 0, 0},
		{"DT-R414 429 on mobile: the widget signs in, a legacy login", func(c *gconnecttest.Control) { c.MobileLogin = "429" }, password, "", gconnect.KindLegacy, 1, 0},
		{"DT-R415 Cloudflare 403 on mobile: the widget signs in, a legacy login", func(c *gconnecttest.Control) { c.MobileLogin = "cloudflare" }, password, "", gconnect.KindLegacy, 1, 0},
		{"DT-R416 captcha on mobile: the widget signs in, a legacy login", func(c *gconnecttest.Control) { c.MobileLogin = "captcha" }, password, "", gconnect.KindLegacy, 1, 0},
		{"DT-R417 DI refused for every client id: the widget signs in, a legacy login", func(c *gconnecttest.Control) { c.DIAuth = "fail" }, password, "", gconnect.KindLegacy, 1, 4},
		{"DT-R418 DI answers 429: RateLimited, no widget request", func(c *gconnecttest.Control) { c.DIAuth = "429" }, password, gconnect.RateLimited, gconnect.KindNone, 0, 1},
		{"DT-R419 429 on mobile and on the widget: RateLimited", func(c *gconnecttest.Control) { c.MobileLogin = "429"; c.Widget429 = true }, password, gconnect.RateLimited, gconnect.KindNone, 1, 0},
		{"DT-R420 both logins blocked (403): Blocked", func(c *gconnecttest.Control) { c.MobileLogin = "cloudflare"; c.WidgetStatus = 403 }, password, gconnect.Blocked, gconnect.KindNone, 1, 0},
		{"DT-R421 429 on mobile, wrong password on the widget: BadPassword", func(c *gconnecttest.Control) { c.MobileLogin = "429" }, "nope", gconnect.BadPassword, gconnect.KindNone, 1, 0},
		{"DT-R422 Cloudflare on mobile, the widget answers 500: Blocked", func(c *gconnecttest.Control) { c.MobileLogin = "cloudflare"; c.WidgetStatus = 500 }, password, gconnect.Blocked, gconnect.KindNone, 1, 0},
	}
	for _, c := range cases {
		t.Run(c.id, func(t *testing.T) {
			t.Parallel()
			srv := fakeGarmin(t)
			if c.set != nil {
				srv.Set(c.set)
			}
			var mu sync.Mutex
			var logs []string
			a := &gconnect.Auth{Hosts: srv.Hosts(), Log: func(m string) { mu.Lock(); logs = append(logs, m); mu.Unlock() }}
			tok, err := a.Login(context.Background(), "e2e@example.invalid", c.password)
			if c.wantCode == "" {
				if err != nil || tok.Kind() != c.wantKind {
					t.Fatalf("got %v %v, want kind %v", tok, err, c.wantKind)
				}
			} else {
				var le *gconnect.LoginError
				if tok != nil || !errors.As(err, &le) || le.Code != c.wantCode {
					t.Fatalf("got %v, want %s", err, c.wantCode)
				}
				if strings.Contains(le.Msg, c.password) || le.Error() != le.Msg {
					t.Fatalf("message %q holds the password or differs from Error()", le.Msg)
				}
			}
			for _, l := range logs {
				if strings.Contains(l, c.password) {
					t.Fatalf("a log line holds the password: %q", l)
				}
			}
			if n := posts(srv.Calls("/sso/signin")); n != c.widgetPosts {
				t.Fatalf("widget sign in posts: %d, want %d", n, c.widgetPosts)
			}
			if c.widgetPosts == 0 && len(srv.Calls("/sso/embed")) != 0 {
				t.Fatal("a widget request was sent")
			}
			if n := len(srv.Calls("/mobile/api/login")); n != 1 {
				t.Fatalf("mobile logins: %d, want 1", n)
			}
			if n := len(srv.Calls("/di-oauth2-service/oauth/token")); n != c.diCalls {
				t.Fatalf("diauth calls: %d, want %d", n, c.diCalls)
			}
		})
	}
}

// TestLoginScriptedAnswers covers Garmin answers the fake has no knob for. The expected codes
// come from the LoginCode docs: a blocked new login falls back to the widget once, and any
// failure that is not a wrong password, MFA, a rate limit or a block is LoginFailed.
func TestLoginScriptedAnswers(t *testing.T) {
	t.Parallel()
	notJSON := reply(200, "<html>maintenance</html>")
	cases := []struct {
		id       string
		override map[string]http.HandlerFunc
		wantCode gconnect.LoginCode
		wantKind gconnect.Kind
		msgHas   string
	}{
		{"DT-R423 mobile answers HTML instead of JSON: the widget signs in, a legacy login",
			map[string]http.HandlerFunc{"POST /mobile/api/login": notJSON}, "", gconnect.KindLegacy, ""},
		{"DT-R424 mobile answers 200 with a 429 inside the JSON error: the widget signs in, a legacy login",
			map[string]http.HandlerFunc{"POST /mobile/api/login": reply(200, `{"error":{"status-code":"429"}}`)}, "", gconnect.KindLegacy, ""},
		{"DT-R425 mobile answers an unknown status type: the widget signs in, a legacy login",
			map[string]http.HandlerFunc{"POST /mobile/api/login": reply(200, `{"responseStatus":{"type":"SOMETHING_NEW"}}`)}, "", gconnect.KindLegacy, ""},
		{"DT-R426 mobile answers JSON with no status at all: the widget signs in, a legacy login",
			map[string]http.HandlerFunc{"POST /mobile/api/login": reply(502, `{}`)}, "", gconnect.KindLegacy, ""},
		{"DT-R427 the mobile connection drops: the widget signs in, a legacy login",
			map[string]http.HandlerFunc{"POST /mobile/api/login": hangUp}, "", gconnect.KindLegacy, ""},
		{"DT-R428 mobile SUCCESSFUL without a ticket: the widget signs in, a legacy login",
			map[string]http.HandlerFunc{"POST /mobile/api/login": reply(200, `{"responseStatus":{"type":"SUCCESSFUL"}}`)}, "", gconnect.KindLegacy, ""},
		{"DT-R429 the widget shows the locked account page: LoginFailed that says the account is locked",
			map[string]http.HandlerFunc{"POST /mobile/api/login": notJSON, "POST /sso/signin": reply(200, `<script>var status = "ACCOUNT_LOCKED";</script>`)}, gconnect.LoginFailed, gconnect.KindNone, "locked"},
		{"DT-R430 the widget asks for a phone number update: LoginFailed that says so",
			map[string]http.HandlerFunc{"POST /mobile/api/login": notJSON, "POST /sso/signin": reply(200, `<h1>Update Phone Number</h1>`)}, gconnect.LoginFailed, gconnect.KindNone, "phone number"},
		{"DT-R431 the widget sign in page has no CSRF token: LoginFailed",
			map[string]http.HandlerFunc{"POST /mobile/api/login": notJSON, "GET /sso/signin": reply(200, `<html>no form</html>`)}, gconnect.LoginFailed, gconnect.KindNone, "CSRF"},
		{"DT-R432 the widget embed page drops the connection: LoginFailed",
			map[string]http.HandlerFunc{"POST /mobile/api/login": notJSON, "GET /sso/embed": hangUp}, gconnect.LoginFailed, gconnect.KindNone, ""},
		{"DT-R433 the widget sign in page drops the connection: LoginFailed",
			map[string]http.HandlerFunc{"POST /mobile/api/login": notJSON, "GET /sso/signin": hangUp}, gconnect.LoginFailed, gconnect.KindNone, ""},
		{"DT-R434 the widget form post drops the connection: LoginFailed",
			map[string]http.HandlerFunc{"POST /mobile/api/login": notJSON, "POST /sso/signin": hangUp}, gconnect.LoginFailed, gconnect.KindNone, ""},
		{"DT-R435 the widget form post answers a 302 redirect: not followed, Blocked",
			map[string]http.HandlerFunc{"POST /mobile/api/login": notJSON, "POST /sso/signin": func(w http.ResponseWriter, r *http.Request) {
				http.Redirect(w, r, "/sso/embed?ticket=ST-redirect", http.StatusFound)
			}}, gconnect.Blocked, gconnect.KindNone, "HTTP 302"},
		{"EG-401 the OAuth consumer file answers 500: LoginFailed",
			map[string]http.HandlerFunc{"POST /mobile/api/login": notJSON, "GET /oauth_consumer.json": reply(500, "")}, gconnect.LoginFailed, gconnect.KindNone, "HTTP 500"},
		{"EG-402 the OAuth consumer file is not JSON: LoginFailed",
			map[string]http.HandlerFunc{"POST /mobile/api/login": notJSON, "GET /oauth_consumer.json": reply(200, "<xml/>")}, gconnect.LoginFailed, gconnect.KindNone, "not JSON"},
		{"EG-403 the OAuth consumer request drops the connection: LoginFailed",
			map[string]http.HandlerFunc{"POST /mobile/api/login": notJSON, "GET /oauth_consumer.json": hangUp}, gconnect.LoginFailed, gconnect.KindNone, ""},
		{"EG-404 OAuth1 preauthorized answers 401: LoginFailed",
			map[string]http.HandlerFunc{"POST /mobile/api/login": notJSON, "GET /oauth-service/oauth/preauthorized": reply(401, "")}, gconnect.LoginFailed, gconnect.KindNone, "HTTP 401"},
		{"EG-405 OAuth1 preauthorized drops the connection: LoginFailed",
			map[string]http.HandlerFunc{"POST /mobile/api/login": notJSON, "GET /oauth-service/oauth/preauthorized": hangUp}, gconnect.LoginFailed, gconnect.KindNone, ""},
		{"EG-406 the OAuth2 exchange answers 500: LoginFailed",
			map[string]http.HandlerFunc{"POST /mobile/api/login": notJSON, "POST /oauth-service/oauth/exchange/user/2.0": reply(500, "")}, gconnect.LoginFailed, gconnect.KindNone, "HTTP 500"},
		{"EG-407 the OAuth2 exchange answer is not JSON: LoginFailed",
			map[string]http.HandlerFunc{"POST /mobile/api/login": notJSON, "POST /oauth-service/oauth/exchange/user/2.0": reply(200, "oops")}, gconnect.LoginFailed, gconnect.KindNone, "not JSON"},
		{"EG-408 the OAuth2 exchange drops the connection: LoginFailed",
			map[string]http.HandlerFunc{"POST /mobile/api/login": notJSON, "POST /oauth-service/oauth/exchange/user/2.0": hangUp}, gconnect.LoginFailed, gconnect.KindNone, ""},
		{"EG-409 diauth answers 200 that is not JSON for every client id: the widget signs in, a legacy login",
			map[string]http.HandlerFunc{"POST /di-oauth2-service/oauth/token": reply(200, "<html>")}, "", gconnect.KindLegacy, ""},
		{"EG-410 diauth answers 200 without access_token for every client id: the widget signs in, a legacy login",
			map[string]http.HandlerFunc{"POST /di-oauth2-service/oauth/token": reply(200, `{"token_type":"bearer"}`)}, "", gconnect.KindLegacy, ""},
		{"EG-411 the diauth connection drops for every client id: the widget signs in, a legacy login",
			map[string]http.HandlerFunc{"POST /di-oauth2-service/oauth/token": hangUp}, "", gconnect.KindLegacy, ""},
	}
	for _, c := range cases {
		t.Run(c.id, func(t *testing.T) {
			t.Parallel()
			srv := fakeGarmin(t)
			tok, err := (&gconnect.Auth{Hosts: front(t, srv, c.override)}).Login(context.Background(), "e@example.invalid", password)
			if c.wantCode == "" {
				if err != nil || tok.Kind() != c.wantKind {
					t.Fatalf("got %v %v, want kind %v", tok, err, c.wantKind)
				}
				return
			}
			var le *gconnect.LoginError
			if tok != nil || !errors.As(err, &le) || le.Code != c.wantCode || !strings.Contains(le.Msg, c.msgHas) || strings.Contains(le.Msg, password) {
				t.Fatalf("got %v %#v, want %s with %q", tok, err, c.wantCode, c.msgHas)
			}
		})
	}
}

func TestLoginRequests(t *testing.T) {
	t.Parallel()
	t.Run("EG-412 a cancelled context: LoginFailed, nothing reaches Garmin, no password in the message", func(t *testing.T) {
		t.Parallel()
		srv := fakeGarmin(t)
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		_, err := (&gconnect.Auth{Hosts: srv.Hosts()}).Login(ctx, "e@example.invalid", password)
		var le *gconnect.LoginError
		if !errors.As(err, &le) || le.Code != gconnect.LoginFailed || strings.Contains(le.Msg, password) {
			t.Fatalf("got %#v", err)
		}
		if n := len(srv.Calls("")); n != 0 {
			t.Fatalf("%d requests reached the fake", n)
		}
	})
	t.Run("EG-413 malformed host URLs for each step: LoginFailed, the password in no message", func(t *testing.T) {
		t.Parallel()
		srv := fakeGarmin(t)
		bad := "http://bad host"
		ok := srv.URL()
		for _, h := range []gconnect.Hosts{
			{SSO: bad, DIAuth: ok, ConnectAPI: ok, Consumer: ok},
			{SSO: ok, DIAuth: bad, ConnectAPI: ok, Consumer: bad},
			{SSO: ok, DIAuth: bad, ConnectAPI: bad, Consumer: ok},
		} {
			tok, err := (&gconnect.Auth{Hosts: h}).Login(context.Background(), "e@example.invalid", password)
			var le *gconnect.LoginError
			if tok != nil || !errors.As(err, &le) || le.Code != gconnect.LoginFailed || strings.Contains(le.Msg, password) {
				t.Fatalf("hosts %+v: got %v %#v", h, tok, err)
			}
		}
		if _, err := (&gconnect.Auth{Hosts: gconnect.Hosts{DIAuth: bad, ConnectAPI: bad}}).Refresh(context.Background(), expired()); err == nil {
			t.Fatal("a refresh to a malformed host succeeded")
		}
		legacy := parse(t, `{"oauth1":{},"oauth2":{"expires_at":1},"consumer":{}}`)
		if _, err := (&gconnect.Auth{Hosts: gconnect.Hosts{ConnectAPI: bad}}).Refresh(context.Background(), legacy); err == nil {
			t.Fatal("a legacy refresh to a malformed host succeeded")
		}
	})
	t.Run("CL-401 a mobile login: a JSON post of the email and password to /mobile/api/login, service gcm/ios", func(t *testing.T) {
		t.Parallel()
		srv := fakeGarmin(t)
		if _, err := (&gconnect.Auth{Hosts: srv.Hosts()}).Login(context.Background(), "e@example.invalid", password); err != nil {
			t.Fatal(err)
		}
		c := srv.Calls("/mobile/api/login")[0]
		var body struct{ Username, Password string }
		q, _ := url.ParseQuery(c.Query)
		if json.Unmarshal([]byte(c.Body), &body) != nil || body.Username != "e@example.invalid" || body.Password != password ||
			c.Header.Get("Content-Type") != "application/json" || q.Get("service") != "https://mobile.integration.garmin.com/gcm/ios" {
			t.Fatalf("call = %+v", c)
		}
	})
	t.Run("CL-402 the DI grant sends Basic client_id: and the native headers", func(t *testing.T) {
		t.Parallel()
		srv := fakeGarmin(t)
		if _, err := (&gconnect.Auth{Hosts: srv.Hosts()}).Login(context.Background(), "e@example.invalid", password); err != nil {
			t.Fatal(err)
		}
		c := srv.Calls("/di-oauth2-service/oauth/token")[0]
		form, _ := url.ParseQuery(c.Body)
		basic, _ := base64.StdEncoding.DecodeString(strings.TrimPrefix(c.Header.Get("Authorization"), "Basic "))
		if string(basic) != form.Get("client_id")+":" || form.Get("service_ticket") != "ST-mobile-1" || c.Header.Get("X-Garmin-Client-Platform") != "Android" {
			t.Fatalf("call = %+v", c)
		}
	})
	t.Run("CL-403 the OAuth1 preauthorized request: an RFC 5849 HMAC-SHA1 signature over the URL and the ticket", func(t *testing.T) {
		t.Parallel()
		srv := fakeGarmin(t)
		srv.Set(func(c *gconnecttest.Control) { c.MobileLogin = "429" })
		if _, err := (&gconnect.Auth{Hosts: srv.Hosts()}).Login(context.Background(), "e@example.invalid", password); err != nil {
			t.Fatal(err)
		}
		c := srv.Calls("/oauth-service/oauth/preauthorized")[0]
		oauth := map[string]string{}
		for _, kv := range strings.Split(strings.TrimPrefix(c.Header.Get("Authorization"), "OAuth "), ", ") {
			k, v, _ := strings.Cut(kv, "=")
			oauth[k], _ = url.QueryUnescape(strings.Trim(v, `"`))
		}
		// The RFC 5849 section 3.4 base string, built here independently of the library.
		params := map[string]string{}
		for k, v := range oauth {
			if k != "oauth_signature" {
				params[k] = v
			}
		}
		q, _ := url.ParseQuery(c.Query)
		for k := range q {
			params[k] = q.Get(k)
		}
		enc := func(s string) string { return strings.ReplaceAll(url.QueryEscape(s), "+", "%20") }
		var keys []string
		for k := range params {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		var pairs []string
		for _, k := range keys {
			pairs = append(pairs, enc(k)+"="+enc(params[k]))
		}
		base := "GET&" + enc(srv.URL()+"/oauth-service/oauth/preauthorized") + "&" + enc(strings.Join(pairs, "&"))
		m := hmac.New(sha1.New, []byte("cs&"))
		m.Write([]byte(base))
		if want := base64.StdEncoding.EncodeToString(m.Sum(nil)); oauth["oauth_signature"] != want || q.Get("ticket") != "ST-widget-1" || oauth["oauth_consumer_key"] != "ck" {
			t.Fatalf("signature %q, want %q; oauth %v", oauth["oauth_signature"], want, oauth)
		}
	})
	t.Run("EP-401 a set Auth.HTTP: it carries the sign in and the data requests", func(t *testing.T) {
		t.Parallel()
		srv := fakeGarmin(t)
		var n atomic.Int32
		hc := &http.Client{Transport: roundTrip(func(r *http.Request) (*http.Response, error) {
			n.Add(1)
			return http.DefaultTransport.RoundTrip(r)
		})}
		a := &gconnect.Auth{Hosts: srv.Hosts(), HTTP: hc}
		tok, err := a.Login(context.Background(), "e@example.invalid", password)
		if err != nil {
			t.Fatal(err)
		}
		c := &gconnect.Client{Auth: a, Tokens: tok, Gap: -1}
		if res := c.Get(context.Background(), ep("social_profile"), gconnect.Params{}); res.Code != "" {
			t.Fatalf("result %+v", res)
		}
		if got, want := int(n.Load()), len(srv.Calls("")); got != want || got != 3 {
			t.Fatalf("%d requests went through Auth.HTTP, %d reached the fake, want 3", got, want)
		}
	})
	t.Run("EP-402 a nil Auth.HTTP: sign in and data work with the default client", func(t *testing.T) {
		t.Parallel()
		srv := fakeGarmin(t)
		a := &gconnect.Auth{Hosts: srv.Hosts()}
		tok, err := a.Login(context.Background(), "e@example.invalid", password)
		if err != nil {
			t.Fatal(err)
		}
		if res := (&gconnect.Client{Auth: a, Tokens: tok, Gap: -1}).Get(context.Background(), ep("social_profile"), gconnect.Params{}); res.Code != "" {
			t.Fatalf("result %+v", res)
		}
	})
}

type roundTrip func(*http.Request) (*http.Response, error)

func (f roundTrip) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestHosts(t *testing.T) {
	t.Parallel()
	real := [4]string{"https://sso.garmin.com", "https://diauth.garmin.com", "https://connectapi.garmin.com", "https://thegarth.s3.amazonaws.com"}
	cases := []struct {
		id    string
		hosts gconnect.Hosts
		want  [4]string
	}{
		{"EP-403 the zero Hosts: the real Garmin hosts and the garth consumer bucket", gconnect.Hosts{}, real},
		{"EP-404 SameHost: all four hosts are that base", gconnect.SameHost("http://127.0.0.1:9"), [4]string{"http://127.0.0.1:9", "http://127.0.0.1:9", "http://127.0.0.1:9", "http://127.0.0.1:9"}},
		{"EP-405 only Consumer set: Consumer is pinned, the other three stay real", gconnect.Hosts{Consumer: "https://mine.example"}, [4]string{real[0], real[1], real[2], "https://mine.example"}},
	}
	for _, c := range cases {
		t.Run(c.id, func(t *testing.T) {
			t.Parallel()
			if got := gconnect.HostURLs(c.hosts); got != c.want {
				t.Fatalf("got %v", got)
			}
		})
	}
}

// diAnswer scripts the DI grant: it answers body to every grant and records the forms.
func diAnswer(t *testing.T, body string) (gconnect.Hosts, func() []url.Values) {
	t.Helper()
	srv := fakeGarmin(t)
	var mu sync.Mutex
	var forms []url.Values
	hosts := front(t, srv, map[string]http.HandlerFunc{"POST /di-oauth2-service/oauth/token": func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		f, _ := url.ParseQuery(string(b))
		mu.Lock()
		forms = append(forms, f)
		mu.Unlock()
		io.WriteString(w, body)
	}})
	return hosts, func() []url.Values { mu.Lock(); defer mu.Unlock(); return forms }
}

func TestDITokenFields(t *testing.T) {
	t.Parallel()
	now := time.Unix(1_800_000_000, 0)
	at := func(d int64) *int64 { v := now.Unix() + d; return &v }
	cases := []struct {
		id, body     string
		wantExpires  int64
		wantClientID string // "": the id the grant was sent with
		wantRefresh  *int64
	}{
		{"EP-406 the access token JWT holds exp and client_id: ExpiresAt is exp, ClientID is the claim",
			`{"access_token":"` + gconnecttest.JWT(map[string]any{"exp": 1_900_000_000, "client_id": "FROM_JWT"}) + `","refresh_token":"r","expires_in":10,"refresh_token_expires_in":2592000}`,
			1_900_000_000, "FROM_JWT", at(2592000)},
		{"EP-407 a JWT without exp and an expires_in of 7200: ExpiresAt is now + 7200",
			`{"access_token":"` + gconnecttest.JWT(map[string]any{"sub": 1}) + `","refresh_token":"r","expires_in":7200}`,
			now.Unix() + 7200, "", nil},
		{"EP-408 an opaque token without expires_in: ExpiresAt is now + 3600 (assumption S1)",
			`{"access_token":"opaque","refresh_token":"r"}`,
			now.Unix() + 3600, "", nil},
		{"EP-409 no refresh_token_expires_in: RefreshTokenExpiresAt stays null",
			`{"access_token":"opaque","refresh_token":"r","expires_in":60}`,
			now.Unix() + 60, "", nil},
	}
	for _, c := range cases {
		t.Run(c.id, func(t *testing.T) {
			t.Parallel()
			hosts, forms := diAnswer(t, c.body)
			tok, err := (&gconnect.Auth{Hosts: hosts, Now: func() time.Time { return now }}).Login(context.Background(), "e@example.invalid", password)
			if err != nil {
				t.Fatal(err)
			}
			want := c.wantClientID
			if want == "" {
				want = forms()[0].Get("client_id")
			}
			di := tok.DI
			if di.ExpiresAt != c.wantExpires || di.ClientID != want || di.RefreshToken != "r" || (di.RefreshTokenExpiresAt == nil) != (c.wantRefresh == nil) ||
				c.wantRefresh != nil && *di.RefreshTokenExpiresAt != *c.wantRefresh {
				t.Fatalf("DI = %+v (refresh expiry %v)", di, di.RefreshTokenExpiresAt)
			}
		})
	}
	t.Run("EP-410 a refresh answer without refresh_token: the old one is kept (RFC 6749 section 6)", func(t *testing.T) {
		t.Parallel()
		hosts, forms := diAnswer(t, `{"access_token":"new","expires_in":60}`)
		old := &gconnect.Tokens{DI: &gconnect.DI{AccessToken: "a", RefreshToken: "keep-me", ClientID: "CID", ExpiresAt: 1}}
		tok, err := (&gconnect.Auth{Hosts: hosts}).Refresh(context.Background(), old)
		if err != nil || tok.DI.RefreshToken != "keep-me" || tok.DI.AccessToken != "new" || tok.DI.ClientID != "CID" {
			t.Fatalf("got %+v %v", tok, err)
		}
		if f := forms()[0]; f.Get("grant_type") != "refresh_token" || f.Get("refresh_token") != "keep-me" || f.Get("client_id") != "CID" {
			t.Fatalf("form = %v", f)
		}
	})
	t.Run("EP-411 the first client id refused: the next one is tried and the accepted id is stored", func(t *testing.T) {
		t.Parallel()
		srv := fakeGarmin(t)
		var mu sync.Mutex
		var ids []string
		hosts := front(t, srv, map[string]http.HandlerFunc{"POST /di-oauth2-service/oauth/token": func(w http.ResponseWriter, r *http.Request) {
			b, _ := io.ReadAll(r.Body)
			f, _ := url.ParseQuery(string(b))
			mu.Lock()
			defer mu.Unlock()
			if ids = append(ids, f.Get("client_id")); len(ids) == 1 {
				w.WriteHeader(400)
				return
			}
			io.WriteString(w, `{"access_token":"opaque","refresh_token":"r","expires_in":60}`)
		}})
		tok, err := (&gconnect.Auth{Hosts: hosts}).Login(context.Background(), "e@example.invalid", password)
		mu.Lock()
		defer mu.Unlock()
		if err != nil || len(ids) != 2 || ids[0] == ids[1] || tok.DI.ClientID != ids[1] {
			t.Fatalf("ids %v, tokens %+v, err %v", ids, tok, err)
		}
	})
	t.Run("EP-412 a legacy OAuth2 token with expires_in 1234: it expires 1234 s after now (RFC 6749 section 5.1)", func(t *testing.T) {
		t.Parallel()
		srv := fakeGarmin(t)
		srv.Set(func(c *gconnecttest.Control) { c.MobileLogin = "429"; c.TokenTTL = 1234 })
		tok, err := (&gconnect.Auth{Hosts: srv.Hosts(), Now: func() time.Time { return now }}).Login(context.Background(), "e@example.invalid", password)
		if err != nil || tok.OAuth2.ExpiresAt != now.Unix()+1234 || tok.OAuth2.RefreshTokenExpiresAt != now.Unix()+2592000 || tok.OAuth1.Token != "ot" || tok.Consumer.Key != "ck" {
			t.Fatalf("got %+v %v", tok, err)
		}
	})
}

func TestFreshMargin(t *testing.T) {
	t.Parallel()
	const exp = 1_800_000_000
	margin := 15 * time.Minute
	cases := []struct {
		id      string
		now     int64
		refresh bool
	}{
		{"BVA-401 one second before expiry minus the margin: no refresh, no request", exp - 901, false},
		{"BVA-402 exactly expiry minus the margin: refresh", exp - 900, true},
		{"BVA-403 one second after expiry minus the margin: refresh", exp - 899, true},
	}
	for _, c := range cases {
		t.Run(c.id, func(t *testing.T) {
			t.Parallel()
			srv := fakeGarmin(t)
			tok := &gconnect.Tokens{DI: &gconnect.DI{AccessToken: "a", RefreshToken: "refresh-1", ClientID: "X", ExpiresAt: exp}}
			got, refreshed, err := (&gconnect.Auth{Hosts: srv.Hosts(), Now: func() time.Time { return time.Unix(c.now, 0) }}).Fresh(context.Background(), tok, margin)
			calls := map[bool]int{true: 1, false: 0}[c.refresh]
			if err != nil || refreshed != c.refresh || (got == tok) == c.refresh || len(srv.Calls("")) != calls {
				t.Fatalf("refreshed %v, same tokens %v, err %v, calls %d", refreshed, got == tok, err, len(srv.Calls("")))
			}
		})
	}
}

func TestTokenLifecycle(t *testing.T) {
	t.Parallel()
	t.Run("ST-T401 valid, then near expiry, then refreshed: Fresh sends one refresh, only in the middle state", func(t *testing.T) {
		t.Parallel()
		srv := fakeGarmin(t)
		a := &gconnect.Auth{Hosts: srv.Hosts()}
		tok, err := a.Login(context.Background(), "e@example.invalid", password)
		if err != nil {
			t.Fatal(err)
		}
		srv.Reset()
		if got, refreshed, err := a.Fresh(context.Background(), tok, 15*time.Minute); err != nil || refreshed || got != tok {
			t.Fatalf("valid: %v %v", refreshed, err)
		}
		a.Now = func() time.Time { return time.Unix(tok.AccessExpiresAt()-60, 0) }
		next, refreshed, err := a.Fresh(context.Background(), tok, 15*time.Minute)
		if err != nil || !refreshed || next.DI.AccessToken == tok.DI.AccessToken {
			t.Fatalf("near expiry: %v %v", refreshed, err)
		}
		a.Now = nil
		if _, refreshed, err := a.Fresh(context.Background(), next, 15*time.Minute); err != nil || refreshed {
			t.Fatalf("refreshed: %v %v", refreshed, err)
		}
		if n := len(srv.Calls("/di-oauth2-service/oauth/token")); n != 1 {
			t.Fatalf("%d refresh grants, want 1", n)
		}
	})
	t.Run("ST-T402 an expired legacy token renews through the OAuth1 exchange alone", func(t *testing.T) {
		t.Parallel()
		srv := fakeGarmin(t)
		past := strconv.FormatInt(time.Now().Unix()-10, 10)
		tok := parse(t, `{"oauth1":{"oauth_token":"ot","oauth_token_secret":"os"},"oauth2":{"access_token":"old","expires_at":`+past+`,"refresh_token_expires_at":`+past+`},"consumer":{"consumer_key":"ck","consumer_secret":"cs"}}`)
		got, refreshed, err := (&gconnect.Auth{Hosts: srv.Hosts()}).Fresh(context.Background(), tok, 15*time.Minute)
		if err != nil || !refreshed || got.OAuth2.AccessToken == "old" || got.OAuth1 != tok.OAuth1 || got.Consumer != tok.Consumer {
			t.Fatalf("got %+v %v", got, err)
		}
		if len(srv.Calls("/oauth-service/oauth/exchange/user/2.0")) != 1 || len(srv.Calls("/mobile/api/login")) != 0 {
			t.Fatal("the legacy token was not renewed through the OAuth1 exchange alone")
		}
	})
	t.Run("ST-T403 Fresh and Refresh on tokens with no login: ErrNoTokens, nothing sent", func(t *testing.T) {
		t.Parallel()
		srv := fakeGarmin(t)
		a := &gconnect.Auth{Hosts: srv.Hosts()}
		for _, tok := range []*gconnect.Tokens{{}, {OAuth1: &gconnect.OAuth1{}, OAuth2: &gconnect.OAuth2{}}} {
			if _, _, err := a.Fresh(context.Background(), tok, time.Minute); !errors.Is(err, gconnect.ErrNoTokens) {
				t.Fatalf("Fresh: %v", err)
			}
			if _, err := a.Refresh(context.Background(), tok); !errors.Is(err, gconnect.ErrNoTokens) {
				t.Fatalf("Refresh: %v", err)
			}
		}
		if n := len(srv.Calls("")); n != 0 {
			t.Fatalf("%d requests", n)
		}
	})
	t.Run("ST-T411 a DI login without refresh_token is dead and sends nothing", func(t *testing.T) {
		t.Parallel()
		srv := fakeGarmin(t)
		tok := parse(t, `{"di":{"access_token":"x","client_id":"GARMIN_CONNECT_MOBILE_ANDROID_DI_2025Q2","expires_at":1}}`)
		_, _, err := (&gconnect.Auth{Hosts: srv.Hosts()}).Fresh(context.Background(), tok, 15*time.Minute)
		if !errors.Is(err, gconnect.ErrSessionDead) {
			t.Fatalf("got %v, want ErrSessionDead", err)
		}
		if n := len(srv.Calls("")); n != 0 {
			t.Fatalf("%d requests", n)
		}
	})
}

func TestRefreshClassifiesGarminAnswers(t *testing.T) {
	t.Parallel()
	di := `{"di":{"access_token":"x","refresh_token":"refresh-0","client_id":"GARMIN_CONNECT_MOBILE_ANDROID_DI_2025Q2","expires_at":1}}`
	legacy := `{"oauth1":{"oauth_token":"ot","oauth_token_secret":"os"},"oauth2":{"access_token":"old","expires_at":1,"refresh_token_expires_at":1},"consumer":{"consumer_key":"ck","consumer_secret":"cs"}}`
	cases := []struct {
		id, body string
		status   int // 0: the server is closed
		dead     bool
	}{
		{"ST-T421 the DI refresh grant answers 400: the login is dead", di, 400, true},
		{"ST-T422 the DI refresh grant answers 401: the login is dead", di, 401, true},
		{"ST-T423 the legacy OAuth2 exchange answers 401: the login is dead", legacy, 401, true},
		{"ST-T424 the DI refresh grant answers 403: a plain error, keep the tokens", di, 403, false},
		{"ST-T425 the DI refresh grant answers 429: a plain error, keep the tokens", di, 429, false},
		{"ST-T426 the DI refresh grant answers 500: a plain error, keep the tokens", di, 500, false},
		{"ST-T427 a closed server on the DI refresh: a plain error, keep the tokens", di, 0, false},
		{"ST-T428 the legacy OAuth2 exchange answers 400: a plain error, keep the tokens", legacy, 400, false},
		{"ST-T429 the legacy OAuth2 exchange answers 500: a plain error, keep the tokens", legacy, 500, false},
		{"ST-T430 a closed server on the legacy exchange: a plain error, keep the tokens", legacy, 0, false},
	}
	for _, c := range cases {
		t.Run(c.id, func(t *testing.T) {
			t.Parallel()
			srv := fakeGarmin(t)
			srv.Set(func(k *gconnecttest.Control) { k.RefreshFail = c.status })
			if c.status == 0 {
				srv.Close()
			}
			tok, err := (&gconnect.Auth{Hosts: srv.Hosts()}).Refresh(context.Background(), parse(t, c.body))
			if err == nil || tok != nil || errors.Is(err, gconnect.ErrSessionDead) != c.dead {
				t.Fatalf("got %v %v, dead want %v", tok, err, c.dead)
			}
		})
	}
}

func TestSigningAndHeaders(t *testing.T) {
	t.Parallel()
	t.Run("EG-421 a diauth JWT, and a token without dots: exp and client_id read, or none", func(t *testing.T) {
		t.Parallel()
		c := gconnect.JWTClaims(gconnecttest.JWT(map[string]any{"exp": 5, "client_id": "X"}))
		if c.ClientID != "X" || c.Exp != 5 {
			t.Fatalf("got %+v", c)
		}
		if c := gconnect.JWTClaims("not-a-jwt"); c.ClientID != "" || c.Exp != 0 {
			t.Fatalf("a token without dots gave %+v", c)
		}
	})
	t.Run("CL-422 the OAuth1 signature of a fixed request: the value the JavaScript implementation computes", func(t *testing.T) {
		t.Parallel()
		params := map[string]string{
			"oauth_consumer_key": "ck", "oauth_nonce": "n", "oauth_signature_method": "HMAC-SHA1",
			"oauth_timestamp": "1", "oauth_version": "1.0", "ticket": "ST-1",
			"login-url": "https://sso.garmin.com/sso/embed", "accepts-mfa-tokens": "true",
		}
		// Computed once with the Garmin Export app's JavaScript signOAuth1, which this code was ported from.
		if got := gconnect.SignOAuth1("GET", "https://x/oauth-service/oauth/preauthorized", params, "cs", ""); got != "vuDWLkAGwPDSl8r0q3ly059MkjE=" {
			t.Fatalf("signature %s", got)
		}
	})
	t.Run("CL-423 DI tokens: the native Android headers and the Bearer access token", func(t *testing.T) {
		t.Parallel()
		h := gconnect.Headers(&gconnect.Tokens{DI: &gconnect.DI{AccessToken: "a"}})
		if h.Get("X-Garmin-Client-Platform") != "Android" || h.Get("User-Agent") != "GCM-Android-5.23" || h.Get("Authorization") != "Bearer a" || h.Get("Accept") != "application/json" {
			t.Fatalf("DI headers %v", h)
		}
	})
	t.Run("CL-424 legacy tokens: the connectmobile user agent and the OAuth2 Bearer token", func(t *testing.T) {
		t.Parallel()
		h := gconnect.Headers(&gconnect.Tokens{OAuth1: &gconnect.OAuth1{}, OAuth2: &gconnect.OAuth2{AccessToken: "b"}, Consumer: &gconnect.Consumer{}})
		if h.Get("User-Agent") != "com.garmin.android.apps.connectmobile" || h.Get("X-Garmin-Client-Platform") != "" || h.Get("Authorization") != "Bearer b" {
			t.Fatalf("legacy headers %v", h)
		}
	})
	t.Run("CL-425 connectapi on the fake with an issued token and another: 200 and 401", func(t *testing.T) {
		t.Parallel()
		srv := fakeGarmin(t)
		tok, err := (&gconnect.Auth{Hosts: srv.Hosts()}).Login(context.Background(), "e2e@example.invalid", password)
		if err != nil {
			t.Fatal(err)
		}
		for _, c := range []struct {
			access string
			want   int
		}{{tok.DI.AccessToken, 200}, {"unknown", 401}} {
			req, _ := http.NewRequestWithContext(context.Background(), "GET", srv.URL()+"/userprofile-service/socialProfile", nil)
			req.Header = gconnect.Headers(&gconnect.Tokens{DI: &gconnect.DI{AccessToken: c.access}})
			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				t.Fatal(err)
			}
			resp.Body.Close()
			if resp.StatusCode != c.want {
				t.Fatalf("token %q answered %d, want %d", c.access, resp.StatusCode, c.want)
			}
		}
	})
}

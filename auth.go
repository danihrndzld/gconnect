package gconnect

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha1"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Constants from auth.js:57-93.
const (
	gcModern      = "https://connect.garmin.com/modern"
	mobileService = "https://mobile.integration.garmin.com/gcm/ios" // the ticket is only valid for this service
	diGrantTicket = "https://connectapi.garmin.com/di-oauth2-service/oauth/grant/service_ticket"
	userAgentIOS  = "Mozilla/5.0 (iPhone; CPU iPhone OS 18_7 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Mobile/15E148"
	userAgentWeb  = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/117.0.0.0 Safari/537.36"
	userAgentApp  = "com.garmin.android.apps.connectmobile"
)

// Tried in order; Garmin retires old ids over time.
var diClientIDs = []string{
	"GARMIN_CONNECT_MOBILE_ANDROID_DI_2025Q2",
	"GARMIN_CONNECT_MOBILE_ANDROID_DI_2024Q4",
	"GARMIN_CONNECT_MOBILE_ANDROID_DI",
	"GARMIN_CONNECT_MOBILE_IOS_DI",
}

// connectapi answers 403 to a DI token without these (measured 2026-10-14). The keys are
// set as written, not canonicalized, so the wire matches auth.js:71-80.
var nativeHeaders = [][2]string{
	{"User-Agent", "GCM-Android-5.23"},
	{"X-Garmin-User-Agent", "com.garmin.android.apps.connectmobile/5.23; ; Google/sdk_gphone64_arm64/google; Android/33; Dalvik/2.1.0"},
	{"X-Garmin-Paired-App-Version", "10861"},
	{"X-Garmin-Client-Platform", "Android"},
	{"X-App-Ver", "10861"},
	{"X-Lang", "en"},
	{"X-GCExperience", "GC5"},
	{"Accept-Language", "en-US,en;q=0.9"},
}

var (
	csrfRE   = regexp.MustCompile(`name="_csrf"\s+value="(.+?)"`)
	ticketRE = regexp.MustCompile(`ticket=([^"]+)"`)
	lockedRE = regexp.MustCompile(`var\s+status\s*=\s*"`)
)

// Hosts names the Garmin hosts. The zero value is the real Garmin; tests set every field to one fake.
type Hosts struct{ SSO, DIAuth, ConnectAPI, Consumer string }

// SameHost points all four hosts at base.
func SameHost(base string) Hosts { return Hosts{base, base, base, base} }

func or(v, real string) string {
	if v != "" {
		return v
	}
	return real
}

func (h Hosts) sso() string        { return or(h.SSO, "https://sso.garmin.com") }
func (h Hosts) diauth() string     { return or(h.DIAuth, "https://diauth.garmin.com") }
func (h Hosts) connectAPI() string { return or(h.ConnectAPI, "https://connectapi.garmin.com") }
func (h Hosts) consumer() string   { return or(h.Consumer, "https://thegarth.s3.amazonaws.com") }

type LoginCode string

const (
	BadPassword LoginCode = "bad_password"
	MFARequired LoginCode = "mfa"
	RateLimited LoginCode = "rate_limited" // the caller should wait 15 minutes before another Login
	Blocked     LoginCode = "blocked"      // 403 or another non 200 on both flows
	LoginFailed LoginCode = "error"        // network, not JSON, unexpected status
)

// LoginError is a sign in failure. Msg never holds the password.
type LoginError struct {
	Code LoginCode
	Msg  string
}

func (e *LoginError) Error() string { return e.Msg }

var errRateLimited = &LoginError{RateLimited, "Garmin is rate limiting sign ins (HTTP 429). Wait 15 minutes and try again."}

var (
	// ErrSessionDead means Garmin proved the saved login is dead; the caller marks it expired.
	ErrSessionDead = errors.New("gconnect: the saved login is no longer valid")
	ErrNoTokens    = errors.New("gconnect: the tokens hold neither a DI nor a legacy login")
)

type Auth struct {
	Hosts Hosts
	HTTP  *http.Client     // nil: 30 s timeout
	Now   func() time.Time // nil: time.Now
	Log   func(msg string) // dim lines; never receives the password
}

func (a *Auth) client() *http.Client {
	if a.HTTP != nil {
		return a.HTTP
	}
	return &http.Client{Timeout: 30 * time.Second}
}

func (a *Auth) now() time.Time {
	if a.Now != nil {
		return a.Now()
	}
	return time.Now()
}

func (a *Auth) log(format string, args ...any) {
	if a.Log != nil {
		a.Log(fmt.Sprintf(format, args...))
	}
}

// do sends one request and reads at most 1 MB of the answer.
func do(c *http.Client, req *http.Request) (int, []byte, error) {
	resp, err := c.Do(req)
	if ue := (*url.Error)(nil); errors.As(err, &ue) {
		err = ue.Err // the URL can hold the OAuth1 token and signature
	}
	if err != nil {
		return 0, nil, err
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	return resp.StatusCode, b, err
}

// OAuth 1.0a signing, auth.js:122-154. percentEncode is RFC 3986, like encodeURIComponent
// plus !*'() escaped.
func percentEncode(s string) string {
	return strings.ReplaceAll(url.QueryEscape(s), "+", "%20")
}

func sortedPairs(params map[string]string, format string) []string {
	keys := make([]string, 0, len(params))
	for k := range params {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := make([]string, len(keys))
	for i, k := range keys {
		out[i] = fmt.Sprintf(format, percentEncode(k), percentEncode(params[k]))
	}
	return out
}

func buildBaseString(method, u string, params map[string]string) string {
	return strings.ToUpper(method) + "&" + percentEncode(u) + "&" + percentEncode(strings.Join(sortedPairs(params, "%s=%s"), "&"))
}

func signOAuth1(method, u string, params map[string]string, consumerSecret, tokenSecret string) string {
	m := hmac.New(sha1.New, []byte(percentEncode(consumerSecret)+"&"+percentEncode(tokenSecret)))
	m.Write([]byte(buildBaseString(method, u, params)))
	return base64.StdEncoding.EncodeToString(m.Sum(nil))
}

func oauthHeader(params map[string]string) string {
	return "OAuth " + strings.Join(sortedPairs(params, `%s="%s"`), ", ")
}

func nonce() string {
	b := make([]byte, 16)
	rand.Read(b)
	return hex.EncodeToString(b)
}

type claims struct {
	Exp      float64 `json:"exp"`
	ClientID string  `json:"client_id"`
}

// jwtClaims reads a JWT payload without verifying it (only exp and client_id are used).
func jwtClaims(token string) claims {
	var c claims
	parts := strings.Split(token, ".")
	if len(parts) < 2 {
		return c
	}
	b, err := base64.RawURLEncoding.DecodeString(strings.TrimRight(parts[1], "="))
	if err == nil {
		_ = json.Unmarshal(b, &c)
	}
	return c
}

// Headers returns the connectapi headers with the access token: the native app headers for
// DI, the mobile UA for legacy.
func Headers(t *Tokens) http.Header {
	h, access := http.Header{}, ""
	if t.DI != nil {
		for _, kv := range nativeHeaders {
			h[kv[0]] = []string{kv[1]}
		}
		access = t.DI.AccessToken
	} else {
		h.Set("User-Agent", userAgentApp)
		if t.OAuth2 != nil {
			access = t.OAuth2.AccessToken
		}
	}
	h.Set("Authorization", "Bearer "+access)
	h.Set("Accept", "application/json")
	return h
}

// Login ports auth.js:login and _loginChain (202-239). It returns the tokens and saves
// nothing. Every failure is a *LoginError; after RateLimited the caller should wait 15 minutes.
func (a *Auth) Login(ctx context.Context, email, password string) (*Tokens, error) {
	t, err := a.chain(ctx, email, password)
	if err == nil {
		return t, nil
	}
	var le *LoginError
	if !errors.As(err, &le) {
		le = &LoginError{LoginFailed, err.Error()}
	}
	return nil, le
}

func (a *Auth) chain(ctx context.Context, email, password string) (*Tokens, error) {
	ticket, err := a.loginMobile(ctx, email, password)
	// A 429 on the mobile login does not stop the chain: Garmin limits the two logins
	// separately (measured 2026-10-14). The widget flow is tried once; a second 429 stops.
	var le *LoginError
	if errors.As(err, &le) && le.Code != RateLimited {
		return nil, err
	}
	if err == nil {
		di, err := a.exchangeTicketForDI(ctx, ticket)
		if errors.As(err, &le) {
			return nil, err
		}
		if err == nil {
			return &Tokens{DI: di}, nil
		}
		a.log("[auth] DI token exchange failed (%v); trying the older sign in", err)
	} else {
		a.log("[auth] new sign in blocked (%v); trying the older sign in", err)
	}
	// Only a blocked new login reaches here: never after a wrong password, MFA or a DI 429.
	ticket, err = a.loginWidget(ctx, email, password)
	if err != nil {
		return nil, err
	}
	return a.legacyFromTicket(ctx, ticket)
}

// loginMobile ports _loginMobile (259-287): one JSON POST, no cookies or CSRF. A plain
// error means the login was blocked and the widget flow may try.
func (a *Auth) loginMobile(ctx context.Context, email, password string) (string, error) {
	sso := a.Hosts.sso()
	qs := url.Values{"clientId": {"GCM_IOS_DARK"}, "locale": {"en-US"}, "service": {mobileService}}.Encode()
	body, _ := json.Marshal(struct {
		Username     string `json:"username"`
		Password     string `json:"password"`
		RememberMe   bool   `json:"rememberMe"`
		CaptchaToken string `json:"captchaToken"`
	}{email, password, true, ""})
	req, err := http.NewRequestWithContext(ctx, "POST", sso+"/mobile/api/login?"+qs, strings.NewReader(string(body)))
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", userAgentIOS)
	req.Header.Set("Accept", "application/json, text/plain, */*")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", sso)
	status, text, err := do(a.client(), req)
	if err != nil {
		return "", fmt.Errorf("network: %w", err)
	}
	if status == 429 {
		return "", errRateLimited
	}
	var r struct {
		ResponseStatus  struct{ Type string } `json:"responseStatus"`
		ServiceTicketID string                `json:"serviceTicketId"`
		Error           map[string]any        `json:"error"`
	}
	if json.Unmarshal(text, &r) != nil {
		return "", fmt.Errorf("HTTP %d, not JSON", status)
	}
	switch t := r.ResponseStatus.Type; {
	case t == "SUCCESSFUL" && r.ServiceTicketID != "":
		return r.ServiceTicketID, nil
	case t == "INVALID_USERNAME_PASSWORD":
		return "", &LoginError{BadPassword, "Wrong email or password."}
	case t == "MFA_REQUIRED":
		return "", &LoginError{MFARequired, "This account uses two step verification, which Garmin Export does not support yet."}
	case r.Error != nil && fmt.Sprint(r.Error["status-code"]) == "429":
		return "", errRateLimited
	case t != "":
		return "", errors.New(t)
	}
	return "", fmt.Errorf("HTTP %d", status)
}

// exchangeTicketForDI ports _exchangeTicketForDi (290-304). A *LoginError (429) stops the chain.
func (a *Auth) exchangeTicketForDI(ctx context.Context, ticket string) (*DI, error) {
	err := errors.New("no client id accepted the ticket")
	for _, id := range diClientIDs {
		di, status, e := a.diTokenRequest(ctx, id, url.Values{
			"client_id": {id}, "service_ticket": {ticket}, "grant_type": {diGrantTicket}, "service_url": {mobileService},
		})
		if status == 429 {
			return nil, errRateLimited
		}
		if e == nil {
			return di, nil
		}
		err = e
	}
	return nil, err
}

// diTokenRequest ports _diTokenRequest (306-341). status is 0 on a network error.
func (a *Auth) diTokenRequest(ctx context.Context, clientID string, form url.Values) (*DI, int, error) {
	req, err := http.NewRequestWithContext(ctx, "POST", a.Hosts.diauth()+"/di-oauth2-service/oauth/token", strings.NewReader(form.Encode()))
	if err != nil {
		return nil, 0, err
	}
	for _, kv := range nativeHeaders {
		req.Header[kv[0]] = []string{kv[1]}
	}
	req.Header.Set("Authorization", "Basic "+base64.StdEncoding.EncodeToString([]byte(clientID+":")))
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Cache-Control", "no-cache")
	status, text, err := do(a.client(), req)
	if err != nil {
		return nil, 0, fmt.Errorf("network: %w", err)
	}
	if status < 200 || status > 299 {
		return nil, status, fmt.Errorf("diauth HTTP %d", status)
	}
	var b struct {
		AccessToken           string `json:"access_token"`
		RefreshToken          string `json:"refresh_token"`
		ExpiresIn             int64  `json:"expires_in"`
		RefreshTokenExpiresIn int64  `json:"refresh_token_expires_in"`
	}
	if json.Unmarshal(text, &b) != nil {
		return nil, status, errors.New("diauth answer is not JSON")
	}
	if b.AccessToken == "" {
		return nil, status, errors.New("diauth answer has no access_token")
	}
	c, now := jwtClaims(b.AccessToken), a.now().Unix()
	di := &DI{AccessToken: b.AccessToken, RefreshToken: b.RefreshToken, ClientID: c.ClientID, ExpiresAt: int64(c.Exp)}
	if di.RefreshToken == "" {
		di.RefreshToken = form.Get("refresh_token")
	}
	if di.ClientID == "" {
		di.ClientID = clientID
	}
	if di.ExpiresAt == 0 {
		di.ExpiresAt = now + orDefault(b.ExpiresIn, 3600)
	}
	if b.RefreshTokenExpiresIn > 0 {
		at := now + b.RefreshTokenExpiresIn
		di.RefreshTokenExpiresAt = &at
	}
	return di, status, nil
}

// orDefault returns v, or def when v is 0 (the JS `v || def`).
func orDefault(v, def int64) int64 {
	if v == 0 {
		return def
	}
	return v
}

// loginWidget ports _loginWidget (344-437): sso/embed, the signin page's CSRF token, then
// the form POST. Redirects are not followed, like the JS `redirect: 'manual'`.
func (a *Auth) loginWidget(ctx context.Context, email, password string) (string, error) {
	jar, _ := cookiejar.New(nil)
	c := *a.client()
	c.Jar = jar
	c.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	sso := a.Hosts.sso()
	embed, signin := sso+"/sso/embed", sso+"/sso/signin"
	get := func(u string) ([]byte, error) {
		req, err := http.NewRequestWithContext(ctx, "GET", u, nil)
		if err != nil {
			return nil, err
		}
		req.Header.Set("User-Agent", userAgentWeb)
		_, b, err := do(&c, req)
		return b, err
	}
	if _, err := get(embed + "?" + url.Values{"clientId": {"GarminConnect"}, "locale": {"en"}, "service": {gcModern}}.Encode()); err != nil {
		return "", err
	}
	page, err := get(signin + "?" + url.Values{"id": {"gauth-widget"}, "embedWidget": {"true"}, "locale": {"en"}, "gauthHost": {embed}}.Encode())
	if err != nil {
		return "", err
	}
	m := csrfRE.FindSubmatch(page)
	if m == nil {
		return "", errors.New("Garmin's sign in page has no CSRF token.")
	}
	qs := url.Values{
		"id": {"gauth-widget"}, "embedWidget": {"true"}, "clientId": {"GarminConnect"}, "locale": {"en"},
		"gauthHost": {embed}, "service": {embed}, "source": {embed},
		"redirectAfterAccountLoginUrl": {embed}, "redirectAfterAccountCreationUrl": {embed},
	}.Encode()
	form := url.Values{"username": {email}, "password": {password}, "embed": {"true"}, "_csrf": {string(m[1])}}.Encode()
	req, err := http.NewRequestWithContext(ctx, "POST", signin+"?"+qs, strings.NewReader(form))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("User-Agent", userAgentWeb)
	req.Header.Set("Origin", sso)
	req.Header.Set("Referer", signin)
	req.Header.Set("Dnt", "1")
	status, html, err := do(&c, req)
	if err != nil {
		return "", err
	}
	switch {
	case lockedRE.Match(html):
		return "", errors.New("The Garmin account is locked. Unlock it at connect.garmin.com.")
	case strings.Contains(string(html), "Update Phone Number"):
		return "", errors.New("Garmin asks for a phone number update. Visit connect.garmin.com first.")
	case status == 429:
		return "", errRateLimited
	case status != 200:
		return "", &LoginError{Blocked, fmt.Sprintf("Garmin blocked the sign in (HTTP %d). Try again later or from another network.", status)}
	}
	t := ticketRE.FindSubmatch(html)
	if t == nil {
		return "", &LoginError{BadPassword, "Wrong email or password, or the account needs two step verification."}
	}
	return string(t[1]), nil
}

// legacyFromTicket ports _legacyFromTicket (440-512): the S3 consumer, OAuth1 preauthorized,
// then the OAuth2 exchange.
func (a *Auth) legacyFromTicket(ctx context.Context, ticket string) (*Tokens, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", a.Hosts.consumer()+"/oauth_consumer.json", nil)
	if err != nil {
		return nil, err
	}
	status, b, err := do(a.client(), req)
	if err != nil {
		return nil, err
	}
	if status < 200 || status > 299 {
		return nil, fmt.Errorf("could not fetch the OAuth consumer (HTTP %d)", status)
	}
	var consumer Consumer
	if err := json.Unmarshal(b, &consumer); err != nil {
		return nil, fmt.Errorf("the OAuth consumer is not JSON: %w", err)
	}

	base := a.Hosts.connectAPI() + "/oauth-service/oauth/preauthorized"
	query := map[string]string{"ticket": ticket, "login-url": a.Hosts.sso() + "/sso/embed", "accepts-mfa-tokens": "true"}
	params := map[string]string{
		"oauth_consumer_key": consumer.Key, "oauth_nonce": nonce(), "oauth_signature_method": "HMAC-SHA1",
		"oauth_timestamp": strconv.FormatInt(a.now().Unix(), 10), "oauth_version": "1.0",
	}
	all := map[string]string{}
	for k, v := range params {
		all[k] = v
	}
	qv := url.Values{}
	for k, v := range query {
		all[k] = v
		qv.Set(k, v)
	}
	params["oauth_signature"] = signOAuth1("GET", base, all, consumer.Secret, "")
	req, err = http.NewRequestWithContext(ctx, "GET", base+"?"+qv.Encode(), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", oauthHeader(params))
	req.Header.Set("User-Agent", userAgentApp)
	status, b, err = do(a.client(), req)
	if err != nil {
		return nil, err
	}
	if status < 200 || status > 299 {
		return nil, fmt.Errorf("the OAuth1 preauthorized request failed (HTTP %d)", status)
	}
	v, _ := url.ParseQuery(string(b))
	o1 := &OAuth1{Token: v.Get("oauth_token"), Secret: v.Get("oauth_token_secret")}
	o2, _, err := a.exchangeOAuth1ForOAuth2(ctx, &consumer, o1)
	if err != nil {
		return nil, err
	}
	return &Tokens{OAuth1: o1, OAuth2: o2, Consumer: &consumer}, nil
}

// exchangeOAuth1ForOAuth2 ports _exchangeOAuth1ForOAuth2 (638-689). status is 0 on a network error.
func (a *Auth) exchangeOAuth1ForOAuth2(ctx context.Context, consumer *Consumer, o1 *OAuth1) (*OAuth2, int, error) {
	u := a.Hosts.connectAPI() + "/oauth-service/oauth/exchange/user/2.0"
	params := map[string]string{
		"oauth_consumer_key": consumer.Key, "oauth_token": o1.Token, "oauth_nonce": nonce(),
		"oauth_signature_method": "HMAC-SHA1", "oauth_timestamp": strconv.FormatInt(a.now().Unix(), 10), "oauth_version": "1.0",
	}
	params["oauth_signature"] = signOAuth1("POST", u, params, consumer.Secret, o1.Secret)
	qv := url.Values{}
	for k, v := range params {
		qv.Set(k, v)
	}
	req, err := http.NewRequestWithContext(ctx, "POST", u+"?"+qv.Encode(), nil)
	if err != nil {
		return nil, 0, err
	}
	req.Header.Set("User-Agent", userAgentApp)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	status, b, err := do(a.client(), req)
	if err != nil {
		return nil, 0, fmt.Errorf("network: %w", err)
	}
	if status < 200 || status > 299 {
		return nil, status, fmt.Errorf("the OAuth2 exchange failed (HTTP %d)", status)
	}
	var r struct {
		AccessToken           string `json:"access_token"`
		TokenType             string `json:"token_type"`
		RefreshToken          string `json:"refresh_token"`
		ExpiresIn             int64  `json:"expires_in"`
		RefreshTokenExpiresIn int64  `json:"refresh_token_expires_in"`
	}
	if err := json.Unmarshal(b, &r); err != nil {
		return nil, status, fmt.Errorf("the OAuth2 exchange answer is not JSON: %w", err)
	}
	now := a.now().Unix()
	return &OAuth2{
		AccessToken: r.AccessToken, TokenType: r.TokenType, RefreshToken: r.RefreshToken,
		ExpiresAt: now + orDefault(r.ExpiresIn, 95760), RefreshTokenExpiresAt: now + orDefault(r.RefreshTokenExpiresIn, 2592000),
	}, status, nil
}

// Fresh ports loadTokens (518-557): it refreshes t when the access token expires within
// margin. refreshed tells the caller to store the new tokens.
func (a *Auth) Fresh(ctx context.Context, t *Tokens, margin time.Duration) (tok *Tokens, refreshed bool, err error) {
	if t.Kind() == KindNone {
		return nil, false, ErrNoTokens
	}
	if a.now().Unix() >= t.AccessExpiresAt()-int64(margin/time.Second) {
		tok, err = a.Refresh(ctx, t)
		return tok, err == nil, err
	}
	return t, false, nil
}

// Refresh ports refresh (563-586) and returns the new tokens without saving them. It returns
// ErrSessionDead only on an answer that proves the login dead (a 400 or 401 from diauth, a
// 401 from the OAuth2 exchange, or a DI login without a refresh token); any other error means
// the caller keeps the old tokens.
func (a *Auth) Refresh(ctx context.Context, t *Tokens) (*Tokens, error) {
	if t.DI != nil {
		if t.DI.RefreshToken == "" {
			return nil, fmt.Errorf("%w: it has no refresh token", ErrSessionDead)
		}
		di, status, err := a.diTokenRequest(ctx, t.DI.ClientID, url.Values{
			"grant_type": {"refresh_token"}, "client_id": {t.DI.ClientID}, "refresh_token": {t.DI.RefreshToken},
		})
		if status == 400 || status == 401 {
			return nil, fmt.Errorf("%w (diauth HTTP %d)", ErrSessionDead, status)
		}
		if err != nil {
			return nil, fmt.Errorf("the Garmin token refresh failed: %w", err)
		}
		return &Tokens{DI: di}, nil
	}
	if !t.isLegacy() {
		return nil, ErrNoTokens
	}
	o2, status, err := a.exchangeOAuth1ForOAuth2(ctx, t.Consumer, t.OAuth1)
	if status == 401 {
		return nil, fmt.Errorf("%w (OAuth2 exchange HTTP 401)", ErrSessionDead)
	}
	if err != nil {
		return nil, fmt.Errorf("the Garmin token refresh failed: %w", err)
	}
	return &Tokens{OAuth1: t.OAuth1, OAuth2: o2, Consumer: t.Consumer}, nil
}

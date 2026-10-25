package gconnect

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"math/rand/v2"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Result is one connectapi answer. Data is nil on a 204, an empty body, or any failure.
type Result struct {
	Data   json.RawMessage
	Status int
	Code   string // "" on success; BAD_JSON, TOO_LARGE, TIMEOUT, NETWORK, AUTH, FORBIDDEN, RATE_LIMIT, HTTP_4XX, HTTP_5XX
	Err    string // redacted; a body preview is at most 200 characters
}

// StopReason says why a Client stopped a run.
type StopReason string

// The reasons a run stops.
const (
	StopSessionDead   StopReason = "session_dead"   // Garmin refused the login; the caller marks it expired
	StopRateLimited   StopReason = "rate_limited"   // 429 after 2 retries
	StopBlocked       StopReason = "blocked"        // 403 on the profile, or 5 in a row
	StopRefreshFailed StopReason = "refresh_failed" // the refresh failed without proving the login dead; keep the tokens
)

// StopError says why the run must stop. Every request after it returns at once.
type StopError struct {
	Reason StopReason
	Msg    string
}

// Error returns Msg.
func (e *StopError) Error() string { return e.Msg }

// Client is a paced, rate limit aware GET client for connectapi.
// It is not safe for concurrent requests: an export asks one thing at a time, and Tokens,
// the pause and the 403 streak are not guarded. Only Stopped may be called from another goroutine.
type Client struct {
	Auth     *Auth
	Tokens   *Tokens
	OnTokens func(*Tokens) error                        // called after every refresh, so the caller can save them; on an error the client logs SAVE_FAILED and goes on
	Gap      time.Duration                              // minimum time between requests; 0 means 500 ms; a negative value disables the pause (for a loopback fake only)
	Timeout  time.Duration                              // per attempt; 0 means 30 s
	Sleep    func(context.Context, time.Duration) error // nil: a timer that ends early when ctx is done
	Jitter   func() time.Duration                       // added to the network backoff; nil means 0 to 999 ms
	Log      func(kind, msg, code string)               // "dim" lines with code "", "warn" lines with an error code

	last      time.Time
	forbidden int
	mu        sync.Mutex // guards stop
	stop      *StopError
}

const (
	refreshMargin    = 900 * time.Second // refresh this long before the access token expires
	maxRetries       = 5                 // network errors and timeouts
	maxRateRetries   = 2                 // HTTP 429
	maxRetryAfter    = 60 * time.Second
	previewMaxLength = 200
	maxBody          = 1 << 20
)

func (c *Client) log(kind, msg, code string) {
	if c.Log != nil {
		c.Log(kind, msg, code)
	}
}

func (c *Client) sleep(ctx context.Context, d time.Duration) error {
	if c.Sleep != nil {
		return c.Sleep(ctx, d)
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-t.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// Stopped is non nil once the run must stop.
func (c *Client) Stopped() *StopError {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.stop
}

func (c *Client) stopWith(reason StopReason, msg string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.stop == nil {
		c.stop = &StopError{reason, msg}
	}
}

// throttle waits until Gap has passed since the last request.
func (c *Client) throttle(ctx context.Context) error {
	gap := c.Gap
	if gap == 0 {
		gap = 500 * time.Millisecond
	}
	if wait := gap - time.Since(c.last); gap > 0 && !c.last.IsZero() && wait > 0 {
		if err := c.sleep(ctx, wait); err != nil {
			return err
		}
	}
	c.last = time.Now()
	return nil
}

// backoff is min(5000 * 2^attempt + jitter, 60000) ms.
func (c *Client) backoff(attempt int) time.Duration {
	j := time.Duration(rand.IntN(1000)) * time.Millisecond
	if c.Jitter != nil {
		j = c.Jitter()
	}
	return min(5*time.Second<<attempt+j, maxRetryAfter)
}

// retryAfter reads a Retry-After value: seconds or an HTTP date, capped at 60 s.
func retryAfter(raw string, now time.Time) (time.Duration, bool) {
	if raw == "" {
		return 0, false
	}
	var d time.Duration
	if secs, err := strconv.ParseFloat(strings.TrimSpace(raw), 64); err == nil {
		if math.IsInf(secs, 0) || math.IsNaN(secs) {
			return 0, false
		}
		d = time.Duration(math.Min(secs, maxRetryAfter.Seconds()) * float64(time.Second))
	} else if at, err := http.ParseTime(raw); err == nil {
		d = at.Sub(now)
	} else {
		return 0, false
	}
	return min(max(d, 0), maxRetryAfter), true
}

// preview is a one line, redacted body preview of at most 200 characters. It redacts first so
// no secret survives the cut.
func preview(body []byte) string {
	s := strings.Join(strings.Fields(Redact(string(body))), " ")
	if r := []rune(s); len(r) > previewMaxLength {
		return string(r[:previewMaxLength]) + "…"
	}
	return s
}

// errText strips the URL that net/http puts in its errors (it can hold a token) and redacts the rest.
func errText(err error) string {
	if ue := (*url.Error)(nil); errors.As(err, &ue) {
		err = ue.Err
	}
	return Redact(err.Error())
}

func on(name string) string {
	if name == "" {
		return ""
	}
	return " on " + name
}

func tag(name string) string {
	if name == "" {
		return ""
	}
	return " [" + name + "]"
}

// Get requests ep with p from the connectapi host, or from the Personal API host when Tokens
// holds a Personal API key.
func (c *Client) Get(ctx context.Context, ep Endpoint, p Params) Result {
	base := c.Auth.Hosts.connectAPI()
	if c.Tokens != nil && c.Tokens.Kind() == KindPersonal {
		base = c.Auth.Hosts.personalAPI()
	}
	return c.get(ctx, ep.Name, ep.URL(base, p), ep.Header)
}

// GetURL requests a full URL with extra headers; it has no endpoint name, so a 403 on it
// never counts as the profile.
func (c *Client) GetURL(ctx context.Context, u string, h http.Header) Result {
	return c.get(ctx, "", u, h)
}

// refreshFailed stops the run after a failed refresh: dead only when Garmin proved it.
func (c *Client) refreshFailed(err error) Result {
	if errors.Is(err, ErrSessionDead) || errors.Is(err, ErrNoTokens) {
		c.stopWith(StopSessionDead, "Garmin no longer accepts the saved login. Sign in again.")
	} else {
		msg := errText(err)
		if msg != "" {
			msg = strings.ToUpper(msg[:1]) + msg[1:] + "."
		}
		c.stopWith(StopRefreshFailed, msg+" The saved login was kept; try again later.")
	}
	return Result{Code: "AUTH", Err: "Auth error: " + errText(err)}
}

func (c *Client) setTokens(t *Tokens) {
	c.Tokens = t
	if c.OnTokens == nil {
		return
	}
	if err := c.OnTokens(t); err != nil {
		c.log("warn", "Could not save the refreshed Garmin tokens: "+errText(err), "SAVE_FAILED")
	}
}

func (c *Client) headers(extra http.Header) http.Header {
	h := Headers(c.Tokens)
	for k, v := range extra {
		h[http.CanonicalHeaderKey(k)] = v
	}
	return h
}

// get sends one request with the retry rules. A second 401 stops with StopSessionDead and
// does not clear anything: the caller decides what to do with the saved login.
func (c *Client) get(ctx context.Context, name, u string, extra http.Header) Result {
	if s := c.Stopped(); s != nil {
		code := "AUTH"
		if s.Reason == StopRateLimited {
			code = "RATE_LIMIT"
		}
		return Result{Code: code, Err: s.Msg + " Further requests were not sent."}
	}
	if err := c.throttle(ctx); err != nil {
		return Result{Code: "NETWORK", Err: "Network error: " + errText(err)}
	}
	tok, refreshed, err := c.Auth.Fresh(ctx, c.Tokens, refreshMargin)
	if err != nil {
		return c.refreshFailed(err)
	}
	if refreshed {
		c.setTokens(tok)
	}

	h := c.headers(extra)
	timeout := c.Timeout
	if timeout == 0 {
		timeout = 30 * time.Second
	}
	retried401, rateAttempt := false, 0
	for attempt := 0; ; {
		status, header, body, err := c.send(ctx, u, h, timeout)
		if err != nil {
			if ctx.Err() != nil {
				return Result{Code: "NETWORK", Err: "Network error: " + errText(ctx.Err())}
			}
			code, label := "NETWORK", "Network error: "+errText(err)
			if ne := net.Error(nil); errors.As(err, &ne) && ne.Timeout() {
				code, label = "TIMEOUT", fmt.Sprintf("Timeout after %dms", timeout.Milliseconds())
			}
			if attempt >= maxRetries {
				return Result{Code: code, Err: label}
			}
			wait := c.backoff(attempt)
			attempt++
			c.log("dim", fmt.Sprintf("[client] %s%s on attempt %d, retrying in %dms", label, on(name), attempt, wait.Milliseconds()), "")
			if err := c.sleep(ctx, wait); err != nil {
				return Result{Code: "NETWORK", Err: "Network error: " + errText(err)}
			}
			continue
		}

		// 401: refresh once and retry. A second 401 or a failed refresh stops the run.
		if status == 401 {
			if !retried401 {
				retried401 = true
				tok, err := c.Auth.Refresh(ctx, c.Tokens)
				if err != nil {
					return c.refreshFailed(err)
				}
				c.setTokens(tok)
				h = c.headers(extra)
				c.log("dim", "[client] 401"+on(name)+": token refreshed, retrying", "")
				continue
			}
			c.stopWith(StopSessionDead, "Garmin no longer accepts the saved login. Sign in again.")
			return Result{Status: 401, Code: "AUTH", Err: "Auth failed (401). Garmin no longer accepts the saved login."}
		}

		// 403: Garmin or Cloudflare refused this one request. Keep the tokens: a new sign in
		// is the step most likely to be blocked.
		if status == 403 {
			c.forbidden++
			const after = " The saved login was kept; try again later or from another network."
			if name == "social_profile" {
				c.stopWith(StopBlocked, "Garmin refused the profile request (HTTP 403)."+after)
			} else if c.forbidden >= 5 {
				c.stopWith(StopBlocked, fmt.Sprintf("Garmin refused %d requests in a row (HTTP 403).", c.forbidden)+after)
			}
			return Result{Status: 403, Code: "FORBIDDEN", Err: "HTTP 403" + tag(name) + ": Garmin refused this request. The saved login was kept."}
		}
		c.forbidden = 0

		// 429: wait for Retry-After or the backoff, at most 2 times, then stop the run.
		if status == 429 {
			if rateAttempt < maxRateRetries {
				wait, ok := retryAfter(header.Get("Retry-After"), time.Now())
				if !ok {
					wait = c.backoff(rateAttempt)
				}
				rateAttempt++
				c.log("dim", fmt.Sprintf("[client] 429 rate-limited%s (retry %d/%d), waiting %dms", on(name), rateAttempt, maxRateRetries, wait.Milliseconds()), "")
				if err := c.sleep(ctx, wait); err != nil {
					return Result{Code: "NETWORK", Err: "Network error: " + errText(err)}
				}
				continue
			}
			c.stopWith(StopRateLimited, "Garmin is rate limiting requests (HTTP 429). Try again in an hour.")
			return Result{Status: 429, Code: "RATE_LIMIT", Err: fmt.Sprintf("Rate limited (429) after %d attempts. Try again later.", rateAttempt+1)}
		}

		if status < 200 || status > 299 {
			code := "HTTP_4XX"
			if status >= 500 {
				code = "HTTP_5XX"
			}
			return Result{Status: status, Code: code, Err: strings.TrimSpace(fmt.Sprintf("HTTP %d: %s", status, preview(body)))}
		}

		// 204 or an empty body: Garmin has no data yet, e.g. a day the watch has not synced.
		if len(body) > maxBody {
			return Result{Status: status, Code: "TOO_LARGE", Err: fmt.Sprintf("HTTP %d%s: the answer is over 1 MB, so it was not read.", status, tag(name))}
		}
		if strings.TrimSpace(string(body)) == "" {
			return Result{Status: status}
		}
		if !json.Valid(body) {
			return Result{Status: status, Code: "BAD_JSON", Err: fmt.Sprintf("Malformed JSON%s. HTTP %d returned a body that is not valid JSON: %s", tag(name), status, preview(body))}
		}
		return Result{Status: status, Data: body}
	}
}

// send makes one attempt and reads at most 1 MB of the answer plus one byte, so the caller
// can tell a full 1 MB answer from a longer one.
func (c *Client) send(ctx context.Context, u string, h http.Header, timeout time.Duration) (int, http.Header, []byte, error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, "GET", u, nil)
	if err != nil {
		return 0, nil, nil, err
	}
	req.Header = h.Clone()
	resp, err := c.Auth.client().Do(req)
	if err != nil {
		return 0, nil, nil, err
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(io.LimitReader(resp.Body, maxBody+1))
	if err != nil {
		return 0, nil, nil, err
	}
	return resp.StatusCode, resp.Header, b, nil
}

// Safe is Get for callers that skip what fails: it returns nil when there is nothing. A failure logs a "warn"
// [skip] line with its code; no data logs a "dim" [no data] line.
func (c *Client) Safe(ctx context.Context, ep Endpoint, p Params) json.RawMessage {
	r := c.Get(ctx, ep, p)
	if r.Code != "" {
		c.log("warn", "[skip] "+ep.Name+": "+r.Err, r.Code)
		return nil
	}
	if r.Data == nil {
		c.log("dim", fmt.Sprintf("[no data] %s (HTTP %d)", ep.Name, r.Status), "")
	}
	return r.Data
}

// Profile returns the display name from the social profile. Six daily URLs need it.
func (c *Client) Profile(ctx context.Context) (string, error) {
	r := c.Get(ctx, endpoints["social_profile"], Params{})
	if r.Code != "" {
		return "", errors.New(r.Err)
	}
	var p struct{ DisplayName string }
	if json.Unmarshal(r.Data, &p) != nil || p.DisplayName == "" {
		return "", errors.New("displayName not found in social profile response")
	}
	return p.DisplayName, nil
}

// Package gconnect signs in to Garmin Connect. It never touches the disk or the
// environment: the caller stores the tokens and picks the hosts. The token types keep
// the field names of electron-app/garmin/auth.js, so a caller can save them as tokens.json.
package gconnect

type DI struct {
	AccessToken           string `json:"access_token"`
	RefreshToken          string `json:"refresh_token,omitempty"`
	ClientID              string `json:"client_id"`
	ExpiresAt             int64  `json:"expires_at"`
	RefreshTokenExpiresAt *int64 `json:"refresh_token_expires_at"` // null when Garmin sends no lifetime
}

type OAuth1 struct {
	Token  string `json:"oauth_token"`
	Secret string `json:"oauth_token_secret"`
}

type OAuth2 struct {
	AccessToken           string `json:"access_token"`
	TokenType             string `json:"token_type"`
	RefreshToken          string `json:"refresh_token"`
	ExpiresAt             int64  `json:"expires_at"`
	RefreshTokenExpiresAt int64  `json:"refresh_token_expires_at"`
}

type Consumer struct {
	Key    string `json:"consumer_key"`
	Secret string `json:"consumer_secret"`
}

type Tokens struct {
	DI       *DI       `json:"di,omitempty"`
	OAuth1   *OAuth1   `json:"oauth1,omitempty"`
	OAuth2   *OAuth2   `json:"oauth2,omitempty"`
	Consumer *Consumer `json:"consumer,omitempty"`
}

type Kind int

const (
	KindNone Kind = iota
	KindDI
	KindLegacy
)

func (t *Tokens) isDI() bool     { return t.DI != nil && t.DI.AccessToken != "" }
func (t *Tokens) isLegacy() bool { return t.OAuth1 != nil && t.OAuth2 != nil && t.Consumer != nil }

// Kind is KindNone when the tokens hold neither a complete DI nor a complete legacy login.
func (t *Tokens) Kind() Kind {
	switch {
	case t.isDI():
		return KindDI
	case t.isLegacy():
		return KindLegacy
	}
	return KindNone
}

// AccessExpiresAt is the access token's expiry in unix seconds. The OAuth1 token mints new
// OAuth2 tokens, so a legacy login's refresh expiry does not matter.
func (t *Tokens) AccessExpiresAt() int64 {
	if t.isDI() {
		return t.DI.ExpiresAt
	}
	if t.OAuth2 != nil {
		return t.OAuth2.ExpiresAt
	}
	return 0
}

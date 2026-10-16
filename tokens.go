package gconnect

// The token types keep the JSON field names Garmin and the garth project use, so a caller
// can marshal Tokens as is and read the file back later.

// DI is a login from the DI mobile flow (diauth.garmin.com).
type DI struct {
	AccessToken           string `json:"access_token"`
	RefreshToken          string `json:"refresh_token,omitempty"`
	ClientID              string `json:"client_id"`
	ExpiresAt             int64  `json:"expires_at"`
	RefreshTokenExpiresAt *int64 `json:"refresh_token_expires_at"` // null when Garmin sends no lifetime
}

// OAuth1 is the long lived token of the older login. It mints new OAuth2 tokens.
type OAuth1 struct {
	Token  string `json:"oauth_token"`
	Secret string `json:"oauth_token_secret"`
}

// OAuth2 is the access token of the older login. Times are unix seconds.
type OAuth2 struct {
	AccessToken           string `json:"access_token"`
	TokenType             string `json:"token_type"`
	RefreshToken          string `json:"refresh_token"`
	ExpiresAt             int64  `json:"expires_at"`
	RefreshTokenExpiresAt int64  `json:"refresh_token_expires_at"`
}

// Consumer is the OAuth1 consumer key and secret the older login signs with. Login fetches it
// from Hosts.Consumer.
type Consumer struct {
	Key    string `json:"consumer_key"`
	Secret string `json:"consumer_secret"`
}

// Tokens holds one login: DI, or OAuth1 + OAuth2 + Consumer for the older flow.
type Tokens struct {
	DI       *DI       `json:"di,omitempty"`
	OAuth1   *OAuth1   `json:"oauth1,omitempty"`
	OAuth2   *OAuth2   `json:"oauth2,omitempty"`
	Consumer *Consumer `json:"consumer,omitempty"`
}

// Kind says which login a Tokens value holds.
type Kind int

// The kinds of login. KindNone means neither login is complete.
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

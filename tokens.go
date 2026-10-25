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

// Personal is a Personal API key and the access token it last bought. The key comes from
// connect.garmin.com/app/settings/personalApiToken and lives 30 to 365 days; each access
// token lives 24 hours and has no refresh token, so the key buys the next one.
type Personal struct {
	APIKey      string `json:"api_key"`
	AccessToken string `json:"access_token,omitempty"`
	ExpiresAt   int64  `json:"expires_at,omitempty"`
}

// Tokens holds one login: DI, OAuth1 + OAuth2 + Consumer for the older flow, or a Personal
// API key.
type Tokens struct {
	DI       *DI       `json:"di,omitempty"`
	OAuth1   *OAuth1   `json:"oauth1,omitempty"`
	OAuth2   *OAuth2   `json:"oauth2,omitempty"`
	Consumer *Consumer `json:"consumer,omitempty"`
	Personal *Personal `json:"personal,omitempty"`
}

// Kind says which login a Tokens value holds.
type Kind int

// The kinds of login. KindNone means neither login is complete.
const (
	KindNone Kind = iota
	KindDI
	KindLegacy
	KindPersonal // data comes from the Personal API host, which answers a smaller set of paths
)

func (t *Tokens) isDI() bool     { return t.DI != nil && t.DI.AccessToken != "" }
func (t *Tokens) isLegacy() bool { return t.OAuth1 != nil && t.OAuth2 != nil && t.Consumer != nil }
func (t *Tokens) isPersonal() bool {
	return t.Personal != nil && t.Personal.APIKey != ""
}

// Kind is KindNone when the tokens hold no complete login. A Personal API key wins over the
// other two when a value holds more than one.
func (t *Tokens) Kind() Kind {
	switch {
	case t.isPersonal():
		return KindPersonal
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
	if t.isPersonal() {
		return t.Personal.ExpiresAt
	}
	if t.isDI() {
		return t.DI.ExpiresAt
	}
	if t.OAuth2 != nil {
		return t.OAuth2.ExpiresAt
	}
	return 0
}

// Package apiclient is the one HTTP client for the agnthub registry API: it honors the
// proxy variables, sends the user's API key to the configured API origin and nowhere else,
// waits out a short rate limit once and turns the API's credential and rate-limit refusals
// into errors people can act on.
package apiclient

import (
	"strings"

	"github.com/agnthub-sh/agnthub/platform/env"
)

// DefaultURL is the production registry API.
const DefaultURL = "https://api.agnthub.sh"

// URLVariable overrides the registry API origin.
const URLVariable = "AGNTHUB_API_URL"

// KeyVariable carries an API key; it wins over the configuration's apiKey.
const KeyVariable = "AGNTHUB_API_KEY"

// KeyPrefix starts every agnthub API key. Keys are otherwise opaque to the CLI.
const KeyPrefix = "agh_"

// KeyPage is where a signed-in user creates, rotates or revokes their API key.
const KeyPage = "https://app.agnthub.sh/api-key"

// URL is the registry API origin: AGNTHUB_API_URL, else configured (the user
// configuration's apiURL), else DefaultURL.
func URL(e env.Env, configured string) string {
	if override := e.Get(URLVariable); override != "" {
		return override
	}
	if configured != "" {
		return configured
	}
	return DefaultURL
}

// Source is where the API key in use came from.
type Source string

// Key sources.
const (
	SourceNone   Source = ""
	SourceEnv    Source = "env"
	SourceConfig Source = "config"
)

// Credential is the API key requests to the registry API carry; a zero Credential is
// anonymous.
type Credential struct {
	Key    string
	Source Source
}

// MalformedKeyError is a key that cannot be an agnthub API key. It is refused before any
// request, and requests to the API fail with it rather than go out anonymously.
type MalformedKeyError struct {
	Source Source
}

func (e *MalformedKeyError) Error() string {
	from := "the API key"
	switch e.Source {
	case SourceEnv:
		from = KeyVariable
	case SourceConfig:
		from = "apiKey in the agnthub configuration"
	}
	return from + " is not an agnthub API key (keys start with " + KeyPrefix +
		"); copy it again from " + KeyPage
}

// Config is where the registry API is and what credential goes there.
type Config struct {
	// URL is the API origin.
	URL        string
	Credential Credential
	// Err is a configured key that cannot be used: requests to the API fail with it.
	Err error
}

// Load resolves the API origin and credential from e and the user configuration's
// apiURL and apiKey.
func Load(e env.Env, configuredURL, configuredKey string) Config {
	credential, err := Resolve(e, configuredKey)
	return Config{URL: URL(e, configuredURL), Credential: credential, Err: err}
}

// Resolve picks the API key: AGNTHUB_API_KEY, else configured (the configuration's
// apiKey), else none. A key without the agnthub prefix is a MalformedKeyError.
func Resolve(e env.Env, configured string) (Credential, error) {
	credential := Credential{Key: strings.TrimSpace(e.Get(KeyVariable)), Source: SourceEnv}
	if credential.Key == "" {
		credential = Credential{Key: strings.TrimSpace(configured), Source: SourceConfig}
	}
	if credential.Key == "" {
		return Credential{}, nil
	}
	if !ValidKey(credential.Key) {
		return Credential{Source: credential.Source}, &MalformedKeyError{Source: credential.Source}
	}
	return credential, nil
}

// ValidKey reports a key that can be an agnthub API key: the prefix, then printable
// ASCII without spaces, so it can never break out of a header.
func ValidKey(key string) bool {
	if len(key) <= len(KeyPrefix) || !strings.HasPrefix(key, KeyPrefix) {
		return false
	}
	for i := len(KeyPrefix); i < len(key); i++ {
		if key[i] <= ' ' || key[i] > '~' {
			return false
		}
	}
	return true
}

// Mask shows a key the way the site does: the prefix and its last four characters.
func Mask(key string) string {
	if len(key) < len(KeyPrefix)+4 {
		return KeyPrefix + "…"
	}
	return KeyPrefix + "…" + key[len(key)-4:]
}

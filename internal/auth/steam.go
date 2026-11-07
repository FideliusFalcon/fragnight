package auth

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"strings"
)

// SteamOpenIDEndpoint is Steam's OpenID 2.0 endpoint.
const SteamOpenIDEndpoint = "https://steamcommunity.com/openid/login"

// CallbackPath is where Steam sends the browser back to.
const CallbackPath = "/auth/steam/callback"

var claimedIDRe = regexp.MustCompile(`^https://steamcommunity\.com/openid/id/(\d{17})$`)

// LoginURL returns the Steam URL the browser should be redirected to. state is a
// per-attempt random value; Steam signs it into return_to, and the caller checks
// it against a cookie so a callback cannot be replayed or injected into another
// browser.
func LoginURL(baseURL, state string) string {
	v := url.Values{
		"openid.ns":         {"http://specs.openid.net/auth/2.0"},
		"openid.mode":       {"checkid_setup"},
		"openid.return_to":  {baseURL + CallbackPath + "?state=" + url.QueryEscape(state)},
		"openid.realm":      {baseURL},
		"openid.identity":   {"http://specs.openid.net/auth/2.0/identifier_select"},
		"openid.claimed_id": {"http://specs.openid.net/auth/2.0/identifier_select"},
	}
	return SteamOpenIDEndpoint + "?" + v.Encode()
}

// Verify checks a Steam OpenID callback and returns the SteamID64. endpoint is
// Steam's OpenID URL (injectable for tests).
func Verify(ctx context.Context, c *http.Client, endpoint, baseURL string, q url.Values) (string, error) {
	// A key given twice could make the value we check differ from the one Steam signs.
	for k, vs := range q {
		if strings.HasPrefix(k, "openid.") && len(vs) != 1 {
			return "", fmt.Errorf("steam login: duplicate parameter %s", k)
		}
	}
	if q.Get("openid.mode") != "id_res" {
		return "", errors.New("steam login: unexpected openid.mode")
	}
	m := claimedIDRe.FindStringSubmatch(q.Get("openid.claimed_id"))
	if m == nil {
		return "", errors.New("steam login: bad claimed_id")
	}
	if q.Get("openid.identity") != q.Get("openid.claimed_id") {
		return "", errors.New("steam login: identity does not match claimed_id")
	}
	wantReturn := baseURL + CallbackPath
	st := q.Get("state")
	if st == "" {
		return "", errors.New("steam login: missing state")
	}
	wantReturn += "?state=" + url.QueryEscape(st)
	if q.Get("openid.return_to") != wantReturn {
		return "", errors.New("steam login: return_to mismatch")
	}
	signed := strings.Split(q.Get("openid.signed"), ",")
	for _, field := range []string{"claimed_id", "identity", "return_to", "response_nonce"} {
		if !slices.Contains(signed, field) {
			return "", fmt.Errorf("steam login: %s is not signed", field)
		}
	}
	form := url.Values{}
	for k, vs := range q {
		if strings.HasPrefix(k, "openid.") {
			form[k] = vs
		}
	}
	form.Set("openid.mode", "check_authentication")
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := c.Do(req)
	if err != nil {
		return "", fmt.Errorf("steam login: contacting steam: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<16))
	if err != nil {
		return "", err
	}
	if !strings.Contains(string(body), "is_valid:true") {
		return "", errors.New("steam login: steam did not validate the response")
	}
	return m[1], nil
}

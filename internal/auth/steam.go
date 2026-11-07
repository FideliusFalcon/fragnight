package auth

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
)

// SteamOpenIDEndpoint is Steam's OpenID 2.0 endpoint.
const SteamOpenIDEndpoint = "https://steamcommunity.com/openid/login"

// CallbackPath is where Steam sends the browser back to.
const CallbackPath = "/auth/steam/callback"

var claimedIDRe = regexp.MustCompile(`^https://steamcommunity\.com/openid/id/(\d{17})$`)

// LoginURL returns the Steam URL the browser should be redirected to.
func LoginURL(baseURL string) string {
	v := url.Values{
		"openid.ns":         {"http://specs.openid.net/auth/2.0"},
		"openid.mode":       {"checkid_setup"},
		"openid.return_to":  {baseURL + CallbackPath},
		"openid.realm":      {baseURL},
		"openid.identity":   {"http://specs.openid.net/auth/2.0/identifier_select"},
		"openid.claimed_id": {"http://specs.openid.net/auth/2.0/identifier_select"},
	}
	return SteamOpenIDEndpoint + "?" + v.Encode()
}

// Verify checks a Steam OpenID callback and returns the SteamID64. endpoint is
// Steam's OpenID URL (injectable for tests).
func Verify(ctx context.Context, c *http.Client, endpoint, baseURL string, q url.Values) (string, error) {
	if q.Get("openid.mode") != "id_res" {
		return "", errors.New("steam login: unexpected openid.mode")
	}
	m := claimedIDRe.FindStringSubmatch(q.Get("openid.claimed_id"))
	if m == nil {
		return "", errors.New("steam login: bad claimed_id")
	}
	if q.Get("openid.return_to") != baseURL+CallbackPath {
		return "", errors.New("steam login: return_to mismatch")
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

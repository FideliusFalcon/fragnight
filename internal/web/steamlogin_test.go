package web

import (
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

// roundTrip lets Steam's endpoint (the fake server) through and fails every other
// outgoing request (such as the profile lookup), so no test touches the internet.
type roundTrip func(*http.Request) (*http.Response, error)

func (f roundTrip) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func steamEnv(t *testing.T) *env {
	t.Helper()
	e := newEnv(t, false)
	fake := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, "is_valid:true\n")
	}))
	t.Cleanup(fake.Close)
	e.srv.SteamEndpoint = fake.URL
	e.srv.HTTP = &http.Client{Transport: roundTrip(func(r *http.Request) (*http.Response, error) {
		if r.URL.Host == strings.TrimPrefix(fake.URL, "http://") {
			return http.DefaultTransport.RoundTrip(r)
		}
		return &http.Response{StatusCode: 500, Body: io.NopCloser(strings.NewReader("")), Header: http.Header{}}, nil
	})}
	return e
}

func browser() *http.Client {
	jar, _ := cookiejar.New(nil)
	return &http.Client{Jar: jar, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
}

// startLogin follows /auth/steam and returns the state Steam would echo back.
func startLogin(t *testing.T, e *env, c *http.Client) string {
	t.Helper()
	resp, err := c.Get(e.ts.URL + "/auth/steam")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	loc, err := url.Parse(resp.Header.Get("Location"))
	if err != nil || loc.Host != "steamcommunity.com" {
		t.Fatalf("redirect %q", resp.Header.Get("Location"))
	}
	rt, _ := url.Parse(loc.Query().Get("openid.return_to"))
	state := rt.Query().Get("state")
	if len(state) < 16 {
		t.Fatalf("weak or missing state %q", state)
	}
	return state
}

func callbackURL(e *env, state string) string {
	claimed := "https://steamcommunity.com/openid/id/" + sid(5)
	q := url.Values{
		"state":             {state},
		"openid.mode":       {"id_res"},
		"openid.claimed_id": {claimed},
		"openid.identity":   {claimed},
		"openid.return_to":  {"http://lan.local/auth/steam/callback?state=" + state},
		"openid.signed":     {"signed,op_endpoint,claimed_id,identity,return_to,response_nonce"},
		"openid.sig":        {"x"},
	}
	return e.ts.URL + "/auth/steam/callback?" + q.Encode()
}

func TestSteamCallback_LogsInWithMatchingState(t *testing.T) {
	e := steamEnv(t)
	c := browser()
	state := startLogin(t, e, c)
	resp, err := c.Get(callbackURL(e, state))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusSeeOther || resp.Header.Get("Location") != "/" {
		t.Fatalf("status %d location %q", resp.StatusCode, resp.Header.Get("Location"))
	}
	idx, _ := c.Get(e.ts.URL + "/")
	idx.Body.Close()
	if idx.StatusCode != 200 {
		t.Fatalf("not logged in after callback: %d", idx.StatusCode)
	}
}

func TestSteamCallback_RejectsReplayInAnotherBrowser(t *testing.T) {
	e := steamEnv(t)
	attacker := browser()
	state := startLogin(t, e, attacker)
	// the victim never started a login, but is lured to the attacker's valid callback URL
	victim := browser()
	resp, err := victim.Get(callbackURL(e, state))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if loc := resp.Header.Get("Location"); !strings.HasPrefix(loc, "/login") {
		t.Fatalf("victim was logged in: location %q", loc)
	}
	idx, _ := victim.Get(e.ts.URL + "/")
	idx.Body.Close()
	if idx.StatusCode == 200 {
		t.Fatal("victim has a session")
	}
}

func TestSteamCallback_RejectsWrongState(t *testing.T) {
	e := steamEnv(t)
	c := browser()
	startLogin(t, e, c)
	resp, err := c.Get(callbackURL(e, "0123456789abcdef0123456789abcdef"))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if loc := resp.Header.Get("Location"); !strings.HasPrefix(loc, "/login") {
		t.Fatalf("location %q", loc)
	}
}

func TestSteamCallback_StateIsSingleUse(t *testing.T) {
	e := steamEnv(t)
	c := browser()
	state := startLogin(t, e, c)
	r1, _ := c.Get(callbackURL(e, state))
	r1.Body.Close()
	again := browser()
	// even if the cookie were somehow present again, a second login needs a fresh /auth/steam
	r2, _ := again.Get(callbackURL(e, state))
	r2.Body.Close()
	if !strings.HasPrefix(r2.Header.Get("Location"), "/login") {
		t.Fatalf("replay accepted: %q", r2.Header.Get("Location"))
	}
}

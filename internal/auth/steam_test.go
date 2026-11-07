package auth

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

const base = "http://lan.local:8080"
const sid = "76561198000000000"

func validQuery() url.Values {
	return url.Values{
		"openid.mode":       {"id_res"},
		"openid.claimed_id": {"https://steamcommunity.com/openid/id/" + sid},
		"openid.identity":   {"https://steamcommunity.com/openid/id/" + sid},
		"openid.return_to":  {base + "/auth/steam/callback"},
		"openid.sig":        {"abc"},
		"openid.signed":     {"signed"},
	}
}

func steamServer(t *testing.T, reply string, calls *int) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		*calls++
		r.ParseForm()
		if r.PostForm.Get("openid.mode") != "check_authentication" {
			t.Errorf("mode = %q, want check_authentication", r.PostForm.Get("openid.mode"))
		}
		w.Write([]byte(reply))
	}))
}

func TestLoginURL(t *testing.T) {
	u := LoginURL(base)
	if !strings.HasPrefix(u, "https://steamcommunity.com/openid/login?") {
		t.Fatal(u)
	}
	q, _ := url.Parse(u)
	v := q.Query()
	if v.Get("openid.return_to") != base+"/auth/steam/callback" || v.Get("openid.realm") != base {
		t.Fatalf("params: %v", v)
	}
}

func TestVerify_AcceptsValid(t *testing.T) {
	calls := 0
	srv := steamServer(t, "ns:http://specs.openid.net/auth/2.0\nis_valid:true\n", &calls)
	defer srv.Close()
	got, err := Verify(context.Background(), srv.Client(), srv.URL, base, validQuery())
	if err != nil || got != sid {
		t.Fatalf("got %q, %v", got, err)
	}
}

func TestVerify_RejectsInvalidReply(t *testing.T) {
	calls := 0
	srv := steamServer(t, "is_valid:false\n", &calls)
	defer srv.Close()
	if _, err := Verify(context.Background(), srv.Client(), srv.URL, base, validQuery()); err == nil {
		t.Fatal("want error")
	}
}

func TestVerify_RejectsBadClaimedID(t *testing.T) {
	calls := 0
	srv := steamServer(t, "is_valid:true\n", &calls)
	defer srv.Close()
	for _, claimed := range []string{
		"https://evil.example/openid/id/" + sid,
		"https://steamcommunity.com/openid/id/123",
		"https://steamcommunity.com/openid/id/" + sid + "x",
		"",
	} {
		q := validQuery()
		q.Set("openid.claimed_id", claimed)
		if _, err := Verify(context.Background(), srv.Client(), srv.URL, base, q); err == nil {
			t.Errorf("claimed_id %q accepted", claimed)
		}
	}
	if calls != 0 {
		t.Fatalf("network called %d times before rejecting", calls)
	}
}

func TestVerify_RejectsWrongReturnTo(t *testing.T) {
	calls := 0
	srv := steamServer(t, "is_valid:true\n", &calls)
	defer srv.Close()
	q := validQuery()
	q.Set("openid.return_to", "http://evil.example/auth/steam/callback")
	if _, err := Verify(context.Background(), srv.Client(), srv.URL, base, q); err == nil {
		t.Fatal("want error")
	}
	if calls != 0 {
		t.Fatal("network called")
	}
}

func TestVerify_RejectsWrongMode(t *testing.T) {
	calls := 0
	srv := steamServer(t, "is_valid:true\n", &calls)
	defer srv.Close()
	q := validQuery()
	q.Set("openid.mode", "cancel")
	if _, err := Verify(context.Background(), srv.Client(), srv.URL, base, q); err == nil {
		t.Fatal("want error")
	}
}

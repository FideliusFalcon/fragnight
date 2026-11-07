package auth

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func issue(t *testing.T, s *Sessions) *http.Cookie {
	t.Helper()
	rec := httptest.NewRecorder()
	s.Issue(rec, "76561198000000000")
	cs := rec.Result().Cookies()
	if len(cs) != 1 {
		t.Fatalf("cookies: %v", cs)
	}
	return cs[0]
}

func req(c *http.Cookie) *http.Request {
	r := httptest.NewRequest("GET", "/", nil)
	r.AddCookie(c)
	return r
}

func TestSession_RoundTrip(t *testing.T) {
	s := NewSessions([]byte("secret"), time.Hour)
	c := issue(t, s)
	if !c.HttpOnly || c.SameSite != http.SameSiteLaxMode {
		t.Fatalf("cookie flags: %+v", c)
	}
	id, ok := s.SteamID(req(c))
	if !ok || id != "76561198000000000" {
		t.Fatalf("got %q %v", id, ok)
	}
}

func TestSession_RejectsTamperedCookie(t *testing.T) {
	s := NewSessions([]byte("secret"), time.Hour)
	c := issue(t, s)
	c.Value = "76561198999999999" + c.Value[17:]
	if _, ok := s.SteamID(req(c)); ok {
		t.Fatal("tampered cookie accepted")
	}
	other := NewSessions([]byte("different"), time.Hour)
	if _, ok := other.SteamID(req(issue(t, s))); ok {
		t.Fatal("cookie from another secret accepted")
	}
}

func TestSession_RejectsExpired(t *testing.T) {
	s := NewSessions([]byte("secret"), time.Hour)
	now := time.Now()
	s.Now = func() time.Time { return now }
	c := issue(t, s)
	s.Now = func() time.Time { return now.Add(2 * time.Hour) }
	if _, ok := s.SteamID(req(c)); ok {
		t.Fatal("expired cookie accepted")
	}
}

func TestSession_NoCookie(t *testing.T) {
	s := NewSessions([]byte("secret"), time.Hour)
	if _, ok := s.SteamID(httptest.NewRequest("GET", "/", nil)); ok {
		t.Fatal("want no session")
	}
}

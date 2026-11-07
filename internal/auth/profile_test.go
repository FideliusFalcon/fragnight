package auth

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
)

// redirect sends every request to the test server regardless of host.
type redirect struct{ to *url.URL }

func (r redirect) RoundTrip(req *http.Request) (*http.Response, error) {
	req = req.Clone(req.Context())
	req.URL.Scheme, req.URL.Host = r.to.Scheme, r.to.Host
	return http.DefaultTransport.RoundTrip(req)
}

func client(srv *httptest.Server) *http.Client {
	u, _ := url.Parse(srv.URL)
	return &http.Client{Transport: redirect{u}}
}

func TestFetchProfile_FallsBackToSteamID(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "boom", 500)
	}))
	defer srv.Close()
	p := FetchProfile(context.Background(), client(srv), "", sid)
	if p.SteamID != sid || p.Name != sid {
		t.Fatalf("got %+v", p)
	}
}

func TestFetchProfile_ParsesPublicXML(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/profiles/"+sid {
			t.Errorf("path %s", r.URL.Path)
		}
		w.Write([]byte(`<?xml version="1.0"?><profile><steamID64>` + sid + `</steamID64><steamID><![CDATA[Guso <3]]></steamID><avatarFull><![CDATA[http://img/a.jpg]]></avatarFull></profile>`))
	}))
	defer srv.Close()
	p := FetchProfile(context.Background(), client(srv), "", sid)
	if p.Name != "Guso <3" || p.AvatarURL != "http://img/a.jpg" {
		t.Fatalf("got %+v", p)
	}
}

func TestFetchProfile_UsesAPIWhenKeyGiven(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("key") != "K" || r.URL.Query().Get("steamids") != sid {
			t.Errorf("query %v", r.URL.Query())
		}
		w.Write([]byte(`{"response":{"players":[{"personaname":"ApiName","avatarfull":"http://img/b.jpg"}]}}`))
	}))
	defer srv.Close()
	p := FetchProfile(context.Background(), client(srv), "K", sid)
	if p.Name != "ApiName" || p.AvatarURL != "http://img/b.jpg" {
		t.Fatalf("got %+v", p)
	}
}

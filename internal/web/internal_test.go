package web

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/fideliusfalcon/fragnight/internal/store"
)

func TestConfigEndpoint_RejectsWrongToken(t *testing.T) {
	e := newEnv(t, false)
	ctx := context.Background()
	e.store.UpsertPlayer(ctx, store.Player{SteamID: sid(1), Name: "a"})
	err := e.store.CreateMatch(ctx, store.Match{ID: "m1", LobbyID: "l", LobbyName: "L", Map: "de_dust2", SettingsJSON: "{}",
		Token: "right-token", ConfigJSON: `{"matchid":"m1"}`}, []store.RosterEntry{{SteamID: sid(1), Team: 1}})
	if err != nil {
		t.Fatal(err)
	}
	get := func(path string) (int, string) {
		resp, err := http.Get(e.ts.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, string(b)
	}
	for _, path := range []string{
		"/internal/match/m1/config.json",
		"/internal/match/m1/config.json?token=wrong",
		"/internal/match/m1/config.json?token=",
	} {
		if code, _ := get(path); code != http.StatusForbidden {
			t.Errorf("%s: status %d, want 403", path, code)
		}
	}
	code, body := get("/internal/match/m1/config.json?token=right-token")
	if code != 200 || !strings.Contains(body, `"matchid":"m1"`) {
		t.Fatalf("status %d body %s", code, body)
	}
	if code, _ := get("/internal/match/unknown/config.json?token=right-token"); code != http.StatusNotFound {
		t.Fatalf("unknown match: %d", code)
	}
	// once a match has ended its config is no longer served
	e.store.SetMatchStatus(ctx, "m1", store.MatchAborted, 1)
	if code, _ := get("/internal/match/m1/config.json?token=right-token"); code != http.StatusNotFound {
		t.Fatalf("ended match: %d", code)
	}
	_ = context.Background
}

func postEvent(t *testing.T, e *env, serverID, secret, body string) int {
	t.Helper()
	req, _ := http.NewRequest("POST", e.ts.URL+"/internal/events/"+serverID, strings.NewReader(body))
	if secret != "" {
		req.Header.Set("X-Webhook-Secret", secret)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	return resp.StatusCode
}

func TestWebhook_WrongSecretForbidden(t *testing.T) {
	e := newEnv(t, false)
	body := `{"event":"map_result","matchid":"m1"}`
	for _, secret := range []string{"", "wrong", "hook-secre"} {
		if code := postEvent(t, e, "s1", secret, body); code != http.StatusForbidden {
			t.Errorf("secret %q: status %d", secret, code)
		}
	}
	if len(e.matches.events) != 0 {
		t.Fatalf("event delivered with bad secret: %v", e.matches.events)
	}
}

func TestWebhook_NoConfiguredSecretRejectsEverything(t *testing.T) {
	e := newEnv(t, false)
	e.srv.Config.WebhookSecret = ""
	if code := postEvent(t, e, "s1", "", `{"event":"going_live"}`); code != http.StatusForbidden {
		t.Fatalf("status %d", code)
	}
}

func TestWebhook_DeliversParsedEvent(t *testing.T) {
	e := newEnv(t, false)
	if code := postEvent(t, e, "cs2-1", "hook-secret", `{"event":"round_end","matchid":"m1"}`); code != 200 {
		t.Fatalf("status %d", code)
	}
	if len(e.matches.events) != 1 || e.matches.events[0] != "cs2-1:round_end:m1" {
		t.Fatalf("events %v", e.matches.events)
	}
}

func TestWebhook_GarbageBodyIsBadRequest(t *testing.T) {
	e := newEnv(t, false)
	if code := postEvent(t, e, "s1", "hook-secret", `not json`); code != http.StatusBadRequest {
		t.Fatalf("status %d", code)
	}
	if len(e.matches.events) != 0 {
		t.Fatal("garbage delivered")
	}
}

package auth

import (
	"context"
	"encoding/json"
	"encoding/xml"
	"net/http"
	"net/url"

	"github.com/fideliusfalcon/fragnight/internal/store"
)

// FetchProfile looks up a player's display name and avatar. It never fails: any
// error falls back to using the SteamID as the name.
func FetchProfile(ctx context.Context, c *http.Client, apiKey, steamID string) store.Player {
	fallback := store.Player{SteamID: steamID, Name: steamID}
	if apiKey != "" {
		if p, ok := fromAPI(ctx, c, apiKey, steamID); ok {
			return p
		}
	}
	if p, ok := fromXML(ctx, c, steamID); ok {
		return p
	}
	return fallback
}

func get(ctx context.Context, c *http.Client, u string) (*http.Response, bool) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, false
	}
	resp, err := c.Do(req)
	if err != nil {
		return nil, false
	}
	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		return nil, false
	}
	return resp, true
}

func fromAPI(ctx context.Context, c *http.Client, key, steamID string) (store.Player, bool) {
	q := url.Values{"key": {key}, "steamids": {steamID}}
	resp, ok := get(ctx, c, "https://api.steampowered.com/ISteamUser/GetPlayerSummaries/v2/?"+q.Encode())
	if !ok {
		return store.Player{}, false
	}
	defer resp.Body.Close()
	var out struct {
		Response struct {
			Players []struct {
				Name   string `json:"personaname"`
				Avatar string `json:"avatarfull"`
			} `json:"players"`
		} `json:"response"`
	}
	if json.NewDecoder(resp.Body).Decode(&out) != nil || len(out.Response.Players) == 0 || out.Response.Players[0].Name == "" {
		return store.Player{}, false
	}
	p := out.Response.Players[0]
	return store.Player{SteamID: steamID, Name: p.Name, AvatarURL: p.Avatar}, true
}

func fromXML(ctx context.Context, c *http.Client, steamID string) (store.Player, bool) {
	resp, ok := get(ctx, c, "https://steamcommunity.com/profiles/"+steamID+"?xml=1")
	if !ok {
		return store.Player{}, false
	}
	defer resp.Body.Close()
	var out struct {
		Name   string `xml:"steamID"`
		Avatar string `xml:"avatarFull"`
	}
	if xml.NewDecoder(resp.Body).Decode(&out) != nil || out.Name == "" {
		return store.Player{}, false
	}
	return store.Player{SteamID: steamID, Name: out.Name, AvatarURL: out.Avatar}, true
}

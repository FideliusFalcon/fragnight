// Package match connects lobbies to CS2 servers: it starts matches, follows
// MatchZy events and records results.
package match

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"time"

	"github.com/fideliusfalcon/fragnight/internal/lobby"
	"github.com/fideliusfalcon/fragnight/internal/matchzy"
	"github.com/fideliusfalcon/fragnight/internal/pool"
	"github.com/fideliusfalcon/fragnight/internal/store"
)

const rconTimeout = 10 * time.Second

type Deps struct {
	Lobbies       *lobby.Manager
	Pool          *pool.Pool
	RCON          pool.RCON
	Store         *store.Store
	BaseURL       string
	WebhookSecret string
	// Admins are SteamIDs allowed to abort any match.
	Admins []string
	Now    func() time.Time
}

type Orchestrator struct {
	Deps
}

func New(d Deps) *Orchestrator {
	if d.Now == nil {
		d.Now = time.Now
	}
	return &Orchestrator{Deps: d}
}

func randHex(nBytes int) string {
	b := make([]byte, nBytes)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b)
}

// Start prepares the match for a lobby whose vote just finished and launches it
// on a free server (or queues it). It blocks while talking to the server, so
// callers on a shared loop should run it in a goroutine.
func (o *Orchestrator) Start(lobbyID, mapName string) {
	ctx := context.Background()
	if _, err := o.Store.ActiveMatchOfLobby(ctx, lobbyID); err == nil {
		return // already prepared
	}
	snap, ok := o.Lobbies.Get(lobbyID)
	if !ok {
		return
	}
	var team1, team2 []lobby.Player
	var roster []store.RosterEntry
	for _, m := range snap.Members {
		switch m.Team {
		case lobby.Team1:
			team1 = append(team1, m.Player)
			roster = append(roster, store.RosterEntry{SteamID: m.SteamID, Team: 1})
		case lobby.Team2:
			team2 = append(team2, m.Player)
			roster = append(roster, store.RosterEntry{SteamID: m.SteamID, Team: 2})
		}
	}
	matchID := randHex(8)
	cfg, err := matchzy.BuildConfig(matchzy.ConfigInput{
		MatchID: matchID, Map: mapName, Team1Name: "Team 1", Team2Name: "Team 2",
		Team1: team1, Team2: team2, Settings: snap.Settings,
	})
	if err != nil {
		o.Lobbies.SetStatus(lobbyID, lobby.StatusWaiting, "Could not start: "+err.Error())
		return
	}
	settingsJSON, _ := json.Marshal(snap.Settings)
	err = o.Store.CreateMatch(ctx, store.Match{
		ID: matchID, LobbyID: lobbyID, LobbyName: snap.Name, Map: mapName,
		SettingsJSON: string(settingsJSON), Token: randHex(16), ConfigJSON: string(cfg),
		StartedAt: o.Now().Unix(),
	}, roster)
	if err != nil {
		log.Printf("create match: %v", err)
		o.Lobbies.SetStatus(lobbyID, lobby.StatusWaiting, "Could not start: database error")
		return
	}
	srv, ok := o.Pool.Acquire(lobbyID)
	if !ok {
		o.Lobbies.SetStatus(lobbyID, lobby.StatusAwaitingServer, "")
		return
	}
	o.launch(lobbyID, srv)
}

// launch loads the lobby's pending match onto srv.
func (o *Orchestrator) launch(lobbyID string, srv pool.Server) {
	ctx := context.Background()
	m, err := o.Store.ActiveMatchOfLobby(ctx, lobbyID)
	if err != nil || m.Status != store.MatchPending {
		o.ReleaseServer(srv.ID)
		return
	}
	password := randHex(4)
	exec := func(cmd string) error {
		c, cancel := context.WithTimeout(ctx, rconTimeout)
		defer cancel()
		_, err := o.RCON.Exec(c, srv, cmd)
		return err
	}
	configURL := fmt.Sprintf("%s/internal/match/%s/config.json?token=%s", o.BaseURL, m.ID, m.Token)
	for _, cmd := range []string{"sv_password " + password, matchzy.LoadCommand(configURL)} {
		if err := exec(cmd); err != nil {
			log.Printf("start match %s on %s: %v", m.ID, srv.ID, err)
			o.Store.SetMatchStatus(ctx, m.ID, store.MatchFailed, o.Now().Unix())
			o.Lobbies.SetStatus(lobbyID, lobby.StatusWaiting, fmt.Sprintf("%s did not respond. Press Ready to try again.", srv.Name))
			o.ReleaseServer(srv.ID)
			return
		}
	}
	if err := o.Store.SetMatchRunning(ctx, m.ID, srv.ID); err != nil {
		log.Printf("mark running: %v", err)
	}
	o.Lobbies.SetLive(lobbyID, fmt.Sprintf("connect %s:%d; password %s", srv.Host, srv.Port, password))
	o.Lobbies.SetStatus(lobbyID, lobby.StatusInMatch, "")
}

// ReleaseServer returns a server to the pool and starts the oldest waiting lobby on it.
func (o *Orchestrator) ReleaseServer(serverID string) {
	granted, ok := o.Pool.Release(serverID)
	if !ok {
		return
	}
	if srv, found := o.Pool.ServerOf(granted); found {
		o.launch(granted, srv)
	}
}

// LobbyGone cleans up after a lobby was deleted because everyone left.
func (o *Orchestrator) LobbyGone(lobbyID string) {
	ctx := context.Background()
	o.Pool.Forget(lobbyID)
	m, err := o.Store.ActiveMatchOfLobby(ctx, lobbyID)
	if err != nil {
		if !errors.Is(err, store.ErrNotFound) {
			log.Printf("lobby gone: %v", err)
		}
		return
	}
	o.endEarly(m, "")
}

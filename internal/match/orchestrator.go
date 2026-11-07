// Package match connects lobbies to CS2 servers: it starts matches, follows
// MatchZy events and records results.
package match

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/fideliusfalcon/fragnight/internal/lobby"
	"github.com/fideliusfalcon/fragnight/internal/matchzy"
	"github.com/fideliusfalcon/fragnight/internal/pool"
	"github.com/fideliusfalcon/fragnight/internal/store"
)

const rconTimeout = 10 * time.Second

// DefaultMaxDuration is how long a running match may last before it is aborted
// automatically. Generous: a long overtime match is about two hours.
const DefaultMaxDuration = 3 * time.Hour

type Deps struct {
	Lobbies       *lobby.Manager
	Pool          *pool.Pool
	RCON          pool.RCON
	Store         *store.Store
	BaseURL       string
	WebhookSecret string
	// Admins are SteamIDs allowed to abort any match.
	Admins []string
	// MaxDuration limits how long a match may run; zero means DefaultMaxDuration.
	MaxDuration time.Duration
	Now         func() time.Time
}

// Orchestrator serializes everything that changes one lobby's match behind a
// per-lobby lock, so that starting, aborting, resetting and expiring a match
// never interleave. Rules that keep this deadlock-free:
//   - at most one lobby lock is held at a time;
//   - ReleaseServer (which may start another lobby) is only called after the lock
//     has been released.
type Orchestrator struct {
	Deps
	locks sync.Map // lobby ID -> *sync.Mutex
}

func New(d Deps) *Orchestrator {
	if d.Now == nil {
		d.Now = time.Now
	}
	return &Orchestrator{Deps: d}
}

// lock takes the lobby's lock and returns the unlock function.
func (o *Orchestrator) lock(lobbyID string) func() {
	v, _ := o.locks.LoadOrStore(lobbyID, &sync.Mutex{})
	mu := v.(*sync.Mutex)
	mu.Lock()
	return mu.Unlock
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
	unlock := o.lock(lobbyID)
	release := o.startLocked(lobbyID, mapName)
	unlock()
	if release != "" {
		o.ReleaseServer(release)
	}
}

// startLocked returns the ID of a server that must be released, or "".
func (o *Orchestrator) startLocked(lobbyID, mapName string) string {
	ctx := context.Background()
	if _, err := o.Store.ActiveMatchOfLobby(ctx, lobbyID); err == nil {
		return "" // already prepared
	}
	snap, ok := o.Lobbies.Get(lobbyID)
	if !ok {
		return ""
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
		return ""
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
		return ""
	}
	srv, ok := o.Pool.Acquire(lobbyID)
	if !ok {
		o.Lobbies.SetStatus(lobbyID, lobby.StatusAwaitingServer, "")
		return ""
	}
	return o.launchLocked(lobbyID, srv)
}

// launch loads the lobby's pending match onto srv (a server the pool just granted).
func (o *Orchestrator) launch(lobbyID string, srv pool.Server) {
	unlock := o.lock(lobbyID)
	release := o.launchLocked(lobbyID, srv)
	unlock()
	if release != "" {
		o.ReleaseServer(release)
	}
}

// launchLocked returns the ID of a server that must be released, or "".
func (o *Orchestrator) launchLocked(lobbyID string, srv pool.Server) string {
	ctx := context.Background()
	// Only give the server back if this lobby still holds it. A stale launch (the
	// lobby was aborted or deleted after the pool granted it a server) must never
	// free a server that now belongs to someone else.
	releaseIfHeld := func() string {
		if cur, held := o.Pool.ServerOf(lobbyID); held && cur.ID == srv.ID {
			return srv.ID
		}
		return ""
	}
	m, err := o.Store.ActiveMatchOfLobby(ctx, lobbyID)
	if err != nil || m.Status != store.MatchPending {
		return releaseIfHeld()
	}
	password := randHex(8)
	configURL := fmt.Sprintf("%s/internal/match/%s/config.json?token=%s", o.BaseURL, m.ID, m.Token)
	steps := []struct {
		cmd      string
		optional bool
	}{
		{"kickall", true}, // players of the previous match must not stay on the server
		{"sv_password " + password, false},
		{matchzy.LoadCommand(configURL), false},
	}
	for i, step := range steps {
		if i > 0 {
			// Defence in depth: nothing else can change this lobby's match while we
			// hold its lock, but never load a match that is no longer pending.
			if cur, err := o.Store.GetMatch(ctx, m.ID); err != nil || cur.Status != store.MatchPending {
				log.Printf("match %s is no longer pending; not loading it on %s", m.ID, srv.ID)
				return releaseIfHeld()
			}
		}
		c, cancel := context.WithTimeout(ctx, rconTimeout)
		_, err := o.RCON.Exec(c, srv, step.cmd)
		cancel()
		if err == nil {
			continue
		}
		if step.optional {
			log.Printf("start match %s on %s: %v (continuing)", m.ID, srv.ID, err)
			continue
		}
		log.Printf("start match %s on %s: %v", m.ID, srv.ID, err)
		o.Store.SetMatchStatus(ctx, m.ID, store.MatchFailed, o.Now().Unix())
		o.Lobbies.SetStatus(lobbyID, lobby.StatusWaiting, fmt.Sprintf("%s did not respond. Press Ready to try again.", srv.Name))
		return releaseIfHeld()
	}
	if err := o.Store.SetMatchRunning(ctx, m.ID, srv.ID, o.Now().Unix()); err != nil {
		log.Printf("match %s no longer pending after load on %s: %v", m.ID, srv.ID, err)
		return releaseIfHeld()
	}
	o.Lobbies.SetMatchID(lobbyID, m.ID)
	o.Lobbies.SetLive(lobbyID, fmt.Sprintf("connect %s:%d; password %s", srv.Host, srv.Port, password))
	o.Lobbies.SetStatus(lobbyID, lobby.StatusInMatch, "")
	return ""
}

// ReleaseServer returns a server to the pool and starts the oldest waiting lobby on it.
// It must not be called while a lobby lock is held.
func (o *Orchestrator) ReleaseServer(serverID string) {
	granted, ok := o.Pool.Release(serverID)
	if !ok {
		return
	}
	if srv, found := o.Pool.ServerOf(granted); found {
		o.launch(granted, srv)
	}
}

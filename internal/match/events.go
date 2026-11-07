package match

import (
	"context"
	"errors"
	"log"
	"slices"

	"github.com/fideliusfalcon/fragnight/internal/elo"
	"github.com/fideliusfalcon/fragnight/internal/lobby"
	"github.com/fideliusfalcon/fragnight/internal/matchzy"
	"github.com/fideliusfalcon/fragnight/internal/pool"
	"github.com/fideliusfalcon/fragnight/internal/store"
)

// HandleEvent processes one MatchZy webhook delivery from a server. Events for
// unknown servers, stale matches and already-finished matches are ignored.
func (o *Orchestrator) HandleEvent(serverID string, ev matchzy.Event) error {
	ctx := context.Background()
	m, err := o.Store.RunningMatchOnServer(ctx, serverID)
	if errors.Is(err, store.ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	if ev.MatchID != "" && ev.MatchID != m.ID {
		return nil
	}
	switch ev.Name {
	case "going_live":
		o.Lobbies.SetScore(m.LobbyID, 0, 0)
	case "round_end":
		t1, t2, err := matchzy.Scores(ev.Raw)
		if err != nil {
			return err
		}
		o.Lobbies.SetScore(m.LobbyID, t1, t2)
	case "map_result":
		return o.finish(ctx, m, ev)
	}
	return nil
}

func (o *Orchestrator) finish(ctx context.Context, m store.Match, ev matchzy.Event) error {
	res, err := matchzy.MapResult(ev.Raw)
	if err != nil {
		return err
	}
	roster, err := o.Store.Roster(ctx, m.ID)
	if err != nil {
		return err
	}
	var team1, team2 []int
	for _, r := range roster {
		if r.Team == 1 {
			team1 = append(team1, r.EloBefore)
		} else {
			team2 = append(team2, r.EloBefore)
		}
	}
	d1, d2 := 0, 0
	if res.Winner != 0 { // no winner (a draw) leaves ratings alone
		d1, d2 = elo.Deltas(elo.Average(team1), elo.Average(team2), res.Winner == 1)
	}
	stats := map[string]matchzy.PlayerStats{}
	for _, p := range res.Players {
		stats[p.SteamID] = p
	}
	in := store.FinishInput{
		MatchID: m.ID, Team1Score: res.Team1Score, Team2Score: res.Team2Score, Winner: res.Winner,
		EndedAt: o.Now().Unix(),
	}
	for _, r := range roster {
		delta := d2
		if r.Team == 1 {
			delta = d1
		}
		s := stats[r.SteamID]
		in.Players = append(in.Players, store.PlayerResult{
			SteamID: r.SteamID, Kills: s.Kills, Deaths: s.Deaths, Assists: s.Assists, Damage: s.Damage,
			Headshots: s.Headshots, Rounds: s.Rounds, EloAfter: r.EloBefore + delta,
		})
	}
	if err := o.Store.FinishMatch(ctx, in); err != nil {
		if errors.Is(err, store.ErrNotRunning) {
			return nil // duplicate delivery
		}
		return err
	}
	o.Lobbies.SetScore(m.LobbyID, res.Team1Score, res.Team2Score)
	o.Lobbies.SetStatus(m.LobbyID, lobby.StatusFinished, "")
	o.resetAndRelease(m.ServerID, false)
	return nil
}

// Abort ends a lobby's pending or running match without stats or rating changes.
// Allowed for the lobby's host and for configured admins.
func (o *Orchestrator) Abort(lobbyID, actor string) error {
	snap, ok := o.Lobbies.Get(lobbyID)
	if !ok {
		return lobby.ErrNotFound
	}
	if snap.HostSteamID != actor && !slices.Contains(o.Admins, actor) {
		return lobby.ErrNotHost
	}
	m, err := o.Store.ActiveMatchOfLobby(context.Background(), lobbyID)
	if errors.Is(err, store.ErrNotFound) {
		return lobby.ErrBadState
	}
	if err != nil {
		return err
	}
	o.Pool.Forget(lobbyID)
	o.endEarly(m, "Match aborted. Press Ready to play again.")
	return nil
}

// endEarly marks the match aborted first (so late events are ignored), stops it
// on the server, frees the server, and, if reason is not empty, puts the lobby
// back to Waiting with that message.
func (o *Orchestrator) endEarly(m store.Match, reason string) {
	ctx := context.Background()
	if err := o.Store.SetMatchStatus(ctx, m.ID, store.MatchAborted, o.Now().Unix()); err != nil {
		log.Printf("abort match %s: %v", m.ID, err)
	}
	if reason != "" {
		o.Lobbies.SetLive(m.LobbyID, "")
		o.Lobbies.SetStatus(m.LobbyID, lobby.StatusWaiting, reason)
	}
	switch {
	case m.ServerID != "":
		o.resetAndRelease(m.ServerID, true)
	default:
		if srv, ok := o.Pool.ServerOf(m.LobbyID); ok {
			o.ReleaseServer(srv.ID)
		}
	}
}

// resetAndRelease clears the match password (and optionally ends the running
// match) on the server, then returns it to the pool.
func (o *Orchestrator) resetAndRelease(serverID string, endMatch bool) {
	if srv, ok := o.serverByID(serverID); ok {
		cmds := []string{`sv_password ""`}
		if endMatch {
			cmds = []string{"get5_endmatch", `sv_password ""`}
		}
		for _, cmd := range cmds {
			ctx, cancel := context.WithTimeout(context.Background(), rconTimeout)
			if _, err := o.RCON.Exec(ctx, srv, cmd); err != nil {
				log.Printf("reset %s (%s): %v", serverID, cmd, err)
			}
			cancel()
		}
	}
	o.ReleaseServer(serverID)
}

func (o *Orchestrator) serverByID(id string) (pool.Server, bool) {
	for _, s := range o.Pool.State() {
		if s.ID == id {
			return s.Server, true
		}
	}
	return pool.Server{}, false
}

// PoolState lists the CS2 servers and who holds them (for the admin page).
func (o *Orchestrator) PoolState() []pool.ServerState { return o.Pool.State() }

// ResetServer is the admin recovery action: it aborts whatever match is running
// on the server and returns the server to the pool, even if nothing is running.
func (o *Orchestrator) ResetServer(serverID string) error {
	if _, ok := o.serverByID(serverID); !ok {
		return errors.New("unknown server")
	}
	m, err := o.Store.RunningMatchOnServer(context.Background(), serverID)
	if err == nil {
		o.endEarly(m, "Match aborted by an admin.")
		return nil
	}
	if !errors.Is(err, store.ErrNotFound) {
		return err
	}
	o.resetAndRelease(serverID, false)
	return nil
}

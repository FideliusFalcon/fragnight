package match

import (
	"context"
	"errors"
	"fmt"
	"log"
	"slices"
	"time"

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
			Headshots: s.Headshots, Rounds: s.Rounds, EloDelta: delta,
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
// Allowed for the lobby's host and for configured admins. If the match is still
// being loaded onto a server, Abort waits for that to finish first.
func (o *Orchestrator) Abort(lobbyID, actor string) error {
	snap, ok := o.Lobbies.Get(lobbyID)
	if !ok {
		return lobby.ErrNotFound
	}
	if snap.HostSteamID != actor && !slices.Contains(o.Admins, actor) {
		return lobby.ErrNotHost
	}
	unlock := o.lock(lobbyID)
	m, err := o.Store.ActiveMatchOfLobby(context.Background(), lobbyID) // re-read under the lock
	if err != nil {
		unlock()
		if errors.Is(err, store.ErrNotFound) {
			return lobby.ErrBadState
		}
		return err
	}
	o.Pool.Forget(lobbyID)
	serverID, endMatch := o.endEarlyLocked(m, "Match aborted. Press Ready to play again.")
	unlock()
	o.resetAndRelease(serverID, endMatch)
	return nil
}

// endEarlyLocked marks the match aborted first (so late events are ignored) and,
// if reason is not empty, puts the lobby back to Waiting with that message. It
// returns the server that must now be reset and released (and whether a running
// match must be ended on it), or "" when there is none. The lobby lock is held.
func (o *Orchestrator) endEarlyLocked(m store.Match, reason string) (serverID string, endMatch bool) {
	if err := o.Store.SetMatchStatus(context.Background(), m.ID, store.MatchAborted, o.Now().Unix()); err != nil {
		log.Printf("abort match %s: %v", m.ID, err)
	}
	if reason != "" {
		o.Lobbies.SetLive(m.LobbyID, "")
		o.Lobbies.SetStatus(m.LobbyID, lobby.StatusWaiting, reason)
	}
	if m.ServerID != "" {
		return m.ServerID, true
	}
	if srv, held := o.Pool.ServerOf(m.LobbyID); held {
		return srv.ID, false
	}
	return "", false
}

// LobbyGone cleans up after a lobby was deleted because everyone left or it was
// deleted by its host or an admin.
func (o *Orchestrator) LobbyGone(lobbyID string) {
	unlock := o.lock(lobbyID)
	o.Pool.Forget(lobbyID)
	m, err := o.Store.ActiveMatchOfLobby(context.Background(), lobbyID)
	if err != nil {
		unlock()
		if !errors.Is(err, store.ErrNotFound) {
			log.Printf("lobby gone: %v", err)
		}
		return
	}
	serverID, endMatch := o.endEarlyLocked(m, "")
	unlock()
	o.resetAndRelease(serverID, endMatch)
	o.locks.Delete(lobbyID)
}

// resetAndRelease makes a server safe for its next user, then returns it to the
// pool. When a running match is being cut short everyone is kicked. The idle
// server is locked with a fresh random password (never left open), and players
// who stay after a normal finish keep their connection until the next match.
// It must not be called while a lobby lock is held.
func (o *Orchestrator) resetAndRelease(serverID string, endMatch bool) {
	if serverID == "" {
		return
	}
	if srv, ok := o.serverByID(serverID); ok {
		cmds := []string{"sv_password " + randHex(8)}
		if endMatch {
			cmds = []string{"get5_endmatch", "kickall", cmds[0]}
		}
		for _, cmd := range cmds {
			ctx, cancel := context.WithTimeout(context.Background(), rconTimeout)
			if _, err := o.RCON.Exec(ctx, srv, cmd); err != nil {
				log.Printf("reset %s: %v", serverID, err)
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
	holder, known := "", false
	for _, st := range o.Pool.State() {
		if st.ID == serverID {
			holder, known = st.LobbyID, true
		}
	}
	if !known {
		return errors.New("unknown server")
	}
	unlock := func() {}
	if holder != "" {
		unlock = o.lock(holder) // waits for a start in progress on that server
	}
	releaseID, endMatch := serverID, false
	m, err := o.Store.RunningMatchOnServer(context.Background(), serverID)
	switch {
	case err == nil:
		releaseID, endMatch = o.endEarlyLocked(m, "Match aborted by an admin.")
	case !errors.Is(err, store.ErrNotFound):
		unlock()
		return err
	}
	unlock()
	o.resetAndRelease(releaseID, endMatch)
	return nil
}

// ExpireStale aborts matches that have been running longer than MaxDuration, so a
// match that never reports a result (or a host who never finishes) cannot hold a
// server forever. Call it periodically.
func (o *Orchestrator) ExpireStale() {
	limit := o.MaxDuration
	if limit <= 0 {
		limit = DefaultMaxDuration
	}
	running, err := o.Store.RunningMatches(context.Background())
	if err != nil {
		log.Printf("expire stale: %v", err)
		return
	}
	for _, m := range running {
		since := m.RunningAt
		if since == 0 {
			since = m.StartedAt
		}
		if o.Now().Unix()-since < int64(limit.Seconds()) {
			continue
		}
		unlock := o.lock(m.LobbyID)
		cur, err := o.Store.GetMatch(context.Background(), m.ID) // re-read under the lock
		if err != nil || cur.Status != store.MatchRunning {
			unlock()
			continue
		}
		serverID, endMatch := o.endEarlyLocked(cur, fmt.Sprintf("The match timed out after %s. Press Ready to play again.", limit.Round(time.Minute)))
		unlock()
		log.Printf("match %s on %s timed out after %s", m.ID, m.ServerID, limit)
		o.resetAndRelease(serverID, endMatch)
	}
}

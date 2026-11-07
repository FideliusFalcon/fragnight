package lobby

import (
	"slices"
	"time"
)

// VoteDuration is how long the map vote runs.
const VoteDuration = 30 * time.Second

// Ready starts the map vote countdown. Host only; teams must be equal and non-empty.
func (m *Manager) Ready(id, actor string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	s, ok := m.lobbies[id]
	if !ok {
		return ErrNotFound
	}
	if s.host != actor {
		return ErrNotHost
	}
	if s.status != StatusWaiting {
		return ErrBadState
	}
	c1, c2 := s.teamCount(Team1), s.teamCount(Team2)
	if c1 < 1 || c1 != c2 {
		return ErrBadTeams
	}
	s.status = StatusVoting
	s.errMsg = "" // a message about the previous attempt must not outlive it
	s.deadline = m.now().Add(VoteDuration)
	m.publish(s)
	return nil
}

// StopVote cancels the countdown; votes are kept.
func (m *Manager) StopVote(id, actor string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	s, ok := m.lobbies[id]
	if !ok {
		return ErrNotFound
	}
	if s.host != actor {
		return ErrNotHost
	}
	if s.status != StatusVoting {
		return ErrBadState
	}
	s.status = StatusWaiting
	s.deadline = time.Time{}
	m.publish(s)
	return nil
}

// CastVote records (or replaces) a player's vote.
func (m *Manager) CastVote(id, steamID, mapName string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	s, ok := m.lobbies[id]
	if !ok {
		return ErrNotFound
	}
	i := s.memberIdx(steamID)
	if i < 0 {
		return ErrNotFound
	}
	if s.status != StatusVoting || !m.now().Before(s.deadline) {
		return ErrBadState
	}
	if s.members[i].Team == TeamNone {
		return ErrNotVoter
	}
	if !slices.Contains(s.settings.Maps, mapName) {
		return ErrBadMap
	}
	s.members[i].Vote = mapName
	m.publish(s)
	return nil
}

// Tick finishes every vote whose countdown has run out. Call it about once a
// second.
func (m *Manager) Tick() {
	type pick struct{ id, mapName string }
	var picks []pick

	m.mu.Lock()
	now := m.now()
	for _, s := range m.lobbies {
		if s.status != StatusVoting || now.Before(s.deadline) {
			continue
		}
		s.mapName = m.winnerLocked(s)
		s.status = StatusAwaitingServer
		s.deadline = time.Time{}
		m.publish(s)
		picks = append(picks, pick{s.id, s.mapName})
	}
	afters := m.expireLocked(now)
	cb := m.OnMapChosen
	m.mu.Unlock()

	for _, after := range afters {
		after()
	}
	if cb != nil {
		for _, p := range picks {
			cb(p.id, p.mapName)
		}
	}
}

// winnerLocked returns the map with the most votes; ties (and no votes at all)
// are broken randomly among the tied maps.
func (m *Manager) winnerLocked(s *state) string {
	tally := s.tally()
	best := -1
	var tied []string
	for _, name := range s.settings.Maps {
		switch n := tally[name]; {
		case n > best:
			best, tied = n, []string{name}
		case n == best:
			tied = append(tied, name)
		}
	}
	return tied[m.rnd.Intn(len(tied))]
}

// SetLive records the connect string once the match server is ready.
func (m *Manager) SetLive(id, connect string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if s, ok := m.lobbies[id]; ok {
		s.connect = connect
		m.publish(s)
	}
}

// SetScore updates the live score shown in the lobby and browser.
func (m *Manager) SetScore(id string, s1, s2 int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if s, ok := m.lobbies[id]; ok {
		s.score = [2]int{s1, s2}
		m.publish(s)
	}
}

// SetMatchID records which match the lobby is playing, so the page can link to its scoreboard.
func (m *Manager) SetMatchID(id, matchID string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if s, ok := m.lobbies[id]; ok {
		s.matchID = matchID
		m.publish(s)
	}
}

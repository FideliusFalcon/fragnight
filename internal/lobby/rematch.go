package lobby

import "time"

// Rematch reuses a finished lobby: it goes back to waiting with the same members,
// teams and settings, and everything about the previous match is cleared so the
// players can change teams, vote and start again. Host only.
func (m *Manager) Rematch(id, actor string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	s, ok := m.lobbies[id]
	if !ok {
		return ErrNotFound
	}
	if s.host != actor {
		return ErrNotHost
	}
	if s.status != StatusFinished {
		return ErrBadState
	}
	s.status = StatusWaiting
	s.deadline = time.Time{}
	s.mapName, s.connect, s.errMsg, s.matchID = "", "", "", ""
	s.score = [2]int{}
	for i := range s.members {
		s.members[i].Vote = ""
	}
	m.publish(s)
	return nil
}

// SwapTeams exchanges team 1 and team 2 (players not on a team stay as they are).
// Host only, while waiting.
func (m *Manager) SwapTeams(id, actor string) error {
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
	for i := range s.members {
		switch s.members[i].Team {
		case Team1:
			s.members[i].Team = Team2
		case Team2:
			s.members[i].Team = Team1
		}
		s.members[i].Vote = ""
	}
	m.publish(s)
	return nil
}

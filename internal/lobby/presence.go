package lobby

import "time"

// PresenceGrace is how long a member may be offline (no open lobby page) before
// being removed from a lobby that is still forming or finished. Lobbies whose
// match is starting or running never expire, because players leave the lobby
// page to play.
const PresenceGrace = 90 * time.Second

// Connected records an open lobby page (WebSocket) for the member.
func (m *Manager) Connected(id, steamID string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	s, ok := m.lobbies[id]
	if !ok || s.memberIdx(steamID) < 0 {
		return
	}
	s.conns[steamID]++
	delete(s.offline, steamID)
}

// Disconnected records a closed lobby page. When the member's last page closes
// the grace period starts.
func (m *Manager) Disconnected(id, steamID string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	s, ok := m.lobbies[id]
	if !ok || s.memberIdx(steamID) < 0 {
		return
	}
	if s.conns[steamID] > 0 {
		s.conns[steamID]--
	}
	if s.conns[steamID] == 0 {
		s.offline[steamID] = m.now()
	}
}

// IsOnline reports whether the member has at least one open lobby page.
func (m *Manager) IsOnline(id, steamID string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	s, ok := m.lobbies[id]
	return ok && s.conns[steamID] > 0
}

// expireLocked removes members who have been offline longer than PresenceGrace
// from lobbies that are waiting, voting or finished. It returns callbacks to run
// after the lock is released.
func (m *Manager) expireLocked(now time.Time) []func() {
	var afters []func()
	for id, s := range m.lobbies {
		if s.status != StatusWaiting && s.status != StatusVoting && s.status != StatusFinished {
			continue
		}
		var stale []string
		for _, mem := range s.members {
			if since, off := s.offline[mem.SteamID]; off && now.Sub(since) >= PresenceGrace {
				stale = append(stale, mem.SteamID)
			}
		}
		for _, steamID := range stale {
			if _, after := m.removeLocked(id, steamID); after != nil {
				afters = append(afters, after)
			}
		}
	}
	return afters
}

package pool

import (
	"slices"
	"sync"
)

// ServerState is a server and who holds it.
type ServerState struct {
	Server
	Busy    bool
	LobbyID string
}

// Pool hands CS2 servers to lobbies, with a FIFO queue when all are busy.
type Pool struct {
	mu      sync.Mutex
	servers []Server
	holder  map[string]string // serverID -> lobbyID
	queue   []string          // waiting lobby IDs, oldest first
}

func New(servers []Server) *Pool {
	return &Pool{servers: slices.Clone(servers), holder: map[string]string{}}
}

func (p *Pool) findByLobby(lobbyID string) (Server, bool) {
	for _, s := range p.servers {
		if p.holder[s.ID] == lobbyID {
			return s, true
		}
	}
	return Server{}, false
}

// Acquire gives the lobby a free server. If none is free the lobby is queued
// (once) and false is returned. A lobby that already holds a server gets it back.
func (p *Pool) Acquire(lobbyID string) (Server, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if s, ok := p.findByLobby(lobbyID); ok {
		return s, true
	}
	for _, s := range p.servers {
		if _, busy := p.holder[s.ID]; !busy {
			p.holder[s.ID] = lobbyID
			p.queue = slices.DeleteFunc(p.queue, func(id string) bool { return id == lobbyID })
			return s, true
		}
	}
	if !slices.Contains(p.queue, lobbyID) {
		p.queue = append(p.queue, lobbyID)
	}
	return Server{}, false
}

// Release frees a server. If a lobby is waiting it is handed the server and its
// ID is returned.
func (p *Pool) Release(serverID string) (grantedLobbyID string, ok bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if !slices.ContainsFunc(p.servers, func(s Server) bool { return s.ID == serverID }) {
		return "", false
	}
	delete(p.holder, serverID)
	if len(p.queue) == 0 {
		return "", false
	}
	next := p.queue[0]
	p.queue = p.queue[1:]
	p.holder[serverID] = next
	return next, true
}

// Forget removes a lobby from the wait queue.
func (p *Pool) Forget(lobbyID string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.queue = slices.DeleteFunc(p.queue, func(id string) bool { return id == lobbyID })
}

func (p *Pool) ServerOf(lobbyID string) (Server, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.findByLobby(lobbyID)
}

func (p *Pool) QueueLen() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.queue)
}

func (p *Pool) State() []ServerState {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make([]ServerState, 0, len(p.servers))
	for _, s := range p.servers {
		id, busy := p.holder[s.ID]
		out = append(out, ServerState{Server: s, Busy: busy, LobbyID: id})
	}
	return out
}

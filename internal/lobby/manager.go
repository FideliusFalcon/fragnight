package lobby

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	mrand "math/rand"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/fideliusfalcon/fragnight/internal/settings"
)

type state struct {
	id, name string
	host     string
	public   bool
	status   Status
	members  []Member // join order
	settings settings.Match
	deadline time.Time
	mapName  string
	connect  string
	errMsg   string
	score    [2]int
	subs     map[int]chan Snapshot
}

// Manager owns all lobbies. All methods are safe for concurrent use.
type Manager struct {
	mu       sync.Mutex
	now      func() time.Time
	rnd      *mrand.Rand
	lobbies  map[string]*state
	byPlayer map[string]string
	listSubs map[int]chan []Summary
	nextSub  int

	// OnEmpty is called (without locks held) when the last member leaves.
	OnEmpty func(lobbyID string)
	// OnMapChosen is called (without locks held) when a vote finishes.
	OnMapChosen func(lobbyID, mapName string)
}

func NewManager(now func() time.Time, rnd *mrand.Rand) *Manager {
	return &Manager{
		now: now, rnd: rnd,
		lobbies:  map[string]*state{},
		byPlayer: map[string]string{},
		listSubs: map[int]chan []Summary{},
	}
}

func newID() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b)
}

// send delivers v, dropping the oldest queued value if the subscriber is slow.
func send[T any](ch chan T, v T) {
	for {
		select {
		case ch <- v:
			return
		default:
		}
		select {
		case <-ch:
		default:
		}
	}
}

func (s *state) memberIdx(steamID string) int {
	return slices.IndexFunc(s.members, func(m Member) bool { return m.SteamID == steamID })
}

func (s *state) teamCount(t Team) int {
	n := 0
	for _, m := range s.members {
		if m.Team == t {
			n++
		}
	}
	return n
}

func (s *state) tally() map[string]int {
	t := map[string]int{}
	for _, name := range s.settings.Maps {
		t[name] = 0
	}
	for _, m := range s.members {
		if m.Team != TeamNone && m.Vote != "" {
			t[m.Vote]++
		}
	}
	return t
}

func (m *Manager) snapshot(s *state) Snapshot {
	snap := Snapshot{
		ID: s.id, Name: s.name, HostSteamID: s.host, Public: s.public, Status: s.status,
		Members: slices.Clone(s.members), Settings: s.settings, Tally: s.tally(),
		Map: s.mapName, Connect: s.connect, Score: s.score, Error: s.errMsg,
	}
	snap.Settings.Maps = slices.Clone(s.settings.Maps)
	if s.status == StatusVoting {
		if d := s.deadline.Sub(m.now()); d > 0 {
			snap.VoteRemainingMS = d.Milliseconds()
		}
	}
	return snap
}

func (m *Manager) summary(s *state) Summary {
	host := s.host
	if i := s.memberIdx(s.host); i >= 0 {
		host = s.members[i].Name
	}
	info := fmt.Sprintf("%d maps", len(s.settings.Maps))
	switch {
	case s.mapName != "":
		info = s.mapName
	case len(s.settings.Maps) == 1:
		info = s.settings.Maps[0]
	}
	sum := Summary{
		ID: s.id, Name: s.name, HostName: host, Players: len(s.members), Capacity: Capacity,
		MapInfo: info, Status: s.status, Score: s.score,
	}
	if s.status == StatusVoting {
		if d := s.deadline.Sub(m.now()); d > 0 {
			sum.VoteRemainingMS = d.Milliseconds()
		}
	}
	return sum
}

// publish pushes the lobby's snapshot and the public list to subscribers.
// Must be called with m.mu held.
func (m *Manager) publish(s *state) {
	if s != nil {
		snap := m.snapshot(s)
		for _, ch := range s.subs {
			send(ch, snap)
		}
	}
	if len(m.listSubs) > 0 {
		list := m.listPublicLocked()
		for _, ch := range m.listSubs {
			send(ch, list)
		}
	}
}

func (m *Manager) listPublicLocked() []Summary {
	out := []Summary{}
	for _, s := range m.lobbies {
		if s.public {
			out = append(out, m.summary(s))
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

func (m *Manager) Create(host Player, name string, public bool) (Snapshot, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, in := m.byPlayer[host.SteamID]; in {
		return Snapshot{}, ErrAlreadyInLobby
	}
	name = strings.TrimSpace(name)
	if r := []rune(name); len(r) > 40 {
		name = string(r[:40])
	}
	if name == "" {
		name = host.Name + "'s lobby"
	}
	s := &state{
		id: newID(), name: name, host: host.SteamID, public: public,
		status: StatusWaiting, settings: settings.Defaults(),
		members: []Member{{Player: host}}, subs: map[int]chan Snapshot{},
	}
	m.lobbies[s.id] = s
	m.byPlayer[host.SteamID] = s.id
	m.publish(s)
	return m.snapshot(s), nil
}

func (m *Manager) Join(id string, p Player) (Snapshot, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	s, ok := m.lobbies[id]
	if !ok {
		return Snapshot{}, ErrNotFound
	}
	if cur, in := m.byPlayer[p.SteamID]; in {
		if cur == id {
			return m.snapshot(s), nil
		}
		return Snapshot{}, ErrAlreadyInLobby
	}
	if s.status != StatusWaiting && s.status != StatusVoting {
		return Snapshot{}, ErrBadState
	}
	s.members = append(s.members, Member{Player: p})
	m.byPlayer[p.SteamID] = id
	m.publish(s)
	return m.snapshot(s), nil
}

func (m *Manager) SetTeam(id, steamID string, t Team) error {
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
	if t < TeamNone || t > Team2 {
		return ErrBadTeam
	}
	if s.status != StatusWaiting {
		return ErrBadState
	}
	if s.members[i].Team == t {
		return nil
	}
	if t != TeamNone && s.teamCount(t) >= TeamSize {
		return ErrTeamFull
	}
	s.members[i].Team = t
	s.members[i].Vote = ""
	m.publish(s)
	return nil
}

func (m *Manager) Leave(id, steamID string) error {
	m.mu.Lock()
	err, after := m.removeLocked(id, steamID)
	m.mu.Unlock()
	if after != nil {
		after()
	}
	return err
}

func (m *Manager) Kick(id, actor, target string) error {
	m.mu.Lock()
	s, ok := m.lobbies[id]
	if !ok {
		m.mu.Unlock()
		return ErrNotFound
	}
	if s.host != actor {
		m.mu.Unlock()
		return ErrNotHost
	}
	if target == actor || (s.status != StatusWaiting && s.status != StatusVoting) {
		m.mu.Unlock()
		return ErrBadState
	}
	err, after := m.removeLocked(id, target)
	m.mu.Unlock()
	if after != nil {
		after()
	}
	return err
}

// removeLocked removes a member, handing over host or deleting an empty lobby.
// The returned func (if any) must be called after releasing the lock.
func (m *Manager) removeLocked(id, steamID string) (error, func()) {
	s, ok := m.lobbies[id]
	if !ok {
		return ErrNotFound, nil
	}
	i := s.memberIdx(steamID)
	if i < 0 {
		return ErrNotFound, nil
	}
	s.members = slices.Delete(s.members, i, i+1)
	delete(m.byPlayer, steamID)
	if len(s.members) == 0 {
		delete(m.lobbies, id)
		for _, ch := range s.subs {
			close(ch)
		}
		s.subs = nil
		m.publish(nil)
		cb := m.OnEmpty
		if cb == nil {
			return nil, nil
		}
		return nil, func() { cb(id) }
	}
	if s.host == steamID {
		s.host = s.members[0].SteamID
	}
	m.publish(s)
	return nil, nil
}

func (m *Manager) SetSettings(id, actor string, ns settings.Match) error {
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
	if err := ns.Validate(); err != nil {
		return err
	}
	ns.Maps = slices.Clone(ns.Maps)
	s.settings = ns
	for i := range s.members {
		s.members[i].Vote = ""
	}
	m.publish(s)
	return nil
}

// SetStatus lets the match orchestrator move a lobby between statuses.
func (m *Manager) SetStatus(id string, st Status, errMsg string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if s, ok := m.lobbies[id]; ok {
		s.status = st
		s.errMsg = errMsg
		if st != StatusVoting {
			s.deadline = time.Time{}
		}
		m.publish(s)
	}
}

func (m *Manager) Get(id string) (Snapshot, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	s, ok := m.lobbies[id]
	if !ok {
		return Snapshot{}, false
	}
	return m.snapshot(s), true
}

func (m *Manager) ListPublic() []Summary {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.listPublicLocked()
}

func (m *Manager) LobbyOf(steamID string) (string, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	id, ok := m.byPlayer[steamID]
	return id, ok
}

// Subscribe returns a channel that receives the current snapshot immediately and
// again after every change. The channel is closed if the lobby is deleted.
func (m *Manager) Subscribe(id string) (<-chan Snapshot, func()) {
	m.mu.Lock()
	defer m.mu.Unlock()
	s, ok := m.lobbies[id]
	if !ok {
		ch := make(chan Snapshot)
		close(ch)
		return ch, func() {}
	}
	m.nextSub++
	key := m.nextSub
	ch := make(chan Snapshot, 4)
	s.subs[key] = ch
	ch <- m.snapshot(s)
	return ch, func() {
		m.mu.Lock()
		defer m.mu.Unlock()
		if cur, ok := m.lobbies[id]; ok && cur.subs != nil {
			delete(cur.subs, key)
		}
	}
}

func (m *Manager) SubscribeList() (<-chan []Summary, func()) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.nextSub++
	key := m.nextSub
	ch := make(chan []Summary, 4)
	m.listSubs[key] = ch
	ch <- m.listPublicLocked()
	return ch, func() {
		m.mu.Lock()
		defer m.mu.Unlock()
		delete(m.listSubs, key)
	}
}

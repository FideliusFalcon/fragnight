package lobby

import (
	"math/rand"
	"testing"
	"time"
)

func presenceMgr() (*Manager, *fakeClock, *[]string) {
	clk := &fakeClock{t: time.Unix(1_700_000_000, 0)}
	m := NewManager(clk.Now, rand.New(rand.NewSource(3)))
	var gone []string
	m.OnEmpty = func(id string) { gone = append(gone, id) }
	return m, clk, &gone
}

func TestPresence_NeverConnectedMemberExpires(t *testing.T) {
	m, clk, gone := presenceMgr()
	s := mustCreate(t, m, pl(1), true)
	clk.Advance(PresenceGrace - time.Second)
	m.Tick()
	if _, ok := m.Get(s.ID); !ok {
		t.Fatal("expired before the grace period")
	}
	clk.Advance(time.Second)
	m.Tick()
	if _, ok := m.Get(s.ID); ok {
		t.Fatal("lobby of a never-connected host still listed")
	}
	if len(*gone) != 1 || (*gone)[0] != s.ID {
		t.Fatalf("OnEmpty calls: %v", *gone)
	}
	if _, in := m.LobbyOf(pl(1).SteamID); in {
		t.Fatal("player still mapped to the deleted lobby")
	}
}

func TestPresence_ConnectedMemberStays(t *testing.T) {
	m, clk, _ := presenceMgr()
	s := mustCreate(t, m, pl(1), true)
	m.Connected(s.ID, pl(1).SteamID)
	clk.Advance(10 * time.Minute)
	m.Tick()
	if _, ok := m.Get(s.ID); !ok {
		t.Fatal("connected member was expired")
	}
	if !m.IsOnline(s.ID, pl(1).SteamID) {
		t.Fatal("IsOnline false")
	}
}

func TestPresence_DisconnectThenExpire(t *testing.T) {
	m, clk, _ := presenceMgr()
	s := mustCreate(t, m, pl(1), true)
	m.Connected(s.ID, pl(1).SteamID)
	clk.Advance(5 * time.Minute)
	m.Disconnected(s.ID, pl(1).SteamID)
	if m.IsOnline(s.ID, pl(1).SteamID) {
		t.Fatal("still online")
	}
	clk.Advance(PresenceGrace - time.Second)
	m.Tick()
	if _, ok := m.Get(s.ID); !ok {
		t.Fatal("expired too early")
	}
	clk.Advance(time.Second)
	m.Tick()
	if _, ok := m.Get(s.ID); ok {
		t.Fatal("abandoned lobby survived")
	}
}

func TestPresence_ReconnectWithinGraceKeepsMember(t *testing.T) {
	m, clk, _ := presenceMgr()
	s := mustCreate(t, m, pl(1), true)
	m.Connected(s.ID, pl(1).SteamID)
	m.Disconnected(s.ID, pl(1).SteamID)
	clk.Advance(60 * time.Second)
	m.Connected(s.ID, pl(1).SteamID)
	clk.Advance(10 * time.Minute)
	m.Tick()
	if _, ok := m.Get(s.ID); !ok {
		t.Fatal("reconnected member was expired")
	}
}

func TestPresence_TwoTabsNeedBothClosed(t *testing.T) {
	m, clk, _ := presenceMgr()
	s := mustCreate(t, m, pl(1), true)
	m.Connected(s.ID, pl(1).SteamID)
	m.Connected(s.ID, pl(1).SteamID)
	m.Disconnected(s.ID, pl(1).SteamID)
	clk.Advance(10 * time.Minute)
	m.Tick()
	if _, ok := m.Get(s.ID); !ok {
		t.Fatal("expired while one tab was still open")
	}
	m.Disconnected(s.ID, pl(1).SteamID)
	m.Disconnected(s.ID, pl(1).SteamID) // extra calls must not go negative
	clk.Advance(PresenceGrace)
	m.Tick()
	if _, ok := m.Get(s.ID); ok {
		t.Fatal("not expired after all tabs closed")
	}
}

func TestPresence_GhostHostHandsOver(t *testing.T) {
	m, clk, _ := presenceMgr()
	s := mustCreate(t, m, pl(1), true) // host never opens the page
	m.Join(s.ID, pl(2))
	m.Connected(s.ID, pl(2).SteamID)
	clk.Advance(PresenceGrace)
	m.Tick()
	got, ok := m.Get(s.ID)
	if !ok || got.HostSteamID != pl(2).SteamID || len(got.Members) != 1 {
		t.Fatalf("host %q members %d ok %v", got.HostSteamID, len(got.Members), ok)
	}
	if len(m.ListPublic()) != 1 {
		t.Fatal("lobby with a live member should stay listed")
	}
}

func TestPresence_OfflineMemberRemovedWhileWaiting(t *testing.T) {
	m, clk, _ := presenceMgr()
	s := mustCreate(t, m, pl(1), true)
	m.Join(s.ID, pl(2))
	m.SetTeam(s.ID, pl(2).SteamID, Team2)
	m.Connected(s.ID, pl(1).SteamID) // p2 never opens the page
	clk.Advance(PresenceGrace)
	m.Tick()
	got, ok := m.Get(s.ID)
	if !ok || len(got.Members) != 1 || got.Members[0].SteamID != pl(1).SteamID {
		t.Fatalf("offline member not removed: %+v", got.Members)
	}
	if _, in := m.LobbyOf(pl(2).SteamID); in {
		t.Fatal("removed player is still stuck in the lobby")
	}
}

func TestPresence_MatchInProgressNeverExpires(t *testing.T) {
	m, clk, _ := presenceMgr()
	s := mustCreate(t, m, pl(1), true)
	m.SetStatus(s.ID, StatusAwaitingServer, "")
	clk.Advance(time.Hour)
	m.Tick()
	if _, ok := m.Get(s.ID); !ok {
		t.Fatal("lobby waiting for a server was expired")
	}
	m.SetStatus(s.ID, StatusInMatch, "")
	clk.Advance(3 * time.Hour) // everyone is at the CS2 server with the lobby tab closed
	m.Tick()
	if _, ok := m.Get(s.ID); !ok {
		t.Fatal("lobby in a running match was expired")
	}
}

func TestPresence_FinishedLobbyGetsFreshGraceThenExpires(t *testing.T) {
	m, clk, _ := presenceMgr()
	s := mustCreate(t, m, pl(1), true)
	m.SetStatus(s.ID, StatusInMatch, "")
	clk.Advance(2 * time.Hour)
	m.SetStatus(s.ID, StatusFinished, "")
	clk.Advance(PresenceGrace - time.Second)
	m.Tick()
	if _, ok := m.Get(s.ID); !ok {
		t.Fatal("finished lobby vanished before players could open the scoreboard")
	}
	clk.Advance(time.Second)
	m.Tick()
	if _, ok := m.Get(s.ID); ok {
		t.Fatal("finished, abandoned lobby never expired")
	}
}

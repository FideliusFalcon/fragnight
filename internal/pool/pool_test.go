package pool

import "testing"

func servers(n int) []Server {
	var out []Server
	for i := 1; i <= n; i++ {
		out = append(out, Server{ID: "s" + string(rune('0'+i)), Name: "Server", Host: "10.0.0.5", Port: 27014 + i})
	}
	return out
}

func TestAcquire_TakesFreeServer(t *testing.T) {
	p := New(servers(2))
	a, ok := p.Acquire("lobbyA")
	if !ok {
		t.Fatal("no server")
	}
	b, ok := p.Acquire("lobbyB")
	if !ok || a.ID == b.ID {
		t.Fatalf("got %v %v", a, b)
	}
	// asking again returns the one the lobby already holds
	again, ok := p.Acquire("lobbyA")
	if !ok || again.ID != a.ID {
		t.Fatalf("re-acquire: %v %v", again, ok)
	}
}

func TestAcquire_QueuesWhenFull(t *testing.T) {
	p := New(servers(1))
	p.Acquire("A")
	if _, ok := p.Acquire("B"); ok {
		t.Fatal("got a server while full")
	}
	st := p.State()
	if len(st) != 1 || !st[0].Busy || st[0].LobbyID != "A" {
		t.Fatalf("state %+v", st)
	}
	if p.QueueLen() != 1 {
		t.Fatalf("queue %d", p.QueueLen())
	}
	p.Acquire("B") // asking twice must not queue twice
	if p.QueueLen() != 1 {
		t.Fatalf("queue %d after repeat", p.QueueLen())
	}
}

func TestRelease_GrantsToOldestWaiting(t *testing.T) {
	p := New(servers(1))
	s, _ := p.Acquire("A")
	p.Acquire("B")
	p.Acquire("C")
	granted, ok := p.Release(s.ID)
	if !ok || granted != "B" {
		t.Fatalf("granted %q %v", granted, ok)
	}
	if got, ok := p.ServerOf("B"); !ok || got.ID != s.ID {
		t.Fatal("B does not hold the server")
	}
	granted, ok = p.Release(s.ID)
	if !ok || granted != "C" {
		t.Fatalf("second grant %q %v", granted, ok)
	}
	if granted, ok = p.Release(s.ID); ok {
		t.Fatalf("granted %q from empty queue", granted)
	}
	if st := p.State(); st[0].Busy {
		t.Fatal("server should be free")
	}
}

func TestForget_RemovesFromQueue(t *testing.T) {
	p := New(servers(1))
	s, _ := p.Acquire("A")
	p.Acquire("B")
	p.Forget("B")
	if granted, ok := p.Release(s.ID); ok {
		t.Fatalf("granted %q to a forgotten lobby", granted)
	}
}

func TestRelease_UnknownServerIsHarmless(t *testing.T) {
	p := New(servers(1))
	if _, ok := p.Release("nope"); ok {
		t.Fatal("granted from nothing")
	}
}

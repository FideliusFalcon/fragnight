package web

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"

	"github.com/fideliusfalcon/fragnight/internal/lobby"
	"github.com/fideliusfalcon/fragnight/internal/settings"
)

func (s *Server) handleBrowserWS(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.me(r); !ok {
		http.Error(w, "login required", http.StatusUnauthorized)
		return
	}
	c, err := websocket.Accept(w, r, nil)
	if err != nil {
		return
	}
	defer c.CloseNow()
	ctx := c.CloseRead(r.Context())
	ch, cancel := s.Lobbies.SubscribeList()
	defer cancel()
	for {
		select {
		case <-ctx.Done():
			return
		case list := <-ch:
			if err := writeJSON(ctx, c, map[string]any{"type": "list", "lobbies": list}); err != nil {
				return
			}
		}
	}
}

func writeJSON(ctx context.Context, c *websocket.Conn, v any) error {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	return wsjson.Write(ctx, c, v)
}

// clientMsg is a command sent by the lobby page.
type clientMsg struct {
	Type     string          `json:"type"`
	Team     int             `json:"team"`
	Map      string          `json:"map"`
	SteamID  string          `json:"steamid"`
	Settings *settings.Match `json:"settings"`
}

func (s *Server) handleLobbyWS(w http.ResponseWriter, r *http.Request) {
	me, ok := s.me(r)
	if !ok {
		http.Error(w, "login required", http.StatusUnauthorized)
		return
	}
	id := r.PathValue("id")
	if cur, in := s.Lobbies.LobbyOf(me.SteamID); !in || cur != id {
		http.Error(w, "not in this lobby", http.StatusForbidden)
		return
	}
	c, err := websocket.Accept(w, r, nil)
	if err != nil {
		return
	}
	defer c.CloseNow()
	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()

	// An open lobby page keeps the member from being expired as a ghost.
	s.Lobbies.Connected(id, me.SteamID)
	defer s.Lobbies.Disconnected(id, me.SteamID)

	out := make(chan any, 8) // errors and notices from the reader
	snaps, unsub := s.Lobbies.Subscribe(id)
	defer unsub()

	// Reader: handle commands from the client.
	go func() {
		defer cancel()
		for {
			var m clientMsg
			if err := wsjson.Read(ctx, c, &m); err != nil {
				return
			}
			if err := s.handleCommand(id, me, m); err != nil {
				select {
				case out <- map[string]any{"type": "error", "message": friendly(err)}:
				case <-ctx.Done():
					return
				}
			}
			if m.Type == "leave" {
				select {
				case out <- map[string]any{"type": "left"}:
				case <-ctx.Done():
				}
				return
			}
		}
	}()

	for {
		select {
		case <-ctx.Done():
			c.Close(websocket.StatusNormalClosure, "")
			return
		case snap, ok := <-snaps:
			if !ok {
				writeJSON(ctx, c, map[string]any{"type": "closed"})
				c.Close(websocket.StatusNormalClosure, "lobby closed")
				return
			}
			if err := writeJSON(ctx, c, map[string]any{"type": "state", "lobby": snap, "you": me.SteamID}); err != nil {
				return
			}
		case msg := <-out:
			if err := writeJSON(ctx, c, msg); err != nil {
				return
			}
		}
	}
}

func (s *Server) handleCommand(id string, me lobby.Player, m clientMsg) error {
	switch m.Type {
	case "team":
		return s.Lobbies.SetTeam(id, me.SteamID, lobby.Team(m.Team))
	case "ready":
		return s.Lobbies.Ready(id, me.SteamID)
	case "stop":
		return s.Lobbies.StopVote(id, me.SteamID)
	case "vote":
		return s.Lobbies.CastVote(id, me.SteamID, m.Map)
	case "kick":
		return s.Lobbies.Kick(id, me.SteamID, m.SteamID)
	case "settings":
		if m.Settings == nil {
			return errors.New("missing settings")
		}
		return s.Lobbies.SetSettings(id, me.SteamID, *m.Settings)
	case "abort":
		if s.Matches == nil {
			return errors.New("matches are not available")
		}
		return s.Matches.Abort(id, me.SteamID)
	case "leave":
		return s.Lobbies.Leave(id, me.SteamID)
	}
	return errors.New("unknown command")
}

func friendly(err error) string {
	switch {
	case errors.Is(err, lobby.ErrTeamFull):
		return "That team is full."
	case errors.Is(err, lobby.ErrNotHost):
		return "Only the host can do that."
	case errors.Is(err, lobby.ErrBadTeams):
		return "Both teams need the same number of players (at least one each)."
	case errors.Is(err, lobby.ErrBadState):
		return "Not possible right now."
	case errors.Is(err, lobby.ErrNotVoter):
		return "Join a team to vote."
	}
	return err.Error()
}

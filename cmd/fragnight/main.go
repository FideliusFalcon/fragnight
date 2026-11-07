package main

import (
	"context"
	"errors"
	"io"
	"log"
	"math/rand"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/fideliusfalcon/fragnight/internal/auth"
	"github.com/fideliusfalcon/fragnight/internal/config"
	"github.com/fideliusfalcon/fragnight/internal/lobby"
	"github.com/fideliusfalcon/fragnight/internal/match"
	"github.com/fideliusfalcon/fragnight/internal/pool"
	"github.com/fideliusfalcon/fragnight/internal/store"
	"github.com/fideliusfalcon/fragnight/internal/web"
)

func main() {
	var servers io.Reader
	if path := os.Getenv("SERVERS_FILE"); path != "" {
		f, err := os.Open(path)
		if err != nil {
			log.Fatal(err)
		}
		defer f.Close()
		servers = f
	}
	cfg, err := config.Load(os.Getenv, servers)
	if err != nil {
		log.Fatal(err)
	}
	st, err := store.Open(cfg.DBPath)
	if err != nil {
		log.Fatal(err)
	}
	defer st.Close()

	lobbies := lobby.NewManager(time.Now, rand.New(rand.NewSource(time.Now().UnixNano())))
	orch := match.New(match.Deps{
		Lobbies: lobbies, Pool: pool.New(cfg.Servers), RCON: pool.NewRCON(), Store: st,
		BaseURL: cfg.BaseURL, WebhookSecret: cfg.WebhookSecret, Admins: cfg.AdminSteamIDs,
	})
	// Starting a match talks to the CS2 server, so keep it off the ticker goroutine.
	lobbies.OnMapChosen = func(id, mapName string) { go orch.Start(id, mapName) }
	lobbies.OnEmpty = func(id string) { go orch.LobbyGone(id) }
	handler := web.New(web.Deps{
		Matches:  orch,
		Config:   cfg,
		Store:    st,
		Sessions: auth.NewSessions([]byte(cfg.SessionSecret), 30*24*time.Hour),
		Lobbies:  lobbies,
	})

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	go func() {
		t := time.NewTicker(time.Second)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				lobbies.Tick()
			}
		}
	}()

	srv := &http.Server{Addr: cfg.ListenAddr, Handler: handler, ReadHeaderTimeout: 10 * time.Second}
	go func() {
		<-ctx.Done()
		shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		srv.Shutdown(shutdown)
	}()
	log.Printf("listening on %s (base URL %s, %d servers)", cfg.ListenAddr, cfg.BaseURL, len(cfg.Servers))
	if err := srv.ListenAndServe(); !errors.Is(err, http.ErrServerClosed) {
		log.Fatal(err)
	}
}

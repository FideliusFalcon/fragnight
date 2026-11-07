package main

import (
	"io"
	"log"
	"os"

	"github.com/fideliusfalcon/fragnight/internal/config"
	"github.com/fideliusfalcon/fragnight/internal/store"
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
	log.Printf("config ok: %d servers, db %s", len(cfg.Servers), cfg.DBPath)
}

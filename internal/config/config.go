package config

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/fideliusfalcon/fragnight/internal/pool"
)

type Config struct {
	ListenAddr    string
	BaseURL       string
	DBPath        string
	SessionSecret string
	SteamAPIKey   string
	WebhookSecret string
	DevLogin      bool
	AdminSteamIDs []string
	Servers       []pool.Server
}

// Load reads configuration from environment variables (via getenv) and the
// server pool from a JSON array.
func Load(getenv func(string) string, serversFile io.Reader) (Config, error) {
	c := Config{
		ListenAddr:    getenv("LISTEN_ADDR"),
		BaseURL:       strings.TrimRight(getenv("BASE_URL"), "/"),
		DBPath:        getenv("DB_PATH"),
		SessionSecret: getenv("SESSION_SECRET"),
		SteamAPIKey:   getenv("STEAM_API_KEY"),
		WebhookSecret: getenv("WEBHOOK_SECRET"),
		DevLogin:      getenv("DEV_LOGIN") == "1",
	}
	if c.BaseURL == "" {
		return c, fmt.Errorf("BASE_URL is required")
	}
	if c.SessionSecret == "" {
		return c, fmt.Errorf("SESSION_SECRET is required")
	}
	if c.ListenAddr == "" {
		c.ListenAddr = ":8080"
	}
	if c.DBPath == "" {
		c.DBPath = "lobby.db"
	}
	for _, id := range strings.Split(getenv("ADMIN_STEAMIDS"), ",") {
		if id = strings.TrimSpace(id); id != "" {
			c.AdminSteamIDs = append(c.AdminSteamIDs, id)
		}
	}
	if serversFile != nil {
		if err := json.NewDecoder(serversFile).Decode(&c.Servers); err != nil {
			return c, fmt.Errorf("servers file: %w", err)
		}
	}
	return c, nil
}

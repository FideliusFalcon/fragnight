package config

import (
	"strings"
	"testing"
)

func env(m map[string]string) func(string) string {
	return func(k string) string { return m[k] }
}

func TestLoad_RequiresBaseURLAndSecret(t *testing.T) {
	_, err := Load(env(map[string]string{"SESSION_SECRET": "s"}), strings.NewReader("[]"))
	if err == nil || !strings.Contains(err.Error(), "BASE_URL") {
		t.Fatalf("want error naming BASE_URL, got %v", err)
	}
	_, err = Load(env(map[string]string{"BASE_URL": "http://x"}), strings.NewReader("[]"))
	if err == nil || !strings.Contains(err.Error(), "SESSION_SECRET") {
		t.Fatalf("want error naming SESSION_SECRET, got %v", err)
	}
}

func TestLoad_ParsesAdminsAndServers(t *testing.T) {
	servers := `[{"id":"cs2-1","name":"One","host":"10.0.0.5","port":27015,"rcon_password":"a"},
	             {"id":"cs2-2","name":"Two","host":"10.0.0.5","port":27016,"rcon_password":"b"}]`
	c, err := Load(env(map[string]string{
		"BASE_URL": "http://x", "SESSION_SECRET": "s", "ADMIN_STEAMIDS": "1,2", "DEV_LOGIN": "1",
	}), strings.NewReader(servers))
	if err != nil {
		t.Fatal(err)
	}
	if len(c.AdminSteamIDs) != 2 || c.AdminSteamIDs[0] != "1" || c.AdminSteamIDs[1] != "2" {
		t.Fatalf("admins: %v", c.AdminSteamIDs)
	}
	if len(c.Servers) != 2 || c.Servers[1].Port != 27016 || c.Servers[0].RCONPassword != "a" {
		t.Fatalf("servers: %+v", c.Servers)
	}
	if !c.DevLogin || c.ListenAddr != ":8080" || c.DBPath != "lobby.db" {
		t.Fatalf("defaults: %+v", c)
	}
}

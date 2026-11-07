package settings

import (
	"fmt"
	"reflect"
	"slices"
	"testing"
)

func tooMany() []string {
	var out []string
	for i := 0; i < 25; i++ {
		out = append(out, fmt.Sprintf("de_map%d", i))
	}
	return out
}

func TestDefaults_AreTournament(t *testing.T) {
	d := Defaults()
	if d.MR != 12 || !d.Overtime || d.OTMaxRounds != 6 || d.OTStartMoney != 10000 ||
		!d.KnifeRound || !d.FriendlyFire || d.DeadTalk || d.TimeoutsPerTeam != 4 || d.TimeoutSeconds != 30 {
		t.Fatalf("defaults: %+v", d)
	}
	if !reflect.DeepEqual(d.Maps, DefaultMapPool) {
		t.Fatalf("maps: %v", d.Maps)
	}
	if err := d.Validate(); err != nil {
		t.Fatal(err)
	}
}

func TestDefaultPool_IsCurrentActiveDuty(t *testing.T) {
	want := []string{"de_ancient", "de_anubis", "de_cache", "de_dust2", "de_inferno", "de_mirage", "de_nuke"}
	if !reflect.DeepEqual(DefaultMapPool, want) {
		t.Fatalf("pool %v, want %v", DefaultMapPool, want)
	}
	for _, m := range []string{"de_overpass", "de_train", "de_vertigo"} {
		if slices.Contains(DefaultMapPool, m) {
			t.Errorf("%s is no longer Active Duty", m)
		}
	}
}

func TestOtherMaps_AreSelectableAndNotInDefaultPool(t *testing.T) {
	for _, m := range []string{"de_overpass", "de_train", "de_vertigo", "de_canals", "cs_office", "cs_italy"} {
		if !slices.Contains(OtherMaps, m) {
			t.Errorf("%s missing from OtherMaps", m)
		}
		if slices.Contains(DefaultMapPool, m) {
			t.Errorf("%s should not be a default", m)
		}
		s := Defaults()
		s.Maps = []string{m}
		if err := s.Validate(); err != nil {
			t.Errorf("%s rejected: %v", m, err)
		}
	}
}

func TestDefaults_MapsAreACopy(t *testing.T) {
	d := Defaults()
	d.Maps[0] = "de_changed"
	if DefaultMapPool[0] == "de_changed" {
		t.Fatal("Defaults shares the DefaultMapPool slice")
	}
}

func TestValidate(t *testing.T) {
	cases := map[string]func(*Match){
		"empty maps":        func(m *Match) { m.Maps = nil },
		"bad map chars":     func(m *Match) { m.Maps = []string{"de_dust2; quit"} },
		"uppercase map":     func(m *Match) { m.Maps = []string{"de_Dust2"} },
		"path in map":       func(m *Match) { m.Maps = []string{"../de_dust2"} },
		"workshop id":       func(m *Match) { m.Maps = []string{"workshop/123/de_x"} },
		"no prefix":         func(m *Match) { m.Maps = []string{"dust2"} },
		"quote in map":      func(m *Match) { m.Maps = []string{`de_x"`} },
		"duplicate map":     func(m *Match) { m.Maps = []string{"de_dust2", "de_dust2"} },
		"too many maps":     func(m *Match) { m.Maps = tooMany() },
		"mr zero":           func(m *Match) { m.MR = 0 },
		"odd ot rounds":     func(m *Match) { m.OTMaxRounds = 5 },
		"negative timeouts": func(m *Match) { m.TimeoutsPerTeam = -1 },
	}
	for name, mutate := range cases {
		m := Defaults()
		mutate(&m)
		if err := m.Validate(); err == nil {
			t.Errorf("%s: want error", name)
		}
	}
	m := Defaults()
	m.Overtime = false
	m.OTMaxRounds = 5 // irrelevant when overtime is off
	if err := m.Validate(); err != nil {
		t.Errorf("overtime off should ignore OT rounds: %v", err)
	}
}

func TestCvars_Defaults(t *testing.T) {
	c := Defaults().Cvars()
	want := map[string]string{
		"mp_maxrounds":           "24",
		"mp_overtime_enable":     "1",
		"mp_overtime_maxrounds":  "6",
		"mp_overtime_startmoney": "10000",
		"mp_friendlyfire":        "1",
		"sv_deadtalk":            "0",
		"mp_team_timeout_max":    "4",
		"mp_team_timeout_time":   "30",
	}
	for k, v := range want {
		if c[k] != v {
			t.Errorf("%s = %q, want %q", k, c[k], v)
		}
	}
}

func TestValidate_AcceptsCustomMapNames(t *testing.T) {
	m := Defaults()
	m.Maps = []string{"de_dust2", "de_mycustom_v2", "cs_agency", "ar_shoots"}
	if err := m.Validate(); err != nil {
		t.Fatalf("custom names rejected: %v", err)
	}
}

func TestValidate_AcceptsLimitsExactly(t *testing.T) {
	m := Defaults()
	m.MR, m.OTMaxRounds, m.OTStartMoney = MaxMR, MaxOTRounds, MaxMoney
	m.TimeoutsPerTeam, m.TimeoutSeconds = MaxTimeouts, MaxTimeoutSeconds
	if err := m.Validate(); err != nil {
		t.Fatalf("limits themselves must be allowed: %v", err)
	}
	m.MR = 1
	if err := m.Validate(); err != nil {
		t.Fatalf("MR 1: %v", err)
	}
}

func TestLimits_CoverTheTournamentDefaults(t *testing.T) {
	d := Defaults()
	if d.MR > MaxMR || d.OTMaxRounds > MaxOTRounds || d.OTStartMoney > MaxMoney || d.TimeoutsPerTeam > MaxTimeouts || d.TimeoutSeconds > MaxTimeoutSeconds {
		t.Fatalf("defaults exceed the limits: %+v", d)
	}
	if MaxMR < 15 {
		t.Fatalf("MR15 must stay possible, MaxMR=%d", MaxMR)
	}
}

func TestIsCustom(t *testing.T) {
	if IsCustomMap("de_dust2") || IsCustomMap("de_cache") || IsCustomMap("cs_office") || IsCustomMap("de_vertigo") {
		t.Fatal("known maps are not custom")
	}
	if !IsCustomMap("de_mymap") || !IsCustomMap("ar_shoots") {
		t.Fatal("unknown maps are custom")
	}
	m := Defaults()
	if m.HasCustomMaps() {
		t.Fatal("defaults have no custom maps")
	}
	m.Maps = append(m.Maps, "de_mymap")
	if !m.HasCustomMaps() {
		t.Fatal("custom map not detected")
	}
}

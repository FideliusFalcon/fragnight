package settings

import (
	"reflect"
	"testing"
)

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
		"unknown map":       func(m *Match) { m.Maps = []string{"de_nowhere"} },
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

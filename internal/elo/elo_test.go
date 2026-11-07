package elo

import "testing"

func TestDeltas_EqualTeams(t *testing.T) {
	d1, d2 := Deltas(1000, 1000, true)
	if d1 != 16 || d2 != -16 {
		t.Fatalf("got %d, %d", d1, d2)
	}
	d1, d2 = Deltas(1000, 1000, false)
	if d1 != -16 || d2 != 16 {
		t.Fatalf("team 2 win: got %d, %d", d1, d2)
	}
}

func TestDeltas_UnderdogGainsMore(t *testing.T) {
	underdog, _ := Deltas(1000, 1200, true)
	_, favourite := Deltas(1000, 1200, false)
	if underdog <= favourite || favourite <= 0 {
		t.Fatalf("underdog +%d, favourite +%d", underdog, favourite)
	}
}

func TestDeltas_ZeroSum(t *testing.T) {
	for a := 600.0; a <= 1600; a += 37 {
		for b := 600.0; b <= 1600; b += 41 {
			for _, won := range []bool{true, false} {
				d1, d2 := Deltas(a, b, won)
				if d1+d2 != 0 {
					t.Fatalf("not zero-sum: %v %v %v -> %d %d", a, b, won, d1, d2)
				}
			}
		}
	}
}

func TestAverage(t *testing.T) {
	if got := Average([]int{1000, 1100, 1200}); got != 1100 {
		t.Fatalf("got %v", got)
	}
	if got := Average(nil); got != 1000 {
		t.Fatalf("empty team should default to 1000, got %v", got)
	}
}

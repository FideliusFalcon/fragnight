// Package elo computes rating changes for team matches.
package elo

import "math"

// K is the maximum rating change for one match.
const K = 32

// Average returns the mean rating of a team, or 1000 for an empty team.
func Average(ratings []int) float64 {
	if len(ratings) == 0 {
		return 1000
	}
	sum := 0
	for _, r := range ratings {
		sum += r
	}
	return float64(sum) / float64(len(ratings))
}

// Deltas returns the rating change for every member of team 1 and of team 2.
// d2 is always -d1, so a match between equal-sized teams is zero-sum.
func Deltas(team1Avg, team2Avg float64, team1Won bool) (d1, d2 int) {
	expected1 := 1 / (1 + math.Pow(10, (team2Avg-team1Avg)/400))
	score := 0.0
	if team1Won {
		score = 1
	}
	d1 = int(math.Round(K * (score - expected1)))
	return d1, -d1
}

// Package leaderboard owns the Reaction board: the app's score formulas ported to Go,
// POST /v1/scores with a server-side recompute, and GET /v1/board.
package leaderboard

import (
	"math"
	"slices"
)

// The reaction-trial constants, verbatim from the app's CTCore/Formulas.swift. Every
// value is a cross-port contract (Swift, Rust, Kotlin, and now this server); the
// proof is testdata/reaction_score.json, copied from the app's parity fixtures.
const (
	// TrialAttempts is the number of attempts in a trial: hits plus misfires.
	TrialAttempts = 3
	// FrameMs is one 60 fps frame in milliseconds.
	FrameMs float64 = 1000.0 / 60.0
	// PBFloorFrames and PBCeilFrames bound the high-score plausibility band: faster
	// than ~10 frames is superhuman, slower than 200 frames is not a reaction. The
	// app shows a result outside the band but writes no PB and no log row, so nothing
	// outside it can be an honest submission.
	PBFloorFrames float64 = 10
	PBCeilFrames  float64 = 200
	// SubThresholdMs is the sub-13-frame kicker threshold (about 216.7 ms).
	SubThresholdMs float64 = 13.0 * 1000.0 / 60.0
	// MinPlausibleReactionMs and MaxPlausibleReactionMs bound one press: the app
	// discards a press outside them before it ever becomes a sample.
	MinPlausibleReactionMs float64 = 100
	MaxPlausibleReactionMs float64 = 3000
)

// Frames converts milliseconds to 60 fps frames.
func Frames(ms float64) float64 { return ms / FrameMs }

// IsValidHighScore reports whether bestMs sits inside the 10 to 200 frame band.
func IsValidHighScore(bestMs float64) bool {
	floor := PBFloorFrames * FrameMs
	ceil := PBCeilFrames * FrameMs
	return bestMs >= floor && bestMs <= ceil
}

// TierName is the SF6 ranked-ladder skill tier for a best reaction, UNRANKED outside
// the plausibility band.
func TierName(bestMs float64) string {
	if !(bestMs > 0) || !IsValidHighScore(bestMs) {
		return "UNRANKED"
	}
	f := Frames(bestMs)
	switch {
	case f < 10.5:
		return "LEGEND"
	case f < 11:
		return "ULTIMATE MASTER"
	case f < 11.5:
		return "HIGH MASTER"
	case f < 12:
		return "MASTER"
	case f < 13:
		return "DIAMOND"
	case f < 14:
		return "PLATINUM"
	case f < 15.5:
		return "GOLD"
	case f < 17:
		return "SILVER"
	case f < 19:
		return "BRONZE"
	default:
		return "ROOKIE"
	}
}

var gradeLadder = []string{"F", "D", "C", "B", "B+", "A", "A+", "S", "S+", "SS"}

// GradeFor is the letter grade for a composite trial score.
func GradeFor(score int) string {
	switch {
	case score >= 2200:
		return "SS"
	case score >= 920:
		return "S+"
	case score >= 820:
		return "S"
	case score >= 720:
		return "A+"
	case score >= 620:
		return "A"
	case score >= 500:
		return "B+"
	case score >= 380:
		return "B"
	case score >= 250:
		return "C"
	case score >= 120:
		return "D"
	default:
		return "F"
	}
}

// CapGrade returns the lower of g and cap on the grade ladder; an unknown grade is
// returned unchanged.
func CapGrade(g, cap string) string {
	gi, ci := slices.Index(gradeLadder, g), slices.Index(gradeLadder, cap)
	if gi < 0 || ci < 0 {
		return g
	}
	return gradeLadder[min(gi, ci)]
}

// TrialScore is every component of a scored trial, the shape of CTFormulas.TrialScore.
type TrialScore struct {
	BestMs       float64
	AvgMs        float64
	Accuracy     float64
	Base         int
	AccBonus     int
	ConsistBonus int
	SubBonus     int
	Flawless     bool
	Score        int
	Grade        string
}

// ScoreTrial scores a completed trial from its hit samples (ms) and misfire count,
// with the semantics of CTFormulas.scoreTrial: best-of-N speed, linear accuracy
// weighting, the four bonuses, and the worst-attempt grade cap.
//
// Explicit float64 conversions keep every product rounded on its own, so the Go
// compiler cannot fuse a multiply-add and drift from the app's IEEE arithmetic.
func ScoreTrial(samples []float64, misfires int) TrialScore {
	attempts := len(samples) + misfires
	bestMs, avgMs := 9999.0, 9999.0
	if len(samples) > 0 {
		bestMs = slices.Min(samples)
		sum := 0.0
		for _, s := range samples {
			sum += s
		}
		avgMs = sum / float64(len(samples))
	}
	accuracy := 0.0
	if attempts > 0 {
		accuracy = float64(len(samples)) / float64(attempts)
	}
	penalty := float64((bestMs - 100.0) * 4.0)
	speedScore := math.Max(0.0, 1000.0-penalty)
	base := int(float64(speedScore * accuracy))

	accBonus := 0
	if accuracy >= 0.999 && attempts >= TrialAttempts {
		accBonus = 150
	}
	consistBonus := 0
	if len(samples) >= 2 {
		spread := slices.Max(samples) - slices.Min(samples)
		switch {
		case spread < 30:
			consistBonus = 100
		case spread < 60:
			consistBonus = 50
		}
	}
	// The sub-13-frame kicker: flat per attempt under the threshold.
	subCount := 0
	for _, s := range samples {
		if s < SubThresholdMs {
			subCount++
		}
	}
	subBonus := subCount * 250
	flawless := accuracy >= 0.999 && len(samples) >= TrialAttempts && subCount >= TrialAttempts
	flawlessBonus := 0
	if flawless {
		flawlessBonus = 500
	}
	score := base + accBonus + consistBonus + subBonus + flawlessBonus

	grade := GradeFor(score)
	// The worst-attempt cap: best-of-N scoring alone would let one slow rep go
	// unpunished, so the top grades require every attempt to be decent.
	if len(samples) > 0 {
		worstFrames := float64(slices.Max(samples)*60) / 1000
		cap := ""
		switch {
		case worstFrames > 20:
			cap = "B"
		case worstFrames > 16:
			cap = "A"
		case worstFrames > 13:
			cap = "A+"
		}
		if cap != "" {
			grade = CapGrade(grade, cap)
		}
	}
	return TrialScore{
		BestMs: bestMs, AvgMs: avgMs, Accuracy: accuracy,
		Base: base, AccBonus: accBonus, ConsistBonus: consistBonus, SubBonus: subBonus,
		Flawless: flawless, Score: score, Grade: grade,
	}
}

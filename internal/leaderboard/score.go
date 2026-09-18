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
//
// LEGEND IS NOT ON THIS LADDER, and cannot be reached by any time. Aaron ruled it off on
// 2026-09-14 — "they should compete online if they want legend" — so a time tops out at
// ULTIMATE MASTER and LEGEND is earned by STANDING, on the board, at rank 1-3. Use
// TierNameForRow for anything a player sees on a ranked board; this function is the time
// ladder alone and is what a feed or a player summary wants.
//
// The bands are r15's and are pinned by testdata/reaction_score.json, which is the same
// parity fixture the app checks its own ladder against. THE TWO MOVE TOGETHER OR NEITHER
// MOVES: this ladder drifted from the app's for five days because the fixture here was a
// copy taken on 2026-09-11 and the ruling landed on 09-14, so the suite stayed green while
// serving a superseded ladder.
func TierName(bestMs float64) string {
	if !(bestMs > 0) || !IsValidHighScore(bestMs) {
		return "UNRANKED"
	}
	f := Frames(bestMs)
	switch {
	case f < 11.5:
		return "ULTIMATE MASTER"
	case f < 12:
		return "HIGH MASTER"
	case f < 13:
		return "MASTER"
	case f < 14.5:
		return "DIAMOND"
	case f < 16:
		return "PLATINUM"
	case f < 18:
		return "GOLD"
	case f < 20.5:
		return "SILVER"
	case f < 25:
		return "BRONZE"
	default:
		return "ROOKIE"
	}
}

// TierNameForRow is the tier a player SEES on a ranked board: the time ladder, with LEGEND
// granted for a top-three STANDING.
//
// A board row is a run, so its tier describes that run — the time it holds, plus the place
// that time took. rank is 1-based; 0 or negative is an upstream bug and never a podium.
//
// ONLY A RANKING MAY PASS A RANK HERE. Recent's "rank" is a position in a chronological
// feed and Bests has no standing at all, so both use TierName and neither may call this —
// passing a feed position in would paint LEGEND on the three newest submissions whatever
// their times, which is precisely the defect the app fixed on its own RECENT board in
// #6447. A rank is only a standing on a board that ranks.
func TierNameForRow(bestMs float64, rank int) string {
	name := TierName(bestMs)
	if name == "UNRANKED" {
		return name
	}
	if rank >= 1 && rank <= 3 {
		return "LEGEND"
	}
	return name
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

// ── Backing a fast best (app issue #6564) ────────────────────────────────────

// BackingThresholdFrames and BackingWithinFrames are the qualification rule for a fast
// best, ruled by Aaron on 2026-09-18 after a single anticipation took #1: a 10.1f best
// standing on a trial whose other two attempts were 17.6f and 17.7f.
//
// Nothing the app or this server already checked could see it. The press was after GO,
// outside the sub-frame reroute, over MinPlausibleReactionMs, inside the 10-frame floor,
// and the trial had all three attempts land — so every gate passed and the row ranked.
// The board's own rule was what rewarded it: fastest single, with nothing behind it.
//
// THE DISCRIMINATOR IS FASTEST VERSUS SECOND-FASTEST, NOT FASTEST VERSUS SLOWEST. A 3x
// fastest-to-slowest spread rule was considered first and would NOT have caught that row:
// 10.1f against 17.7f is only 1.75x. A genuine fast player is fast on every attempt, so
// the gap between their best and their next-best is small; an anticipation is a gap.
const (
	// BackingThresholdFrames is the speed at or above which no backing is required.
	// It is the MASTER boundary on the ladder rather than a new number, and nobody
	// anticipates their way to a slow time — policing one would only punish a casual
	// player for being inconsistent.
	BackingThresholdFrames float64 = 13.5
	// BackingWithinFrames is how close the second-fastest attempt must be. Aaron ruled
	// 2 rather than 1: one frame is tighter than genuine trial-to-trial variance and
	// would cut real players for noise.
	BackingWithinFrames float64 = 2
	// BackingSinceUnix GRANDFATHERS every run submitted before it. Aaron, 2026-09-18:
	// "Let's grandfather all existing EXCEPT for that 10 frame first place", and then,
	// on the date of that run, "That one was September 16th. You can lop that one off
	// right there."
	//
	// So the cutoff IS the dethroning. It is 2026-09-16T00:00:00Z, and the row it
	// removes is the 10.05f touch run submitted at 09:34 that morning against a
	// second-fastest of 17.56f. Every one of the other nineteen rows on the four
	// boards predates it and keeps its place, including two of Aaron's own that would
	// not pass the rule either.
	//
	// A DATE RATHER THAN A ROW ID ON PURPOSE. A keep-list is a second source of truth
	// that has to be maintained and audited; a date is one number, reads as a policy
	// rather than as a grudge, and says the honest thing — the standard changed on this
	// day and is not applied backwards.
	BackingSinceUnix int64 = 1789516800
)

// Qualifies reports whether a trial's fastest attempt may stand on a ranked board.
//
// A best at or slower than BackingThresholdFrames always qualifies. A faster one
// qualifies only if a SECOND attempt in the SAME trial landed within BackingWithinFrames
// of it — a later trial cannot reach back and validate an earlier one, which is the
// misreading the app's card copy was rewritten to avoid.
//
// A trial with fewer than two landed attempts cannot back anything up, so a single fast
// attempt below the threshold does not qualify however fast it was.
func Qualifies(attemptsMs []float64) bool {
	if len(attemptsMs) == 0 {
		return false
	}
	sorted := slices.Clone(attemptsMs)
	slices.Sort(sorted)
	best := sorted[0]
	if Frames(best) >= BackingThresholdFrames {
		return true
	}
	if len(sorted) < 2 {
		return false
	}
	// ADDED TO best RATHER THAN SUBTRACTED FROM sorted[1], to match qualifiedSQL's
	// `je.value <= s.best_ms + <within>` exactly. The two forms are not the same in
	// floating point: at the boundary the subtraction loses a bit and reports a second
	// attempt EXACTLY two frames back as outside the window, while the addition does
	// not. Go and the SQL must agree on every input or a run qualifies in a unit test
	// and vanishes from the board, so they are written the same way on purpose.
	return sorted[1] <= best+BackingWithinFrames*FrameMs
}

package leaderboard

import (
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"testing"
)

// fixtureBlob is the git blob id of tools/parity-fixtures/reaction_score.json in the
// app repo (see testdata/README.md). The copy must stay byte-identical.
//
// WHAT THIS PIN CAN AND CANNOT CATCH, because the difference cost five days. It compares
// the copy to a REMEMBERED hash, so it fires when somebody edits the copy here — and it
// stays silent when the APP's fixture moves, which is the direction that actually happened.
// The 09-11 copy kept matching its own 09-11 hash while the app's ladder changed on 09-14,
// so this suite was green the whole time it served a superseded ladder, and the app's board
// page showed LEGEND for a time three days after LEGEND stopped being a time.
//
// Updating this constant is therefore part of updating the fixture, never a separate
// chore: a stale pin is not a failing test, it is a passing one.
const fixtureBlob = "bb271c50e92f87bb76e9c3926df665329fd67c8f"

type fixture struct {
	Version   int `json:"version"`
	Constants struct {
		TrialAttempts          int     `json:"trialAttempts"`
		FrameMs                float64 `json:"frameMs"`
		PBFloorFrames          float64 `json:"pbFloorFrames"`
		PBCeilFrames           float64 `json:"pbCeilFrames"`
		MaxPlausibleReactionMs float64 `json:"maxPlausibleReactionMs"`
	} `json:"constants"`
	Band []struct {
		Name   string  `json:"name"`
		BestMs float64 `json:"bestMs"`
		Valid  bool    `json:"valid"`
	} `json:"band"`
	Tiers []struct {
		Name   string  `json:"name"`
		BestMs float64 `json:"bestMs"`
		Tier   string  `json:"tier"`
	} `json:"tiers"`
	Trials []struct {
		Name     string    `json:"name"`
		Samples  []float64 `json:"samples"`
		Misfires int       `json:"misfires"`
		Expect   struct {
			BestMs       float64 `json:"bestMs"`
			Accuracy     float64 `json:"accuracy"`
			Base         int     `json:"base"`
			AccBonus     int     `json:"accBonus"`
			ConsistBonus int     `json:"consistBonus"`
			SubBonus     int     `json:"subBonus"`
			Flawless     bool    `json:"flawless"`
			Score        int     `json:"score"`
			Grade        string  `json:"grade"`
		} `json:"expect"`
	} `json:"trials"`
}

func loadFixture(t *testing.T) fixture {
	t.Helper()
	raw, err := os.ReadFile("testdata/reaction_score.json")
	if err != nil {
		t.Fatal(err)
	}
	var f fixture
	if err := json.Unmarshal(raw, &f); err != nil {
		t.Fatal(err)
	}
	if f.Version != 2 {
		t.Fatalf("fixture version %d; this port was written against 2", f.Version)
	}
	return f
}

func TestFixtureIsVerbatimCopy(t *testing.T) {
	raw, err := os.ReadFile("testdata/reaction_score.json")
	if err != nil {
		t.Fatal(err)
	}
	h := sha1.New()
	fmt.Fprintf(h, "blob %d\x00", len(raw))
	h.Write(raw)
	if got := hex.EncodeToString(h.Sum(nil)); got != fixtureBlob {
		t.Fatalf("testdata/reaction_score.json blob %s, want %s: the copy drifted from the app's fixture", got, fixtureBlob)
	}
}

func TestFixtureConstantsMatch(t *testing.T) {
	c := loadFixture(t).Constants
	if c.TrialAttempts != TrialAttempts || c.FrameMs != FrameMs || c.PBFloorFrames != PBFloorFrames ||
		c.PBCeilFrames != PBCeilFrames || c.MaxPlausibleReactionMs != MaxPlausibleReactionMs {
		t.Fatalf("constants drifted: fixture %+v", c)
	}
}

func TestFixtureBand(t *testing.T) {
	for _, c := range loadFixture(t).Band {
		if got := IsValidHighScore(c.BestMs); got != c.Valid {
			t.Errorf("%s: IsValidHighScore(%v) = %v, want %v", c.Name, c.BestMs, got, c.Valid)
		}
	}
}

func TestFixtureTiers(t *testing.T) {
	for _, c := range loadFixture(t).Tiers {
		if got := TierName(c.BestMs); got != c.Tier {
			t.Errorf("%s: TierName(%v) = %q, want %q", c.Name, c.BestMs, got, c.Tier)
		}
	}
}

func TestFixtureTrials(t *testing.T) {
	f := loadFixture(t)
	if len(f.Trials) == 0 {
		t.Fatal("fixture has no trials")
	}
	for _, c := range f.Trials {
		got := ScoreTrial(c.Samples, c.Misfires)
		e := c.Expect
		if got.BestMs != e.BestMs || got.Accuracy != e.Accuracy || got.Base != e.Base ||
			got.AccBonus != e.AccBonus || got.ConsistBonus != e.ConsistBonus || got.SubBonus != e.SubBonus ||
			got.Flawless != e.Flawless || got.Score != e.Score || got.Grade != e.Grade {
			t.Errorf("%s: ScoreTrial(%v, %d) =\n  %+v\nwant\n  %+v", c.Name, c.Samples, c.Misfires, got, e)
		}
	}
}

func TestScoreTrialEdges(t *testing.T) {
	empty := ScoreTrial(nil, 3)
	if empty.BestMs != 9999 || empty.AvgMs != 9999 || empty.Accuracy != 0 || empty.Score != 0 || empty.Grade != "F" {
		t.Errorf("all-misfire trial = %+v", empty)
	}
	if got := ScoreTrial([]float64{200, 210, 220}, 0).AvgMs; got != 210 {
		t.Errorf("avg = %v, want 210", got)
	}
	if TierName(0) != "UNRANKED" || TierName(-5) != "UNRANKED" {
		t.Error("non-positive best must be UNRANKED")
	}
	if CapGrade("SS", "??") != "SS" || CapGrade("A", "S") != "A" || CapGrade("S", "A") != "A" {
		t.Error("CapGrade ladder")
	}
}

// TestLegendIsUnreachableByTime pins Aaron's 2026-09-14 ruling structurally rather than by
// checking one example: "they should compete online if they want legend."
//
// The time ladder tops out at ULTIMATE MASTER. If a future edit puts LEGEND back on it —
// which is what this service shipped for five days — this fails whatever band it is added
// at, because it sweeps the whole plausible range rather than sampling it.
func TestLegendIsUnreachableByTime(t *testing.T) {
	for ms := 1.0; ms <= 4000; ms += 0.5 {
		if got := TierName(ms); got == "LEGEND" {
			t.Fatalf("TierName(%.1f) = LEGEND: no time may earn it (Aaron, 2026-09-14). "+
				"LEGEND is a STANDING, granted by TierNameForRow at rank 1-3.", ms)
		}
	}
	// The control: the sweep must actually be reaching real tiers, or a TierName that
	// returned "" for everything would pass the loop above.
	if TierName(170) != "ULTIMATE MASTER" || TierName(230) != "DIAMOND" {
		t.Fatalf("the sweep is not exercising the ladder: 170 -> %q, 230 -> %q",
			TierName(170), TierName(230))
	}
}

// TestOnlyARankingGrantsLegend is the assertion a reasonable implementer gets wrong.
//
// Recent's rank is a position in a chronological feed and Bests has no standing at all, so
// neither may pass a rank to TierNameForRow — doing so would paint LEGEND on the three
// newest submissions whatever their times. The app shipped exactly that defect on its own
// RECENT board and fixed it in #6447; this is the same rule, server side.
func TestOnlyARankingGrantsLegend(t *testing.T) {
	const slow = 400.0 // 24f — BRONZE, nowhere near the top of the ladder

	if got := TierNameForRow(slow, 1); got != "LEGEND" {
		t.Errorf("TierNameForRow(%.0f, rank 1) = %q, want LEGEND: a top-three STANDING "+
			"grants it regardless of the time", slow, got)
	}
	if got := TierNameForRow(slow, 4); got != TierName(slow) {
		t.Errorf("TierNameForRow(%.0f, rank 4) = %q, want the time ladder's %q",
			slow, got, TierName(slow))
	}
	// A rank is 1-based; 0 or negative is an upstream bug and never a podium.
	for _, r := range []int{0, -1} {
		if got := TierNameForRow(slow, r); got == "LEGEND" {
			t.Errorf("TierNameForRow(%.0f, rank %d) = LEGEND: a non-positive rank is a bug, "+
				"not a placement", slow, r)
		}
	}
	// AND THE FEED MUST NOT BE ABLE TO ASK. TierName is what Recent and Bests call, and it
	// takes no rank at all — so the mistake is unrepresentable rather than merely avoided.
	// If this stops compiling because TierName grew a rank parameter, that is the defect.
	var _ func(float64) string = TierName
}

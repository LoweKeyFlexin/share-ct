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
const fixtureBlob = "7999aa76b48fcaee48fbc727164001a27fca9ebf"

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

# Parity fixture — copied verbatim from the app repo

| File | Source (private repo `LoweKeyFlexin/controller-tester-fgc`) | Copied at | git blob |
| --- | --- | --- | --- |
| `reaction_score.json` | `tools/parity-fixtures/reaction_score.json` | `origin/main` @ `a11e4f79ffa4e63ca4f757293458d472987673b5` | `7999aa76b48fcaee48fbc727164001a27fca9ebf` |

The fixture is the cross-port contract for the reaction formulas (band, tiers, full trial
scoring). iOS is the source of truth; **never edit the copy to make the server pass** — if
the Go port disagrees, the port has the bug. `score_test.go` asserts the copy's git blob id
still equals the one above, so a drifted copy fails the suite.

Refresh: `git show origin/main:tools/parity-fixtures/reaction_score.json > reaction_score.json`
in the app repo, then update this table.

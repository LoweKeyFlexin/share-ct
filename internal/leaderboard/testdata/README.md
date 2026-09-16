# Parity fixture — copied verbatim from the app repo

| File | Source (private repo `LoweKeyFlexin/controller-tester-fgc`) | Copied at | git blob |
| --- | --- | --- | --- |
| `reaction_score.json` | `tools/parity-fixtures/reaction_score.json` | `origin/main` @ `3aca8bd00fbc871561db3ead3c40f4ee003ed8c2` | `bb271c50e92f87bb76e9c3926df665329fd67c8f` |

The fixture is the cross-port contract for the reaction formulas (band, tiers, full trial
scoring). iOS is the source of truth; **never edit the copy to make the server pass** — if
the Go port disagrees, the port has the bug. `score_test.go` asserts the copy's git blob id
still equals the one above, so a drifted copy fails the suite.

**That check is one-directional, and the gap is what this table is for.** It compares the
copy to the hash written here, so it catches somebody editing the copy — and it says nothing
when the APP's fixture moves, because both sides still match a hash taken before the change.
That is what happened: the copy landed 2026-09-11, Aaron's ruling removing LEGEND from the
time ladder landed 09-14, and this suite stayed green for five days while the service served
the superseded ladder. **Whoever changes the app's fixture refreshes this copy in the same
pass**; nothing here will remind them.

Refresh: `git show origin/main:tools/parity-fixtures/reaction_score.json > reaction_score.json`
in the app repo, then update this table.

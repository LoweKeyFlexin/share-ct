# Share CT

The online side of **Controller Tester FGC** (iOS, iPadOS, macOS). Opt-in, beta.

Ships in this order:

1. **Reaction leaderboard**: best reaction-test scores by input source (touch, pad, keyboard).
2. Shared controller layouts.
3. Shared combos.

## Shape

- One small Go binary, one container, one SQLite file. Runs as a non-root user on a
  read-only filesystem, listens on 8080, writes only under `/data` and `/tmp`, logs to stdout.
- Built for `linux/arm64` by GitHub Actions on every push to `main`; the image is public on GHCR.
- Hosted by a friend on a Raspberry Pi. **It may go down.** The app says so.

## What it stores

A random per-device player id, a separate random public `player_ref`, the display name
the player typed, scores with the attempt timings, the app version and device model.
A player report stores the reporter, target, reason and review state. No player email, account,
ads or tracking. Players can rename or erase their data from the app.

## Status

M0 scoring API: players (register, rename, erase), score submission with a server-side
recompute, the board, and a front page that renders it. M1: the board ranks the fastest
single attempt by default and each row carries the winning submission's `device_label` and
`created_at`; erase answers `404` for a player already gone; a score body with an
undeclared field is refused. The app repo is private; this one
is public so the server's behaviour is inspectable and the image builds for free.

## Run

One static binary, configured by environment variables only. No secrets, no config files.

| Variable | Required | Default | Meaning |
| --- | --- | --- | --- |
| `DATABASE_PATH` | yes | — | SQLite file, e.g. `/data/leaderboard.db`. The `-wal` and `-shm` files sit beside it, so the directory must exist and be writable. Unset → exit 2 with a `fatal` log line. |
| `LISTEN_ADDR` | no | `0.0.0.0:8080` | Bind address. |
| `LOG_LEVEL` | no | `info` | `debug`, `info`, `warn` or `error`. |

Logs are JSON lines on stdout, one object per line. Every boot prints, in order:

```
{"msg":"starting","version":"…","commit":"…","go":"go1.23.x","database_path":"/data/leaderboard.db","listen_addr":"0.0.0.0:8080"}
{"msg":"migrations","applied":6,"from":0,"to":6}     ← applied is 0 on every later boot
{"msg":"listening","addr":"[::]:8080"}               ← Linux reports the 0.0.0.0 wildcard as the dual-stack [::]
```

Per request: method, path, status, bytes, ms. Never a query string, header, body or token.
`/healthz` requests are logged at `debug` so the container's own probe stays out of the log viewer.
Startup writes nothing outside the database's directory and `/tmp`; the server never creates
directories. `SIGTERM` drains in-flight requests (8 s) and exits 0.

The compose entry Will runs (image path lowercase, as GHCR requires):

```yaml
  leaderboard:
    image: ghcr.io/lowekeyflexin/share-ct:latest
    container_name: leaderboard
    user: "2000:2000"
    read_only: true
    tmpfs:
      - /tmp
    volumes:
      - /srv/aaronapp/data:/data
    ports:
      - "8080:8080"
    mem_limit: 768m
    cpus: 1.0
    pids_limit: 256
    restart: unless-stopped
    labels:
      - "aaron.visible=true"
    environment:
      - DATABASE_PATH=/data/leaderboard.db
```

**Healthcheck.** The image declares `HEALTHCHECK CMD ["/sharect", "-healthcheck"]` (30 s interval,
3 s timeout): the binary GETs its own `/healthz` and exits 0 on `ok`, because the distroless
runtime has no curl. Compose needs nothing extra.

**Running it yourself** (any machine with Docker; this is the command the CI smoke job uses):

```sh
mkdir -p data && sudo chown 2000:2000 data
docker run -d --name sharect --platform linux/arm64 --user 2000:2000 --read-only --tmpfs /tmp \
  -v "$PWD/data:/data" -p 8080:8080 -e DATABASE_PATH=/data/leaderboard.db \
  ghcr.io/lowekeyflexin/share-ct:latest
curl localhost:8080/healthz   # ok
```

Without Docker: `DATABASE_PATH=/tmp/sharect.db go run ./cmd/sharect`.

**Image tags.** Every push to `main` publishes `:latest` and `:sha-<short>`; pull requests build
the same image and smoke it without pushing.

## API

Every body is JSON, capped at 4 KB. Errors are `{"error":"<code>"}`; a body that parsed
but broke a rule is `422 {"error":"invalid","reason":"<rule>"}`; a bad query is
`400 {"error":"bad_request","reason":"<param>"}`; over a limit is `429` with `Retry-After`
in seconds. `POST /v1/scores` is decoded strictly: a key it does not declare is
`422 {"error":"invalid","reason":"unknown_field"}`, never silently dropped. Authenticated
routes take `Authorization: Bearer <token>`: `401` without a valid token, `403` for another
player's id.

| Route | Auth | Response |
| --- | --- | --- |
| `GET /healthz` | – | `200` `ok` after `SELECT 1`; `503` otherwise. |
| `GET /` | – | The board page: one HTML document, inline CSS, one inline script (CSP hash-pinned) that fetches `/v1/board`. |
| `POST /v1/players` | – | `201 {player_id, token}` |
| `GET /v1/players/{id}` | – | `200 {player_id, player_short, display_name, platform, best:{touch,pad,keyboard}, submissions, created_at}` |
| `PATCH /v1/players/{id}` | bearer, own id | `200 {player_id, player_short, display_name, platform}` |
| `DELETE /v1/players/{id}` | bearer, own id | `204`, the player and every submission erased; `404` when the id is unknown, an already-erased player included, which the app treats as finished too |
| `POST /v1/scores` | bearer | `201 {submission_id, rank, board_size}`; `200` with the original row on a repeated `client_id`; `422 reason unknown_field` on any key outside the twelve declared |
| `GET /v1/board` | – | `200 {source, window, entries:[{rank, player_ref, player_short, display_name, score, best_ms, avg_ms, accuracy, tier, platform, device_label, created_at}]}`; `?sort=fastest` (default) or `score`, see below |
| `GET /v1/recent` | – | `200 {source:"recent", window:"all", entries:[...]}`; every entry also has `player_ref`. |
| `POST /v1/reports` | bearer | `201 {report_id}` after a new report is stored; `202 {report_id}` for an identical earlier report |
| anything else | – | `404 {"error":"not_found"}` |

### Register a player

The device mints nothing itself: it sends a display name and gets an opaque id and a
bearer token back. Only `sha256(token)` is stored; a lost token means a new player.
Display names follow the app's username rules: trimmed, 3–20 characters, letters, marks, numbers
and spaces, reserved handles (`ADMIN`, `NO NAME`, `CT`, …) refused. Names matching the
versioned ASCII content policy are also refused. Ten registrations a
day per IP.

```sh
curl -sS -X POST https://ct.bond-haus.com/v1/players \
  -H 'Content-Type: application/json' \
  -d '{"display_name":"Aaron","platform":"ios"}'
# {"player_id":"6f1a2b3c-4d5e-4f60-8a7b-9c0d1e2f3a4b","token":"…43 chars…"}
```

`reason` on 422: `name_too_short`, `name_too_long`, `name_invalid_characters`, `name_reserved`,
`name_inappropriate`, `platform`
(`ios`, `mac`, `windows` or `android`).

The content-policy refusal is the same on registration and rename:
`422 {"error":"invalid","reason":"name_inappropriate"}`. It never echoes the
submitted name. An empty or whitespace-only name is still accepted as anonymous.
The embedded policy has format `CT-NAME-FILTER` v1, normalization
`ASCII_UPPER_SPACE_V1`, and matching revision `ASCII_NAME_CANDIDATES_V3`; its
SHA-256 is `847845bbcd6cd4044d85021573d192a648d05b489fba783e95312aacfe2f0b5a`.
The startup log prints this revision hash so an operator can identify the deployed
policy. The digest asset contains no plaintext reviewed terms. It must be updated
together with the app's `tools/name-filter/blocked-names-v1.sha256` and parity tests.
The reviewed source's `I` and `L` positions have bounded `1`-substitution
variants generated into the digest asset offline (`IL_TO_ONE_ALL_SUBSETS_V1`,
at most four positions per reviewed term). At request time the matcher
leaves `1` literal and folds only `0/3/4/5/7/8` to their reviewed letter
lookalikes, so mixed `I/L` substitutions have a fixed matching cost.

The existing name validator continues to admit Unicode letters, marks and numbers.
This first portable digest policy intentionally checks **ASCII names only**; it
does not claim Unicode confusable coverage. A reviewed, separately versioned
Unicode policy and cross-platform vectors are required before treating the
online name filter as complete. Hashes of short terms are recoverable by guessing,
so the digest asset provides obscurity from casual source inspection, not secrecy.

The policy gates registration and rename only; a name stored before it is shown
unchanged (hiding an existing name is a deferred review action).

### Rename, erase, look up

```sh
curl -sS -X PATCH https://ct.bond-haus.com/v1/players/$PLAYER \
  -H "Authorization: Bearer $TOKEN" -H 'Content-Type: application/json' \
  -d '{"display_name":"Aaron L"}'

curl -sS -X DELETE https://ct.bond-haus.com/v1/players/$PLAYER \
  -H "Authorization: Bearer $TOKEN"          # 204: "delete my data"; 404 once it is gone

curl -sS https://ct.bond-haus.com/v1/players/$PLAYER
# {"player_id":"…","player_short":"3A4B","display_name":"Aaron","platform":"ios",
#  "best":{"touch":{"score":2100,"best_ms":200,"avg_ms":205,"accuracy":1,"tier":"DIAMOND","platform":"ios","created_at":1800000000},
#          "pad":null,"keyboard":null},"submissions":1,"created_at":1800000000}
```

`DELETE` answers `204` when the player and every submission were erased, and `404` when the
id names no player, an already-erased one included. The app runs the erase as a job it
retries until it hears one of those two and treats both as finished: the token dies with
the row, so a retry after a lost `204` arrives without valid credentials, and a `401` there
would keep it retrying forever with credentials kept for a player that no longer exists. A
live player's id still needs its own token: `401` without a valid one, `403` with another
player's, never `404`, so a wrong token is never mistaken for "nothing to delete".

### Submit a score

The app sends the trial as it scored it. The server **recomputes** `best_ms`, `avg_ms`,
`accuracy` and `score` from `attempts_ms` and `misfires` with the app's own `scoreTrial`
(ported from `CTCore/Formulas.swift` and proven against the app's parity fixture) and
ranks only its own numbers; the client's four are kept in `client_*` columns for audit.

```sh
curl -sS -X POST https://ct.bond-haus.com/v1/scores \
  -H "Authorization: Bearer $TOKEN" -H 'Content-Type: application/json' \
  -d '{"client_id":"3B2F…","source":"touch",
       "best_ms":200,"avg_ms":205,"accuracy":1,"score":2100,
       "attempts_ms":[200,205,210],"misfires":0,
       "app_build":"2214","platform":"ios","device_model":"iPhone17,2","device_label":"Touch"}'
# 201 {"submission_id":17,"rank":1,"board_size":12}
```

The body is exactly these twelve keys: `client_id`, `source`, `best_ms`, `avg_ms`,
`accuracy`, `score`, `attempts_ms`, `misfires`, `app_build`, `platform`, `device_model`,
`device_label`. Any other key is `422 {"error":"invalid","reason":"unknown_field"}`: the
app's disclosure sheet promises its users exactly what leaves the phone, and refusing an
undeclared key is what keeps the two sides from drifting quietly. `rank` and `board_size`
in the reply are on the source's all-time board in its default `fastest` order, so they
match what a bare `GET /v1/board` shows.

Bounds, each a 422 `reason`:

| reason | rule |
| --- | --- |
| `attempts` | `len(attempts_ms) + misfires == 3`, at least one hit, `misfires >= 0` |
| `implausible` | every sample in `[100, 3000]` ms (the app discards anything else before it is a sample); `min(attempts_ms)` inside the app's high-score band, 10–200 frames (`166.7`–`3333.3` ms), under which the app itself writes no PB |
| `best_ms_mismatch` | `best_ms` is not `min(attempts_ms)` |
| `score_mismatch` | `score` is not what `scoreTrial` gives for these attempts |
| `client_id`, `source`, `platform`, `app_build`, `device` | missing, unknown or too long (`source` is `touch`, `pad` or `keyboard`) |

`client_id` is the app's `RTScore.id`: resubmitting it answers `200` with the original
row, so a retry after a dropped connection never double-posts. Limits: 10 submits a
minute per player, 60 a minute per IP (`CF-Connecting-IP` behind the tunnel).

### Read the board

```sh
curl -sS 'https://ct.bond-haus.com/v1/board?source=pad&window=30d&sort=fastest&limit=10'
# {"source":"pad","window":"30d","entries":[
#   {"rank":1,"player_short":"3A4B","display_name":"Aaron","score":2220,"best_ms":170,"avg_ms":175,
#    "accuracy":1,"tier":"LEGEND","platform":"ios",
#    "device_label":"DualSense Wireless Controller","created_at":"2026-09-11T03:00:00Z"}]}
```

`source` defaults to `touch`, `window` (`all` or `30d`) to `all`, `sort` to `fastest`,
`limit` to 50 (max 100). One row per player, their winning submission under the sort. The
three sources never share a ranking. `entries` is always an array.

| `sort` | order |
| --- | --- |
| `fastest` (default) | `best_ms` ascending; ties to the higher `score`, then the earlier submission |
| `score` | `score` descending; ties to the lower `best_ms`, then the earlier submission |

The default is `fastest` because that is how the app ranks its own local board (the fastest
single attempt), and a bare `/v1/board` must agree with the app about who is first. An
unknown `sort` is `400 {"error":"bad_request","reason":"sort"}`, never a fallback: a typo
that quietly returned a different order would be indistinguishable from the board being
wrong. Migration `005` adds `ix_sub_fastest ON submissions(source, best_ms ASC, score DESC,
created_at)`, the fastest order's index; `ix_sub_board` still serves `sort=score`.

`device_label` and `created_at` (RFC 3339, UTC, stamped by the server on arrival) belong
to the **winning submission**, the row the entry is ranked by, not to the player's latest:
a player whose best was set on a pad and who later played a worse trial on touch is listed
with the pad. Under `sort=score` the winning row is the highest-scoring one, so the label
and time can differ between the two sorts. `device_label` is `""` when the submission
carried none.

### Report a player

Every board and recent row includes `player_ref`, 32 lowercase hex characters backed by
128 cryptographically random bits. Migration 007 backfills old players inside its own
transaction; a new registration mints a fresh ref. The ref stays the same when the player
renames or submits scores. It is public, separate from both the bearer token and player id,
and is never accepted as authentication. Erasing the player removes it.

`POST /v1/reports` accepts exactly `{ "target_player_ref": "…", "reason": "spam" }`
with `spam`, `harassment`, or `inappropriate` as the only reasons. A bearer token is
required. Errors are `401 {"error":"unauthorized"}` for missing or invalid auth,
`404 {"error":"not_found"}` for a well-formed ref whose player is gone,
`422 {"error":"invalid","reason":"target_player_ref"|"reason"|"self_report"|"unknown_field"}`,
`429 {"error":"rate_limited"}` with `Retry-After`, or `500 {"error":"internal"}`.
There is no player-owned route on this endpoint, so `403` is not applicable. The client
must claim success only on 201 or 202. A report never changes board visibility by itself.

The reporter can create five distinct reports in a rolling 24 hours. An identical
reporter/target/reason tuple returns the same ID only while the newest case is pending
and less than 24 hours old. A dismissed or upheld case can be reported again immediately;
an older pending case can be reported again after the window.
The rolling quota is stored in SQLite, so a restart cannot reset it. A report and its

A report is storage only: it sends no alert, has no side effect, and never acts on the
reported player automatically. A storage failure cannot yield 201. Both reporter and
target have `ON DELETE CASCADE`, so erasing either player removes every report involving
them. Each report keeps a `state` (`pending`, `dismissed`, `upheld`) so later review
tooling needs no migration. None exists yet.

### Admin: the private reports page

`GET /admin` is a read-only list of player reports (newest first, at most 500) for the
board's owner. It is reachable only through Cloudflare Access on the `/admin` path. The team domain and AUD
tag are `internal/admin/admin.go`'s `AccessTeamDomain` and `AccessAudience`. Any request
without a valid Access login token gets `404`, exactly like an unknown path. The token (`Cf-Access-Jwt-Assertion`) is verified, never trusted:
it must be RS256-signed by the team's published keys, carry the application's AUD tag and
the team issuer, and be unexpired. Neither setting is a secret. The page is `no-store`,
scriptless, HTML-escaped, and never shows who is signed in. It changes nothing.

### Endless Drills

Each drill has an independent all-time high-score board at 100% speed with three lives.
One point counts a successful active reaction; a correct hold, ignored poke or feint earns
no point and costs no life. Reaction routes and ranking remain unchanged.

`POST /v1/drills/scores` requires the existing bearer identity and exactly these keys:
`client_id`, `drill_id`, `score`, `misses`, `source`, `app_build`, `platform`, `device_model`,
`device_label`. Drill IDs: `whiff-punish`, `hit-confirm`, `anti-air`, `crosscut`, `drive-impact`, `mental-stack`.
Ranked Mental Stack uses all five encounters at 100% speed; custom subsets remain
local practice. Practice requires all five drills unlocked; ranked Endless requires
all five Level 4 clears. The client enforces this eligibility before submission; the aggregate
API does not receive an encounter trace or a custom selection. The new category uses
the existing payload, sharing scope, retention and consent contract.
`score` must be an integer in 0–1,000,000; `misses` must equal 3 (natural game over).
Source is `touch`, `pad`, or `keyboard`; metadata follows Reaction length/platform bounds.
Unknown fields return 422. These aggregate counts are validated, not independently
recomputed from input events. There are no per-reaction traces.

A new run returns `201 {submission_id, rank, board_size}`. The last 100 accepted run
receipts per player return 200 for an identical retry and 422 `client_id_conflict` if
its id is reused with a different body. Once a receipt ages out, a replay still cannot
create a second board row, lower a best, or change the date of an equal score.
At most one best is stored per player/drill. Submission quotas are 10/minute/player
and 60/minute/IP. Player erasure cascades through both bests and retry receipts.

`GET /v1/drills/board?drill_id=whiff-punish&limit=50` returns
`{drill_id, entries:[], own:null, board_size}`. Limits default to 50 and cap at 100;
invalid drill/limit queries return 400. One row per player, highest score first,
ties to the earliest achievement. Rows include rank, public `player_ref`, player short,
display name, score, source, platform, controller name and server timestamp. A valid
optional bearer adds `own` even outside the requested top entries; an invalid bearer
returns 401. Public references support existing reports and local app blocks.

The website offers a separate selector for each drill. The app builds neither a board
read nor a score request until enrollment and acceptance of the expanded disclosure.
It never uploads earlier local Endless runs on enrollment alone.

## Endless Drills Recent Scores

`POST /v1/drills/scores` keeps its original **best-only** contract. Older apps and
already-queued best-only submissions never enter the new public feed. Clients must
obtain renewed consent before automatically sending newly completed runs to
`POST /v1/drills/runs`. That route accepts the identical strict score body and
transactionally appends one run, updates the personal best only if higher, and
stores its retry receipt. A repeated client ID/body/route returns the original
receipt; a different body or sharing route conflicts rather than changing intent.
Lower, tied and zero-point completed runs enter Recent without replacing a higher
personal best. Historical best rows are not backfilled into Recent.

`GET /v1/drills/board?drill_id=anti-air` retains its existing all-time ranking and
response shape. `&sort=recent` returns `sort:"recent"` and the newest accepted run
rows for that drill, ordered by server arrival then row ID **before** the requested
limit (default and maximum 50). Feed entries carry `rank:0`, never a date-index
standing, and an opaque stable `run_ref` derived from the public player reference
and random per-run client ID. No private player ID or token is exposed. `own` and
`board_size` retain the actual all-time standing. Unknown sorts are rejected.
The same name, platform and input-device metadata and local blocking/reporting
identity remain available on each run. The page's Recent switch shows this stream
when a drill is selected and refreshes every 15 seconds while visible.

Storage is bounded to the latest **200 run rows per player across all drills** and
**90 days**, whichever expires first. Runs are removed on new submissions, Recent
reads, at startup, and by an hourly worker tied to process shutdown; expired rows
are excluded from reads while awaiting routine deletion. All-time bests remain
independent. Retry receipts remain capped at 100/player; retained run rows also
preserve retry proof after that ring rolls over. `DELETE /v1/players/{id}` cascades
through the feed, bests, receipts and reports. Authentication against an erased
identity cannot recreate its data. Reaction tables and endpoints are unchanged.

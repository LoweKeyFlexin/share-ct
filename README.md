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
The draft reporting feature also stores a reporter, target, reason and review state.
The private alert recipient is not stored in the database. No player email, account,
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
| `POST /v1/reports` | bearer | `201 {report_id}` after a new report and its alert are committed; `202 {report_id}` for an identical earlier report. **Draft only: the review and mail adapter are not yet deployed.** |
| anything else | – | `404 {"error":"not_found"}` |

### Register a player

The device mints nothing itself: it sends a display name and gets an opaque id and a
bearer token back. Only `sha256(token)` is stored; a lost token means a new player.
Display names follow the app's username rules: trimmed, 3–15 characters, letters, digits
and spaces, reserved handles (`ADMIN`, `NO NAME`, `CT`, …) refused. Ten registrations a
day per IP.

```sh
curl -sS -X POST https://ct.bond-haus.com/v1/players \
  -H 'Content-Type: application/json' \
  -d '{"display_name":"Aaron","platform":"ios"}'
# {"player_id":"6f1a2b3c-4d5e-4f60-8a7b-9c0d1e2f3a4b","token":"…43 chars…"}
```

`reason` on 422: `name_too_short`, `name_too_long`, `name_invalid_characters`, `name_reserved`, `platform`
(`ios`, `mac`, `windows` or `android`).

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

### Draft player report API — release blocked

Every board and recent row includes `player_ref`, 32 lowercase hex characters backed by
128 cryptographically random bits. Migration 006 backfills old players inside its own
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
an older pending case can be reported again after the window. A new case gets a new alert.
The rolling quota is stored in SQLite, so a restart cannot reset it. A report and its
email outbox item commit together; a storage failure cannot yield 201. Both reporter
and target have `ON DELETE CASCADE`, and the outbox cascades from the report, including
after an attempted delivery.

The dispatcher commits a short claim with a 30-second lease before calling the provider,
then sends only the opaque report ID under a 10-second context deadline. The provider
adapter must honor cancellation and set its own network timeout. Failed sends retry with
backoff from one minute to one hour. Erasing either player removes the report and its
outbox item, including an in-flight claim, so completion cannot recreate a retryable
alert. An already-started email cannot be recalled and may arrive after erasure begins;
its ID-only link would then resolve to a missing report. A crash after provider acceptance
may also repeat the same ID. The future email adapter should deduplicate by that ID.

**Deployment decisions still required:** Aaron chose private email, but Will's mail
provider, credentials, private recipient address, and secure review URL are not confirmed. The
outbox exposes `moderation.AlertSender` and `DispatchDue`; no production adapter or
background runner is wired, so this code does **not** send email. Configure the future
adapter via private deployment secrets, never repo files or request logs. The email should
carry only the report ID and a link to an authenticated review interface; the reason,
target ref, name, and reporter details stay behind that interface. Proposed storm
policy: preserve all accepted reports in SQLite, send at most ten alert emails per hour,
and digest additional pending IDs hourly. This policy needs implementation and Aaron's
approval before release. Aaron is the proposed review owner: the alert must lead him to a
separate authenticated queue where he can read the case, dismiss or uphold it, globally
suppress an upheld target's public rows, record the decision, and reverse it. Will must
confirm the private recipient and support/response arrangement with Aaron at deployment.
No operator action belongs in the public player API. Until that workflow, the mail adapter,
privacy-policy/App Store disclosure review, and Will's exact local read-only compose test
are complete, keep this PR draft and do not merge it to `main` (which publishes the image).

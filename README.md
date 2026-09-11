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

A random per-device player id, the display name the player typed, scores with the
attempt timings, the app version and device model. No email, no account, no ads, no
tracking. Players can rename or erase their data from the app.

## Status

M0 scoring API: players (register, rename, erase), score submission with a server-side
recompute, the board, and a front page that renders it. The app repo is private; this one
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
{"msg":"migrations","applied":4,"from":0,"to":4}     ← applied is 0 on every later boot
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
in seconds. Authenticated routes take `Authorization: Bearer <token>`: `401` without a
valid token, `403` for another player's id.

| Route | Auth | Response |
| --- | --- | --- |
| `GET /healthz` | – | `200` `ok` after `SELECT 1`; `503` otherwise. |
| `GET /` | – | The board page: one HTML document, inline CSS, one inline script (CSP hash-pinned) that fetches `/v1/board`. |
| `POST /v1/players` | – | `201 {player_id, token}` |
| `GET /v1/players/{id}` | – | `200 {player_id, player_short, display_name, platform, best:{touch,pad,keyboard}, submissions, created_at}` |
| `PATCH /v1/players/{id}` | bearer, own id | `200 {player_id, player_short, display_name, platform}` |
| `DELETE /v1/players/{id}` | bearer, own id | `204`, the player and every submission erased |
| `POST /v1/scores` | bearer | `201 {submission_id, rank, board_size}`; `200` with the original row on a repeated `client_id` |
| `GET /v1/board` | – | `200 {source, window, entries:[{rank, player_short, display_name, score, best_ms, avg_ms, accuracy, tier, platform}]}` |
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
  -H "Authorization: Bearer $TOKEN"          # 204: "delete my data"

curl -sS https://ct.bond-haus.com/v1/players/$PLAYER
# {"player_id":"…","player_short":"3A4B","display_name":"Aaron","platform":"ios",
#  "best":{"touch":{"score":2100,"best_ms":200,"avg_ms":205,"accuracy":1,"tier":"DIAMOND","platform":"ios","created_at":1800000000},
#          "pad":null,"keyboard":null},"submissions":1,"created_at":1800000000}
```

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
curl -sS 'https://ct.bond-haus.com/v1/board?source=pad&window=30d&limit=10'
# {"source":"pad","window":"30d","entries":[
#   {"rank":1,"player_short":"3A4B","display_name":"Aaron","score":2220,"best_ms":170,"avg_ms":175,
#    "accuracy":1,"tier":"LEGEND","platform":"ios"}]}
```

`source` defaults to `touch`, `window` (`all` or `30d`) to `all`, `limit` to 50 (max 100).
One row per player, their best submission; ties go to the lower `best_ms`, then the earlier
submission. The three sources never share a ranking. `entries` is always an array.

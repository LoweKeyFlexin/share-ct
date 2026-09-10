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

Pipeline-proof skeleton: `/healthz`, the placeholder page, a placeholder `/v1/board`, the
`linux/arm64` image and the CI smoke job. No scoring API yet. The app repo is private; this one is public so the server's
behaviour is inspectable and the image builds for free.

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
{"msg":"migrations","applied":2,"from":0,"to":2}     ← applied is 0 on every later boot
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

| Route | Response |
| --- | --- |
| `GET /healthz` | `200` `ok` after `SELECT 1` succeeds; `503` otherwise. |
| `GET /` | The front page: static HTML, inline CSS, no scripts, no external assets. |
| `GET /v1/board` | **Placeholder.** Always `{"source":"touch","window":"all","entries":[]}` so the app can be pointed at it before the real board lands. |
| anything else | `404` `{"error":"not_found"}` |

The real API (`POST /v1/players`, `POST /v1/scores`, the filtered board) is specified in the
app repo's design brief and lands next.

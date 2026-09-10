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

Skeleton not yet pushed. The app repo is private; this one is public so the server's
behaviour is inspectable and the image builds for free.

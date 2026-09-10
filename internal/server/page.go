package server

import (
	"io"
	"net/http"
)

// indexHTML is the whole front page: inline CSS, no scripts, no external assets.
// Colours are Controller Tester's default Phosphor Wave palette.
const indexHTML = `<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>Share CT · BETA · MAY GO DOWN</title>
<style>
  :root { color-scheme: dark; --bg:#1A1325; --panel:#251A38; --line:#54346E; --ink:#F2E9FF; --dim:#A284C0; --accent:#7CF2A6; --warn:#FFB454; }
  html, body { margin:0; min-height:100%; background:var(--bg); color:var(--ink); font:16px/1.5 system-ui, -apple-system, "Segoe UI", Roboto, sans-serif; }
  main { max-width:36rem; margin:12vh auto 0; padding:0 1.5rem 4rem; }
  h1 { font-size:clamp(2.2rem, 6vw, 3.2rem); line-height:1.1; margin:0 0 .75rem; letter-spacing:.02em; }
  .beta { display:inline-block; border:1px solid var(--warn); color:var(--warn); border-radius:.35rem; padding:.15rem .65rem; font-size:.78rem; font-weight:600; letter-spacing:.14em; }
  p { color:var(--dim); margin:.75rem 0 0; }
  .panel { margin-top:2rem; padding:1.25rem 1.5rem; background:var(--panel); border:1px solid var(--line); border-radius:.75rem; }
  .panel h2 { margin:0; font-size:1.1rem; color:var(--accent); letter-spacing:.04em; }
</style>
</head>
<body>
<main>
  <h1>Share CT</h1>
  <span class="beta">BETA · MAY GO DOWN</span>
  <p>The online side of Controller Tester FGC. Hosted by a friend on a Raspberry Pi.</p>
  <section class="panel">
    <h2>Reaction leaderboard coming soon</h2>
    <p>Best reaction-test scores by input source: touch, pad, keyboard.</p>
  </section>
</main>
</body>
</html>
`

func handleIndex(w http.ResponseWriter, _ *http.Request) {
	h := w.Header()
	h.Set("Content-Type", "text/html; charset=utf-8")
	h.Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'; base-uri 'none'; form-action 'none'")
	h.Set("Cache-Control", "no-cache")
	_, _ = io.WriteString(w, indexHTML)
}

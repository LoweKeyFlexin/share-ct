package server

import (
	"crypto/sha256"
	"encoding/base64"
	"io"
	"net/http"
)

// boardScript is the whole client: it fetches /v1/board and builds rows with
// createElement/textContent, never innerHTML, so a display name can't become markup.
// Its sha256 is the only script the CSP admits, so the page stays one self-contained
// document with no external assets.
const boardScript = `(function () {
  'use strict';
  var FRAME_MS = 1000 / 60;
  var state = { source: 'touch', window: 'all', sort: 'fastest' };
  var status = document.getElementById('status');
  var table = document.getElementById('board');
  var tbody = table.querySelector('tbody');

  function cell(row, text, cls, label) {
    var td = document.createElement('td');
    td.textContent = text;
    if (cls) td.className = cls;
    if (label) td.setAttribute('data-label', label);
    row.appendChild(td);
    return td;
  }

  // A second line inside a cell: the device and the submitted time under a player,
  // the average and accuracy under the best. textContent throughout — a display name
  // is player-supplied and never reaches the page as markup.
  function sub(td, text) {
    if (!text) return;
    var span = document.createElement('span');
    span.className = 'sub';
    span.textContent = text;
    td.appendChild(span);
  }

  // created_at is RFC 3339 UTC. Render it in the reader's own zone; if the value is
  // missing or unparseable, show nothing rather than "Invalid Date".
  function when(iso) {
    if (!iso) return '';
    var d = new Date(iso);
    if (isNaN(d.getTime())) return '';
    return d.toLocaleDateString(undefined, { month: 'short', day: 'numeric' }) +
           ' · ' + d.toLocaleTimeString(undefined, { hour: 'numeric', minute: '2-digit' });
  }

  function render(board) {
    tbody.textContent = '';
    if (!board.entries.length) {
      table.hidden = true;
      status.textContent = 'No ' + state.source + ' scores yet.';
      return;
    }
    board.entries.forEach(function (e) {
      var tr = document.createElement('tr');
      cell(tr, String(e.rank), 'rank');
      var name = cell(tr, e.display_name + ' ', 'name');
      var tail = document.createElement('span');
      tail.className = 'tail';
      tail.textContent = '· ' + e.player_short;
      name.appendChild(tail);
      sub(name, [when(e.created_at), e.device_label].filter(Boolean).join(' · '));
      var sc = cell(tr, String(e.score), 'score', 'score');
      if (state.sort === 'score') { sc.classList.add('ranks'); } 
      var best = cell(tr, (e.best_ms / FRAME_MS).toFixed(1) + 'f · ' + Math.round(e.best_ms) + ' ms', 'best', 'best');
      if (state.sort === 'fastest') { best.classList.add('ranks'); }
      if (typeof e.avg_ms === 'number') {
        var detail = 'avg ' + Math.round(e.avg_ms) + ' ms';
        if (typeof e.accuracy === 'number') { detail += ' · ' + Math.round(e.accuracy * 100) + '%'; }
        sub(best, detail);
      }
      cell(tr, e.tier, 'tier', 'tier');
      cell(tr, e.platform, 'platform');
      tbody.appendChild(tr);
    });
    status.textContent = '';
    table.hidden = false;
  }

  function load() {
    status.textContent = 'Loading…';
    table.hidden = true;
    fetch('/v1/board?source=' + state.source + '&window=' + state.window + '&sort=' + state.sort + '&limit=50', { headers: { Accept: 'application/json' } })
      .then(function (r) { if (!r.ok) throw new Error(String(r.status)); return r.json(); })
      .then(render)
      .catch(function () {
        table.hidden = true;
        status.textContent = 'BOARD OFFLINE — beta, may go down.';
      });
  }

  Array.prototype.forEach.call(document.querySelectorAll('[data-source], [data-window], [data-sort]'), function (button) {
    button.addEventListener('click', function () {
      var key = button.hasAttribute('data-source') ? 'source'
              : button.hasAttribute('data-window') ? 'window' : 'sort';
      state[key] = button.getAttribute('data-' + key);
      Array.prototype.forEach.call(document.querySelectorAll('[data-' + key + ']'), function (other) {
        other.setAttribute('aria-pressed', String(other === button));
      });
      load();
    });
  });

  load();
})();
`

// scriptHash is the CSP hash source for boardScript.
var scriptHash = func() string {
	sum := sha256.Sum256([]byte(boardScript))
	return base64.StdEncoding.EncodeToString(sum[:])
}()

// indexHead is the page up to the opening <script>; indexTail closes it. Colours are
// Controller Tester's default Phosphor Wave palette (CTCore/Theme.swift).
const indexHead = `<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>Fighter CT · Reaction Leaderboard</title>
<style>
  :root {
    color-scheme: dark;
    --bg:#1A1325; --panel:#251A38; --panel2:#2E2144; --well:#1E152D; --line:#54346E; --line2:#70468E;
    --ink:#F2E9FF; --dim:#A284C0; --mute:#70568E; --accent:#7CF2A6; --accent2:#B467FF; --accent3:#43D9FF; --warn:#FFB454;
    --mono: ui-monospace, "SF Mono", Menlo, Consolas, monospace;
  }
  html, body { margin:0; min-height:100%; background:var(--bg); color:var(--ink); font:16px/1.5 system-ui, -apple-system, "Segoe UI", Roboto, sans-serif; }
  main { max-width:52rem; margin:0 auto; padding:clamp(2rem, 8vh, 5rem) 1.25rem 4rem; }
  header h1 { font-size:clamp(2.4rem, 7vw, 3.6rem); line-height:1; margin:0; letter-spacing:.02em; }
  .beta { display:inline-block; margin:.75rem 0 0; border:1px solid var(--warn); color:var(--warn); border-radius:.35rem; padding:.15rem .65rem; font-size:.74rem; font-weight:700; letter-spacing:.16em; }
  header p { color:var(--dim); margin:1rem 0 0; max-width:34rem; }
  .panel { margin-top:2.25rem; background:var(--panel); border:1px solid var(--line); border-radius:.9rem; }
  /* The table was 800px inside a 702px panel at 1024px wide and the panel clipped it, so
     PLATFORM was cut off on an iPad with no way to scroll to it. Wide content scrolls in
     its own box; the page body never does. */
  .scroller { overflow-x:auto; -webkit-overflow-scrolling:touch; border-radius:0 0 .9rem .9rem; }
  .note { font-size:.8rem; }
  .panel h2 { margin:0; padding:1.1rem 1.25rem .5rem; font-size:.8rem; letter-spacing:.18em; text-transform:uppercase; color:var(--accent); }
  .tabs { display:flex; gap:.4rem; flex-wrap:wrap; padding:0 1.25rem; }
  .tabs + .tabs { padding-top:.5rem; }
  .tabs button { font:inherit; font-size:.8rem; font-weight:700; letter-spacing:.12em; color:var(--dim); background:var(--well); border:1px solid var(--line); border-radius:.5rem; padding:.45rem .9rem; cursor:pointer; transition:color 150ms, border-color 150ms, transform 150ms; }
  .tabs button:hover { color:var(--ink); border-color:var(--line2); }
  .tabs button:focus-visible { outline:2px solid var(--accent3); outline-offset:2px; }
  .tabs button:active { transform:translateY(1px); }
  .tabs button[aria-pressed="true"] { color:var(--bg); background:var(--accent); border-color:var(--accent); }
  .window button[aria-pressed="true"] { color:var(--ink); background:var(--panel2); border-color:var(--accent2); }
  .sort button[aria-pressed="true"] { color:var(--bg); background:var(--accent3); border-color:var(--accent3); }
  #status { padding:1.25rem; margin:0; color:var(--dim); min-height:1.5rem; }
  #status:empty { display:none; }
  table { width:100%; border-collapse:collapse; margin-top:1rem; font-variant-numeric:tabular-nums; }
  th, td { padding:.65rem 1.25rem; text-align:left; border-top:1px solid var(--line); white-space:nowrap; }
  th { font-size:.7rem; letter-spacing:.14em; text-transform:uppercase; color:var(--mute); border-top:0; }
  tbody tr:hover td { background:var(--panel2); }
  td.rank { color:var(--mute); font-family:var(--mono); width:2rem; }
  td.name { font-weight:600; }
  .tail { color:var(--mute); font-family:var(--mono); font-weight:400; font-size:.85em; }
  .sub { display:block; margin-top:.15rem; color:var(--mute); font-family:var(--mono); font-weight:400;
         font-size:.76rem; letter-spacing:.02em; white-space:normal; }
  td.score { color:var(--accent); font-family:var(--mono); font-size:1.1rem; }
  td.score.ranks, td.best.ranks { font-weight:700; text-decoration:underline; text-underline-offset:.25rem;
                                  text-decoration-color:var(--accent3); }
  td.best { color:var(--dim); font-family:var(--mono); }
  td.tier { color:var(--accent2); font-size:.78rem; font-weight:700; letter-spacing:.1em; }
  td.platform { color:var(--mute); font-size:.78rem; text-transform:uppercase; letter-spacing:.1em; }
  /* Narrow: drop PLATFORM and TIER, never BEST. The time is the headline of a reaction
     board, and the avg/accuracy sub-line lives in that cell — hiding it took half of what
     the row is for off every phone. */
  @media (max-width: 52rem) { td.platform, th.platform { display:none; } }
  /* PHONE: stop being a table. Six columns in 390px produced a row that wrapped at every
     cell boundary — "14.6f ·" over "243 ms" over "avg 250" over "ms · 67%" — which is what
     Aaron saw. Each row becomes a block that reads top to bottom, like the app's own score
     log, and the numbers carry their labels because the header row is gone. */
  @media (max-width: 34rem) {
    thead { display:none; }
    table, tbody, tr, td { display:block; width:auto; }
    tr { position:relative; padding:.85rem 1rem .85rem 2.6rem; border-top:1px solid var(--line); }
    tbody tr:hover td { background:none; }
    td { padding:0; border:0; white-space:normal; }
    td.rank { position:absolute; left:1rem; top:.85rem; width:auto; }
    td.name { font-size:1.02rem; }
    td.tier { display:inline-block; margin-top:.35rem; }
    td.platform { display:none; }
    td.score, td.best { display:inline-block; vertical-align:top; margin-top:.35rem; font-size:.95rem; }
    td.score { margin-right:1.1rem; }
    td.score::before, td.best::before, td.tier::before {
      content: attr(data-label) " "; font-size:.68rem; letter-spacing:.12em;
      text-transform:uppercase; color:var(--mute); margin-right:.3rem;
    }
    td.score.ranks, td.best.ranks { text-decoration:none; }
    td.score.ranks::before, td.best.ranks::before { color:var(--accent3); }
    .sub { margin-top:.2rem; }
  }
  footer { margin-top:2rem; color:var(--mute); font-size:.85rem; }
  footer p { margin:.25rem 0; }
</style>
</head>
<body>
<main>
  <header>
    <h1>Fighter CT</h1>
    <span class="beta">BETA · MAY GO DOWN</span>
    <p>The reaction leaderboard for Fighter CT. Scores are opt-in from the app.</p>
  </header>
  <section class="panel" aria-labelledby="board-heading">
    <h2 id="board-heading">Reaction leaderboard</h2>
    <nav class="tabs" aria-label="Input source">
      <button type="button" data-source="touch" aria-pressed="true">TOUCH</button>
      <button type="button" data-source="pad" aria-pressed="false">PAD</button>
      <button type="button" data-source="keyboard" aria-pressed="false">KEYBOARD</button>
    </nav>
    <nav class="tabs sort" aria-label="Ranking">
      <button type="button" data-sort="fastest" aria-pressed="true">FASTEST</button>
      <button type="button" data-sort="score" aria-pressed="false">HIGH SCORE</button>
    </nav>
    <nav class="tabs window" aria-label="Time window">
      <button type="button" data-window="all" aria-pressed="true">ALL TIME</button>
      <button type="button" data-window="30d" aria-pressed="false">30 DAYS</button>
    </nav>
    <p id="status" role="status" aria-live="polite"></p>
    <div class="scroller">
    <table id="board" hidden>
      <thead><tr><th>#</th><th>Player</th><th>Score</th><th class="best">Best</th><th class="tier">Tier</th><th class="platform">Platform</th></tr></thead>
      <tbody></tbody>
    </table>
    </div>
  </section>
  <footer>
    <p>Score rewards consistency, not just one good rep: the biggest bonuses go to a trial whose
       <strong>three attempts are all under 13 frames</strong> (216.7&nbsp;ms). A slower best can outscore a faster one.</p>
    <p>Scores are recomputed on the server from the attempt timings. No accounts, no tracking; erase your data from the app.</p>
    <p class="note">Touch, keyboard and gamepads all have their own latency.</p>
  </footer>
</main>
<script>`

const indexTail = `</script>
</body>
</html>
`

func handleIndex(w http.ResponseWriter, _ *http.Request) {
	h := w.Header()
	h.Set("Content-Type", "text/html; charset=utf-8")
	h.Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'; script-src 'sha256-"+scriptHash+"'; connect-src 'self'; base-uri 'none'; form-action 'none'")
	h.Set("Cache-Control", "no-cache")
	_, _ = io.WriteString(w, indexHead)
	_, _ = io.WriteString(w, boardScript)
	_, _ = io.WriteString(w, indexTail)
}

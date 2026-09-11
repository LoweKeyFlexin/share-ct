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
  var state = { source: 'all', window: 'all', sort: 'fastest' };
  var status = document.getElementById('status');
  var board_ = document.getElementById('board');
  var ranked = document.getElementById('ranked');

  // Say which number ranks the board, and what the other one is.
  //
  // The server picks a different submission per player per metric, so on a score board a
  // row's time is the fastest attempt OF THE RUN THAT SCORED HIGHEST — not that player's
  // fastest, and not a record. A column headed BEST over that value would imply one. It
  // also means a player can appear with two different runs across the two boards, which is
  // correct and reads as an inconsistency unless it is said out loud.
  // TierName() on the server is a pure function of best_ms (SF6 frame ladder), so on a
  // score board the tier tracks the winning run's SPEED, not its score: a higher-scoring
  // run with a slower single reads as a LOWER tier. Correct, and baffling unless said.
  var RANKED_BY = {
    fastest: 'Ranked by fastest single attempt.',
    score: 'Ranked by highest score. The time and tier shown are from that run, not the player\'s fastest — tier is a speed rank.'
  };

  function el(tag, cls, text) {
    var n = document.createElement(tag);
    if (cls) n.className = cls;
    if (text != null) n.textContent = text;   // text only: a display name is player-supplied
    return n;
  }

  function frames(ms) { return (ms / FRAME_MS).toFixed(1) + 'f'; }

  // created_at is RFC 3339 UTC. Render it in the reader's own zone; if the value is
  // missing or unparseable, show nothing rather than "Invalid Date".
  function when(iso) {
    if (!iso) return '';
    var d = new Date(iso);
    if (isNaN(d.getTime())) return '';
    return d.toLocaleDateString(undefined, { month: 'short', day: 'numeric' }) +
           ' · ' + d.toLocaleTimeString(undefined, { hour: 'numeric', minute: '2-digit' });
  }

  function def(dl, term, value, cls) {
    if (value == null || value === '') return;
    dl.appendChild(el('dt', null, term));
    dl.appendChild(el('dd', cls, value));
  }

  // A tier is a rank name, so it wears its own colour — Aaron: "If it is a gold tier rank,
  // the color of the world gold should be gold." Derived from the name rather than mapped
  // by index, so a tier the server adds later degrades to the neutral class instead of
  // silently taking the colour of whatever sat at its position.
  function tierClass(name) {
    if (!name) return 'tier';
    var key = String(name).toLowerCase().replace(/[^a-z]/g, '');
    var known = { legend:1, ultimatemaster:'ultimate', highmaster:'highmaster', master:1,
                  diamond:1, platinum:1, gold:1, silver:1, bronze:1, rookie:1, unranked:1 };
    var hit = known[key];
    if (!hit) return 'tier';
    return 'tier t-' + (hit === 1 ? key : hit);
  }

  // One card. The headline is whichever number ranks the board, so the row leads with the
  // thing the list is sorted by; everything else is behind the disclosure.
  function card(e) {
    var byScore = state.sort === 'score';
    var row = el('details', 'row' + (e.rank <= 3 ? ' m' + e.rank : ''));
    var head = el('summary');

    head.appendChild(el('span', 'rk', String(e.rank)));

    var who = el('div', 'who');
    who.appendChild(el('b', null, e.display_name));
    who.appendChild(el('small', null,
      [when(e.created_at), e.device_label].filter(Boolean).join(' · ')));
    // On the mixed board the input is the only thing saying which device set the time,
    // so it is promoted out of the disclosure into the row itself.
    if (state.source === 'all' && e.source) {
      who.appendChild(el('span', 'srcpill', e.source.toUpperCase()));
    }
    head.appendChild(who);

    var big = el('div', 'big');
    big.appendChild(el('b', null, byScore ? String(e.score) : frames(e.best_ms)));
    big.appendChild(el('small', null, byScore ? frames(e.best_ms) : 'avg ' + frames(e.avg_ms)));
    head.appendChild(big);

    head.appendChild(el('span', 'chev', '›'));
    row.appendChild(head);

    var dl = el('dl', 'detail');
    def(dl, 'best', frames(e.best_ms) + ' · ' + Math.round(e.best_ms) + ' ms');
    def(dl, 'avg', frames(e.avg_ms) + ' · ' + Math.round(e.avg_ms) + ' ms');
    if (typeof e.accuracy === 'number') def(dl, 'landed', Math.round(e.accuracy * 100) + '%');
    def(dl, 'score', String(e.score));
    // The tier is a SPEED rank (TierName is a pure function of best_ms), so on a score
    // board it tracks the winning run's time and would read as a lower rank beside a
    // higher score. Shown only where it agrees with what ranks the list.
    if (!byScore) def(dl, 'tier', e.tier, tierClass(e.tier));
    if (Array.isArray(e.attempts_ms) && e.attempts_ms.length) {
      dl.appendChild(el('dt', null, 'run'));
      var runs = el('div', 'runs');
      var fastest = Math.min.apply(null, e.attempts_ms);
      e.attempts_ms.forEach(function (ms) {
        runs.appendChild(el('span', 'run' + (ms === fastest ? ' best' : ''),
                            frames(ms) + ' · ' + Math.round(ms) + ' ms'));
      });
      // A misfire consumed an attempt and produced no time, so the run is short. Showing
      // the gap is the difference between "they landed two" and "they only took two".
      for (var i = 0; i < (e.misfires || 0); i++) {
        runs.appendChild(el('span', 'run miss', 'missed'));
      }
      dl.appendChild(runs);
    }
    def(dl, 'input', e.device_label);
    def(dl, 'platform', e.platform);
    def(dl, 'set', when(e.created_at));
    def(dl, 'player', e.player_short);
    row.appendChild(dl);
    return row;
  }

  function render(board) {
    board_.textContent = '';
    if (!board.entries.length) {
      board_.hidden = true;
      ranked.textContent = '';
      status.textContent = state.source === 'all'
        ? 'No scores yet.'
        : 'No ' + state.source + ' scores yet.';
      return;
    }
    board.entries.forEach(function (e) { board_.appendChild(card(e)); });
    status.textContent = '';
    ranked.textContent = RANKED_BY[state.sort] || '';
    board_.hidden = false;
  }

  function load() {
    status.textContent = 'Loading…';
    ranked.textContent = '';
    board_.hidden = true;
    fetch('/v1/board?source=' + state.source + '&window=' + state.window + '&sort=' + state.sort + '&limit=50', { headers: { Accept: 'application/json' } })
      .then(function (r) { if (!r.ok) throw new Error(String(r.status)); return r.json(); })
      .then(render)
      .catch(function () {
        board_.hidden = true;
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

// pageVersion is the page's own revision, shown at the top right and bumped by hand on
// every visible change (Aaron 2026-09-11: "at a Ver number to the far right (start with
// Ver .01) for each revision"). Deliberately NOT the build sha: this counts revisions a
// reader would notice, not deploys — several pushes can carry one visible change, and a
// redeploy of identical content is not a new revision.
const pageVersion = ".02"

// indexHead is the page up to the opening <script>; indexTail closes it. Colours are
// Controller Tester's default Phosphor Wave palette (CTCore/Theme.swift).
const indexHead = `<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>Share CT · Reaction Leaderboard</title>
<style>
  :root {
    color-scheme: dark;
    --bg:#1A1325; --panel:#251A38; --panel2:#2E2144; --well:#1E152D; --line:#54346E; --line2:#70468E;
    --ink:#F2E9FF; --dim:#A284C0; --mute:#70568E; --accent:#7CF2A6; --accent2:#B467FF; --accent3:#43D9FF; --warn:#FFB454;
    --gold:#FFC93C; --silver:#C9D2E0; --bronze:#D08A4E;
    --t-legend:#FFF1B8; --t-ultimate:#E2A9FF; --t-highmaster:#B467FF; --t-master:#8A7CFF;
    --t-diamond:#7FE3FF; --t-platinum:#7CF2D6; --t-rookie:#8A93A6;
    --mono: ui-monospace, "SF Mono", Menlo, Consolas, monospace;
  }
  html, body { margin:0; min-height:100%; background:var(--bg); color:var(--ink); font:16px/1.5 system-ui, -apple-system, "Segoe UI", Roboto, sans-serif; }
  main { max-width:52rem; margin:0 auto; padding:clamp(2rem, 8vh, 5rem) 1.25rem 4rem; }
  header h1 { font-size:clamp(2.4rem, 7vw, 3.6rem); line-height:1; margin:0; letter-spacing:.02em; }
  .masthead { display:flex; align-items:baseline; justify-content:space-between; gap:1rem; }
  .ver { font-family:var(--mono); font-size:.78rem; letter-spacing:.1em; color:var(--mute);
         white-space:nowrap; }
  .beta { display:inline-block; margin:.75rem 0 0; border:1px solid var(--warn); color:var(--warn); border-radius:.35rem; padding:.15rem .65rem; font-size:.74rem; font-weight:700; letter-spacing:.16em; }
  header p { color:var(--dim); margin:1rem 0 0; max-width:34rem; }
  .panel { margin-top:2.25rem; background:var(--panel); border:1px solid var(--line); border-radius:.9rem; overflow:hidden; }
  .panelhead { padding:1.1rem 1.25rem .6rem; }
  .panel h2 { margin:0; font-size:.8rem; letter-spacing:.18em; text-transform:uppercase; color:var(--accent); }
  .ranked { margin:.35rem 0 0; color:var(--mute); font-size:.8rem; }
  #status { padding:1.25rem; margin:0; color:var(--dim); min-height:1.5rem; }
  #status:empty { display:none; }

  /* The board is a list of cards, not a table. Aaron, on the app's own score log:
     "have scores present more like the app where you can click down into them to view
     more and doesn't make the leaderboard selection take up so much of the app." */
  .board { list-style:none; margin:0; padding:0 .75rem .75rem; display:flex; flex-direction:column; gap:.5rem; }
  .row { border:1px solid var(--line); border-radius:.6rem; background:var(--well); overflow:hidden; }
  .row > summary { display:flex; align-items:center; gap:.75rem; padding:.7rem .85rem; cursor:pointer;
                   list-style:none; }
  .row > summary::-webkit-details-marker { display:none; }
  .row > summary:focus-visible { outline:2px solid var(--accent3); outline-offset:-2px; }
  .row:hover { border-color:var(--line2); }
  .rk { font-family:var(--mono); font-size:1rem; color:var(--mute); min-width:1.4rem; text-align:right; }
  .who { flex:1 1 auto; min-width:0; }
  .who b { display:block; font-size:1.12rem; font-weight:700; letter-spacing:.01em; overflow-wrap:anywhere; }
  .srcpill { display:inline-block; margin-top:.3rem; font-family:var(--mono); font-size:.6rem;
             letter-spacing:.12em; color:var(--accent3); border:1px solid var(--accent3);
             border-radius:.25rem; padding:.05rem .3rem; }
  .who small { display:block; margin-top:.15rem; color:var(--mute); font-family:var(--mono); font-size:.74rem;
               overflow:hidden; text-overflow:ellipsis; white-space:nowrap; }
  .big { text-align:right; font-family:var(--mono); flex:0 0 auto; }
  .big b { display:block; font-size:1.3rem; font-weight:400; color:var(--ink); }
  .big small { display:block; color:var(--mute); font-size:.74rem; }
  .chev { color:var(--mute); font-size:1rem; transition:transform .15s ease; flex:0 0 auto; }
  .row[open] .chev { transform:rotate(90deg); }

  /* Top three wear the app's medals. */
  .row.m1 { border-color:var(--gold); } .row.m1 .rk { color:var(--gold); font-weight:700; }
  .row.m2 { border-color:var(--silver); } .row.m2 .rk { color:var(--silver); font-weight:700; }
  .row.m3 { border-color:var(--bronze); } .row.m3 .rk { color:var(--bronze); font-weight:700; }

  .detail { padding:.1rem .85rem .8rem 3.1rem; display:grid; grid-template-columns:auto 1fr; gap:.25rem .8rem;
            font-size:.82rem; }
  .detail dt { color:var(--mute); font-family:var(--mono); font-size:.7rem; letter-spacing:.1em;
               text-transform:uppercase; align-self:center; }
  .detail dd { margin:0; font-family:var(--mono); color:var(--dim); }
  .detail dd.tier { font-weight:700; letter-spacing:.06em; }
  /* Scoped under .detail dd deliberately: a bare .t-gold is specificity (0,1,0) and loses
     to .detail dd at (0,1,1), so every tier rendered in var(--dim) while its class was
     correctly applied. Found by reading the COMPUTED colour, not the class list. */
  .detail dd.t-legend    { color:var(--t-legend); }
  .detail dd.t-ultimate  { color:var(--t-ultimate); }
  .detail dd.t-highmaster{ color:var(--t-highmaster); }
  .detail dd.t-master    { color:var(--t-master); }
  .detail dd.t-diamond   { color:var(--t-diamond); }
  .detail dd.t-platinum  { color:var(--t-platinum); }
  .detail dd.t-gold      { color:var(--gold); }
  .detail dd.t-silver    { color:var(--silver); }
  .detail dd.t-bronze    { color:var(--bronze); }
  .detail dd.t-rookie, .detail dd.t-unranked { color:var(--t-rookie); }

  /* Controls sit ABOVE the list and stay reachable while it scrolls. Collapsed below the
     board was wrong: Aaron, "that would make it completely hidden if we got 10 scores."
     Above-and-adjacent is where a filter belongs — it has to be visible at the moment the
     list it governs is, and its effect has to be seen without hunting for the control. It
     earns its place by being small, not by being far away. */
  .controls { position:sticky; top:0; z-index:2; background:var(--panel);
              padding:.25rem 1.25rem .7rem; border-bottom:1px solid var(--line); }
  .tabs { display:flex; gap:.35rem; flex-wrap:wrap; align-items:center; }
  .tabs + .tabs { padding-top:.35rem; }
  .tabs button { font:inherit; font-size:.7rem; font-weight:700; letter-spacing:.09em; color:var(--dim);
                 background:var(--well); border:1px solid var(--line); border-radius:.3rem;
                 padding:.26rem .55rem; cursor:pointer; }
  .tabs button:hover { color:var(--ink); border-color:var(--line2); }
  .tabs button:focus-visible { outline:2px solid var(--accent3); outline-offset:2px; }
  .tabs button[aria-pressed="true"] { color:var(--bg); background:var(--accent); border-color:var(--accent); }
  .minor button { font-size:.65rem; padding:.2rem .45rem; }
  .minor button[aria-pressed="true"] { color:var(--ink); background:var(--panel2); border-color:var(--accent2); }
  .sep { width:1px; height:.9rem; background:var(--line); margin:0 .25rem; }

  /* Every attempt of the run behind a row — the thing the score is recomputed from. */
  .runs { grid-column:1 / -1; display:flex; flex-wrap:wrap; gap:.3rem; margin-top:.1rem; }
  .run { font-family:var(--mono); font-size:.74rem; color:var(--dim); background:var(--panel2);
         border:1px solid var(--line); border-radius:.25rem; padding:.12rem .4rem; }
  .run.best { color:var(--accent); border-color:var(--accent); }
  .run.miss { color:var(--bad); border-color:var(--bad); }
  footer { margin-top:2rem; color:var(--mute); font-size:.85rem; }
  footer p { margin:.25rem 0; }
</style>
</head>
<body>
<main>
  <header>
    <div class="masthead">
      <h1>Share CT</h1>
      <span class="ver">Ver ` + pageVersion + `</span>
    </div>
    <span class="beta">BETA · MAY GO DOWN</span>
    <p>The reaction leaderboard for Fighter CT. Scores are opt-in from the app.</p>
  </header>
  <section class="panel" aria-labelledby="board-heading">
    <div class="panelhead">
      <h2 id="board-heading">Reaction leaderboard</h2>
      <p id="ranked" class="ranked"></p>
    </div>
    <div class="controls">
      <nav class="tabs" aria-label="Input">
        <button type="button" data-source="all" aria-pressed="true">ALL</button>
        <button type="button" data-source="touch" aria-pressed="false">TOUCH</button>
        <button type="button" data-source="pad" aria-pressed="false">PAD</button>
        <button type="button" data-source="keyboard" aria-pressed="false">KEYBOARD</button>
      </nav>
      <nav class="tabs minor" aria-label="Ranking and window">
        <button type="button" data-sort="fastest" aria-pressed="true">FASTEST</button>
        <button type="button" data-sort="score" aria-pressed="false">HIGH SCORE</button>
        <span class="sep" aria-hidden="true"></span>
        <button type="button" data-window="all" aria-pressed="true">ALL TIME</button>
        <button type="button" data-window="30d" aria-pressed="false">30 DAYS</button>
      </nav>
    </div>
    <ol id="board" class="board" hidden></ol>
    <p id="status" role="status" aria-live="polite"></p>
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

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
  // One view, not a source crossed with a sort. The three the page offers are RECENT (the
  // feed), FASTEST and HIGH SCORE (both the mixed board, differing only in what ranks it).
  // Per-input boards still exist on the API — source=touch|pad|keyboard — and can come back
  // as a filter when there are enough players to make three separate boards worth reading.
  var state = { view: 'recent' };

  // How deep each list goes. FASTEST is a TOP TEN by Aaron's ask; the other two stay
  // long. Note what actually bounds these: the server returns ONE ROW PER PLAYER
  // (bestPerPlayer partitions by player_id), so a ten-deep list shows ten PLAYERS, and
  // with six players on the board it shows six. The limit is a ceiling, never a floor.
  var LIMITS = { recent: 50, fastest: 10, score: 50 };
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
    recent: 'Every score as it arrives, newest first — all inputs mixed.',
    fastest: 'Top 10, by fastest single attempt. One row per player: their fastest run.',
    score: 'Ranked by highest score. One row per player: their best-SCORING run — so the time and tier are from that run, not their fastest. Tier is a speed rank.'
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

  // A tier is a rank name, so it wears its own colour — Aaron: "Scores should be the color
  // of the Rank. Gold score for gold, the diamond color for Diamond." The class goes on the
  // ROW and only declares a custom property (--tier); the headline number and the tier name
  // both READ that property. That is deliberate: colouring them directly meant a bare
  // .t-gold at specificity (0,1,0) losing to .detail dd at (0,1,1), which rendered every
  // tier grey while the class was correctly applied. Two rules declaring DIFFERENT
  // properties never fight, so the trap cannot come back.
  //
  // Derived from the name rather than mapped by index, so a tier the server adds later
  // degrades to the neutral colour instead of silently taking whatever sat at its position.
  function tierToken(name) {
    if (!name) return '';
    var key = String(name).toLowerCase().replace(/[^a-z]/g, '');
    var known = { legend:'legend', ultimatemaster:'ultimate', highmaster:'highmaster',
                  master:'master', diamond:'diamond', platinum:'platinum', gold:'gold',
                  silver:'silver', bronze:'bronze', rookie:'rookie', unranked:'unranked' };
    var hit = known[key];
    return hit ? 't-' + hit : '';
  }

  // One card. The headline is whichever number ranks the board, so the row leads with the
  // thing the list is sorted by; everything else is behind the disclosure.
  function card(e) {
    var byScore = state.view === 'score';
    var feed = state.view === 'recent';
    // A feed has no standings, so it has no podium either.
    var podium = !feed && e.rank <= 3;
    var cls = 'row';
    var tier = tierToken(e.tier);
    if (tier) { cls += ' ' + tier; }        // sets --tier for everything in the row
    if (podium) { cls += ' m' + e.rank; }   // sets --headline: the metal overrides the tier
    // The two rankings reward different things, so their podiums look different. FASTEST
    // gets the metal flat; HIGH SCORE — the one that pays for three consistent attempts,
    // not one lucky rep — gets it as struck foil, a slow sheen across the number.
    if (podium && byScore) { cls += ' foil'; }
    var row = el('details', cls);
    var head = el('summary');

    // A feed has no standings, so no rank number and no medals: position here is only
    // "how recently", and numbering it would read as a placing.
    head.appendChild(el('span', 'rk', feed ? '·' : String(e.rank)));

    var who = el('div', 'who');
    who.appendChild(el('b', null, e.display_name));
    who.appendChild(el('small', null,
      [when(e.created_at), e.device_label].filter(Boolean).join(' · ')));
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
    // Shown on every board now that the headline wears this colour: a coloured number with
    // no name for the colour is an unexplained decoration. On the score board the RANKED_BY
    // line says out loud that the tier is a SPEED rank, which is what makes a DIAMOND tier
    // beside the top score read as correct rather than as a bug.
    def(dl, 'tier', e.tier, ('tier ' + tier).trim());
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
      // state.source died with the per-input filter; reading it here rendered the literal
      // "No undefined scores yet." on any empty ranking.
      status.textContent = 'No scores yet.';
      return;
    }
    board.entries.forEach(function (e) { board_.appendChild(card(e)); });
    status.textContent = '';
    ranked.textContent = (state.view === 'recent' ? RANKED_BY.recent : RANKED_BY[state.view]) || '';
    board_.hidden = false;
  }

  function load() {
    status.textContent = 'Loading…';
    ranked.textContent = '';
    board_.hidden = true;
    // window=all is now fixed: Aaron, "let's remove the 30 days metric from the
    // leaderboard for now". The server still accepts the parameter, so the board can grow
    // a window control again without a server change — nothing was removed underneath.
    var url = state.view === 'recent'
      ? '/v1/recent?limit=' + LIMITS.recent
      : '/v1/board?source=all&window=all&sort=' + state.view + '&limit=' + LIMITS[state.view];
    fetch(url, { headers: { Accept: 'application/json' } })
      .then(function (r) { if (!r.ok) throw new Error(String(r.status)); return r.json(); })
      .then(function (board) {
        try {
          render(board);
        } catch (err) {
          // The server answered; WE broke. Saying "offline" here would send a reader to
          // check a Pi that is working fine.
          board_.hidden = true;
          status.textContent = 'Could not draw the board. The server answered fine.';
          if (window.console) { console.error('render failed', err); }
        }
      })
      .catch(function () {
        board_.hidden = true;
        status.textContent = 'BOARD OFFLINE — beta, may go down.';
      });
  }

  Array.prototype.forEach.call(document.querySelectorAll('[data-view]'), function (button) {
    button.addEventListener('click', function () {
      state.view = button.getAttribute('data-view');
      Array.prototype.forEach.call(document.querySelectorAll('[data-view]'), function (other) {
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
const pageVersion = ".06"

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
  .who small { display:block; margin-top:.15rem; color:var(--mute); font-family:var(--mono); font-size:.74rem;
               white-space:normal; overflow-wrap:anywhere; line-height:1.35; }
  .big { text-align:right; font-family:var(--mono); flex:0 0 auto; }
  /* The number that ranks the row wears the rank's colour. --headline (the podium metal)
     wins over --tier (the rank name's colour) and falls back to plain ink, all resolved
     in one declaration so nothing has to out-specify anything. */
  .big b { display:block; font-size:1.3rem; font-weight:400; color:var(--headline, var(--tier, var(--ink))); }
  .big small { display:block; color:var(--mute); font-size:.74rem; }
  .chev { color:var(--mute); font-size:1rem; transition:transform .15s ease; flex:0 0 auto; }
  .row[open] .chev { transform:rotate(90deg); }

  /* Top three wear the app's medals, and --headline lifts that metal onto the number the
     board is ranked by, over the tier colour every other row uses.

     Struck as FOIL, not painted flat: the number is filled with a gradient running from a
     dark edge through the metal to a highlight, clipped to the glyphs. Flat was the first
     attempt and silver came out indistinguishable from plain ink — a metal reads as metal
     because it has a dark side and a lit side, not because of its hue. */
  .row.m1 { --headline:var(--gold);   --foil-deep:#8A6410; --foil-hi:#FFF6D0; border-color:var(--gold); }
  .row.m2 { --headline:var(--silver); --foil-deep:#3C4757; --foil-hi:#F4F8FF; border-color:var(--silver); }
  .row.m3 { --headline:var(--bronze); --foil-deep:#7A4A22; --foil-hi:#FFD9A8; border-color:var(--bronze); }
  .row.m1 .rk, .row.m2 .rk, .row.m3 .rk { color:var(--headline); font-weight:700; }
  .row.m1 .big b, .row.m2 .big b, .row.m3 .big b {
    background-image:linear-gradient(100deg,
      var(--foil-deep) 0%, var(--headline) 34%, var(--foil-hi) 50%, var(--headline) 66%, var(--foil-deep) 100%);
    /* 150%, not 300%: the number only ever shows the slice of the gradient that lands on
       it, so a wide image put stops 34%-66% on the glyphs — metal and highlight, never the
       dark edge, which is exactly why flat-looking silver survived the first fix. At 150%
       the visible slice runs ~17%-83% and both shoulders reach the glyphs. */
    background-size:150% 100%; background-position:50% 0;
    -webkit-background-clip:text; background-clip:text;
    color:transparent;
  }

  /* HIGH SCORE's podium moves: the highlight travels across the number, once every five
     seconds. The score is the metric that pays for three consistent attempts rather than
     one lucky rep, so its podium is the one that gets the extra treatment — and it is what
     tells the two podiums apart at a glance. Type only: no badge, no ornament, nothing to
     decode. */
  .row.foil .big b { animation:sheen 5s linear infinite; }
  /* 150% puts the highlight past the right edge, -50% past the left: one full traverse. */
  @keyframes sheen { from { background-position:150% 0; } to { background-position:-50% 0; } }
  /* Motion is the flourish; the foil is the point. Reduced motion keeps the gradient and
     parks it mid-sweep, where the highlight sits on the number. */
  @media (prefers-reduced-motion: reduce) {
    .row.foil .big b { animation:none; background-position:50% 0; }   /* the foil stays; the sweep stops */
  }
  /* Without background-clip:text, a transparent colour would erase the score outright.
     Backticks are forbidden in here: this CSS lives in a Go raw string. */
  @supports not ((-webkit-background-clip: text) or (background-clip: text)) {
    .row.m1 .big b, .row.m2 .big b, .row.m3 .big b { background-image:none; color:var(--headline); }
  }

  .detail { padding:.1rem .85rem .8rem 3.1rem; display:grid; grid-template-columns:auto 1fr; gap:.25rem .8rem;
            font-size:.82rem; }
  .detail dt { color:var(--mute); font-family:var(--mono); font-size:.7rem; letter-spacing:.1em;
               text-transform:uppercase; align-self:center; }
  .detail dd { margin:0; font-family:var(--mono); color:var(--dim); }
  .detail dd.tier { font-weight:700; letter-spacing:.06em; color:var(--tier, var(--dim)); }

  /* One rank colour per row, declared as a VARIABLE on the row and inherited by both the
     headline number and the tier name. These rules only ever declare --tier, so they never
     compete on specificity with the rules that declare colour — which is what previously
     rendered every tier grey (a bare .t-gold at (0,1,0) losing to .detail dd at (0,1,1)). */
  .row.t-legend    { --tier:var(--t-legend); }
  .row.t-ultimate  { --tier:var(--t-ultimate); }
  .row.t-highmaster{ --tier:var(--t-highmaster); }
  .row.t-master    { --tier:var(--t-master); }
  .row.t-diamond   { --tier:var(--t-diamond); }
  .row.t-platinum  { --tier:var(--t-platinum); }
  .row.t-gold      { --tier:var(--gold); }
  .row.t-silver    { --tier:var(--silver); }
  .row.t-bronze    { --tier:var(--bronze); }
  .row.t-rookie, .row.t-unranked { --tier:var(--t-rookie); }

  /* Controls sit ABOVE the list and stay reachable while it scrolls. Collapsed below the
     board was wrong: Aaron, "that would make it completely hidden if we got 10 scores."
     Above-and-adjacent is where a filter belongs — it has to be visible at the moment the
     list it governs is, and its effect has to be seen without hunting for the control. It
     earns its place by being small, not by being far away. */
  .controls { position:sticky; top:0; z-index:2; background:var(--panel);
              padding:.25rem 1.25rem .7rem; border-bottom:1px solid var(--line); }
  .tabs { display:flex; gap:.35rem; flex-wrap:wrap; align-items:center; }
  /* An author display rule beats the UA stylesheet, so .tabs{display:flex} silently
     defeated the hidden attribute: the element reported hidden===true and still
     rendered at full height. Caught by measuring offsetParent, not by reading the
     attribute back. */
  .tabs[hidden] { display:none; }
  .tabs button { font:inherit; font-size:.7rem; font-weight:700; letter-spacing:.09em; color:var(--dim);
                 background:var(--well); border:1px solid var(--line); border-radius:.3rem;
                 padding:.26rem .55rem; cursor:pointer; }
  .tabs button:hover { color:var(--ink); border-color:var(--line2); }
  .tabs button:focus-visible { outline:2px solid var(--accent3); outline-offset:2px; }
  .tabs button[aria-pressed="true"] { color:var(--bg); background:var(--accent); border-color:var(--accent); }

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
      <nav class="tabs" aria-label="View">
        <button type="button" data-view="recent" aria-pressed="true">RECENT</button>
        <button type="button" data-view="fastest" aria-pressed="false">FASTEST</button>
        <button type="button" data-view="score" aria-pressed="false">HIGH SCORE</button>
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

package server

import (
	"crypto/sha256"
	"encoding/base64"
	"io"
	"net/http"
	"strings"
)

// boardScript is the whole client: it fetches /v1/board and builds rows with
// createElement/textContent, never innerHTML, so a display name can't become markup.
// Its sha256 is the only script the CSP admits, so the page stays one self-contained
// document with no external assets.
const boardScriptSource = `(function () {
  'use strict';
  var FRAME_MS = 1000 / 60;
  // One view, not a source crossed with a sort. The three the page offers are RECENT (the
  // feed), FASTEST and HIGH SCORE (both the mixed board, differing only in what ranks it).
  // Per-input boards still exist on the API — source=touch|pad|keyboard — and can come back
  // as a filter when there are enough players to make three separate boards worth reading.
  var state = { view: 'recent', drill: '', mode: 'endless' };
  var loadGeneration = 0;

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

  // Presentation only: the same points ladder as the app. A chronological run
  // never inherits a player's all-time standing, even if its payload carries rank.
  function drillsPresentation(entry, recent) {
    var steps = [[0,'ROOKIE'], [3,'BRONZE'], [6,'SILVER'], [10,'GOLD'],
                 [15,'PLATINUM'], [20,'DIAMOND'], [28,'MASTER'],
                 [35,'HIGH MASTER'], [42,'GRAND MASTER'], [50,'ULTIMATE MASTER']];
    var name = 'ROOKIE';
    steps.forEach(function (step) { if (entry.score >= step[0]) name = step[1]; });
    var podium = !recent && entry.score > 0 && entry.rank >= 1 && entry.rank <= 3;
    return { tier: podium ? 'LEGEND' : name, podium: podium };
  }

  function emptyBoardPresentation(drill, recent, boardSize) {
    var existingRecords = !!drill && recent && boardSize > 0;
    return {
      message: existingRecords ? 'No recent runs yet. View HIGH SCORE for existing records.' : 'No scores yet.',
      offerHighScores: existingRecords
    };
  }

  function drillMode(drill, requested) {
    var atomic = ['whiff-punish','hit-confirm','anti-air','crosscut','drive-impact'];
    return requested === 'arcade' && atomic.indexOf(drill) >= 0 ? 'arcade' : 'endless';
  }

  function arcadePresentation(entry) {
    var seconds = Math.floor(entry.elapsed_ms / 1000);
    var time = Math.floor(seconds / 60) + ':' + String(seconds % 60).padStart(2, '0') +
      '.' + String(Math.floor((entry.elapsed_ms % 1000) / 10)).padStart(2, '0');
    return { headline: entry.completed ? time : String(entry.enemies_defeated),
      label: entry.completed ? 'CLEAR TIME' : 'ENEMIES',
      progress: entry.enemies_defeated + ' / ' + entry.total_opponents + ' enemies' };
  }

  function platformInfo(value) {
    var key = typeof value === 'string' ? value.trim().toLowerCase() : '';
    switch (key) {
      case '': return { icon:'apple', label:'iPhone' }; // Aaron: all legacy scores are iPhone.
      case 'ios': return { icon:'apple', label:'iOS / iPadOS' };
      case 'android': return { icon:'android', label:'Android' };
      case 'mac': return { icon:null, label:'Mac' };
      case 'windows': return { icon:null, label:'Windows' };
      default: return { icon:null, label:'Unknown platform' };
    }
  }
  function platformGlyph(info) {
    var ns = 'http://www.w3.org/2000/svg';
    var svg = document.createElementNS(ns, 'svg');
    svg.setAttribute('class', 'platform-glyph');
    svg.setAttribute('viewBox', '0 0 24 24');
    svg.setAttribute('role', 'img');
    svg.setAttribute('aria-label', info.label);
    svg.setAttribute('fill', 'none');
    svg.setAttribute('stroke', 'currentColor');
    svg.setAttribute('stroke-width', '1.6');
    svg.setAttribute('stroke-linecap', 'round');
    svg.setAttribute('stroke-linejoin', 'round');
    function shape(tag, attrs) {
      var node = document.createElementNS(ns, tag);
      Object.keys(attrs).forEach(function (key) { node.setAttribute(key, attrs[key]); });
      svg.appendChild(node);
    }
    if (info.icon === 'apple') {
      shape("path", {"d": "M15.8 6.3c-1.3-.1-2.4.8-3.7.8-1.1 0-2.1-.8-3.4-.7C5.7 6.6 4.2 9 4.5 12c.3 3.5 2.4 7.6 4.6 7.7 1.1.1 1.7-.7 3-.7s1.8.7 3 .6c1.6-.1 3.1-2.4 3.9-4.5-2.8-1.4-3.2-4.9-.6-6.7-.8-1.3-1.6-2-2.6-2.1Z"});
      shape("path", {"d": "M12.2 5.5c-.2-2 1.3-3.6 3.5-3.9.2 2-1.4 3.7-3.5 3.9Z"});
    }
    else if (info.icon === 'android') {
      shape("path", {"d": "M4 15a8 8 0 0 1 16 0v4H4zM7 7 5 3M17 7l2-4"});
      shape("circle", {"cx": "8.5", "cy": "13", "r": ".8", "fill": "currentColor", "stroke": "none"});
      shape("circle", {"cx": "15.5", "cy": "13", "r": ".8", "fill": "currentColor", "stroke": "none"});
    }
    return svg;
  }

  // Board avatars: the same headshot the app draws beside each name. A row carries only a
  // short descriptor of ids (fighter, outfit, colour, gear, bonus look); the art is one
  // static file of vector paths baked from the app's fighter art, fetched once and cached.
  // Never an image, and nothing a player typed reaches it: ids select from that file.
  var AVATARS_URL = '__AVATARS_URL__';
  var avatarArt = null;          // the baked file, once loaded
  var avatarWaiting = [];        // tiles drawn before it arrived
  // Standard in its COLOR 1 (Player One Cyan), Outfit 1, no gear: Aaron chose blue Standard as
  // the default (controller-tester-fgc, 2026-10-20). TestDefaultLookIsStandardColorOne ties the
  // fill to the baked palette.
  var DEFAULT_LOOK = { body: 'puppet', outfit: '1', gear: null, fill: [100, 210, 255], outline: null, hat: false };
  var EX01 = { id: 'bonus-ex01', fill: [100, 210, 255], outline: [255, 201, 60] };
  var AVATAR_V1 = /^1:[a-z0-9-]{1,32}:[1-9]:[0-9](:[a-z0-9-]{1,32}(,[a-z0-9-]{1,32}){0,3})?$/;
  var AVATAR_V2 = /^2:[a-z0-9-]{1,32}:[1-9]:[0-9]:([a-z0-9-]{1,32}(,[a-z0-9-]{1,32}){0,3})?:[a-z0-9-]{1,32}$/;

  var AVATAR_V3 = /^3:[a-z0-9-]{1,32}:[1-9]:[0-9]:[a-z0-9-]{1,32}(,[a-z0-9-]{1,32}){0,3}:[a-z0-9-]{0,32}:[a-z0-9-]{1,3}(,[a-z0-9-]{1,3}){0,3}$/;

  // The head markup an item draws on one outfit's art (src = the fighter for Outfit 1, or
  // one of its outfits), or null when that art has none.
  function catalogValue(map, id) {
    return map && Object.prototype.hasOwnProperty.call(map, id) ? map[id] : null;
  }
  function gearHead(src, id) {
    var paths = catalogValue(src.gear, id);
    if (Array.isArray(paths) && paths.length) return paths;
    if (id === 'pirate-hat' && Array.isArray(src.hat) && src.hat.length) return src.hat;
    return null;
  }

  // Recipes only select our static catalog; unknown future values keep their known body/outfit.
  function avatarLook(raw, art) {
    if (typeof raw !== 'string' || raw.length > 64 || !(AVATAR_V1.test(raw) || AVATAR_V2.test(raw) || AVATAR_V3.test(raw))) return DEFAULT_LOOK;
    var f = raw.split(':');
    if (f[0] === '3' && f[4].split(',').length !== f[6].split(',').length) return DEFAULT_LOOK;
    var fighter = art && catalogValue(art.fighters, f[1]);
    if (!fighter) return DEFAULT_LOOK;
    var bonus = Number(f[0]) >= 2 && f[5] === EX01.id && (fighter.bonus || []).indexOf(EX01.id) >= 0;
    var outfit = !bonus && f[2] !== '1' && catalogValue(fighter.outfits, f[2]) ? f[2] : '1';
    var src = outfit === '1' ? fighter : fighter.outfits[outfit];
    var ids = f[4] ? f[4].split(',') : [];
    var gear = null, back = null;
    ids.forEach(function (id) {
      if (!gear && gearHead(src, id)) gear = id;
      if (!back && Array.isArray(catalogValue(fighter.back, id))) back = id;
    });
    var look = { body: f[1], outfit: outfit, gear: gear, fill: src.palette[Number(f[3])] || src.palette[0], outline: null, hat: gear === 'pirate-hat' };
    if (bonus) { look.fill = EX01.fill; look.outline = EX01.outline; }
    var recipes = art.recipes || {};
    var skin = catalogValue(catalogValue(recipes.bodySkins, f[1]), f[5]) || catalogValue(recipes.skins, f[5]);
    var reserve = catalogValue(catalogValue(recipes.reserves, f[1]), f[5]);
    if (skin) { look.fill = skin.line; look.outline = skin.edge || null; if (skin.fill) look.fillColor = skin.fill; }
    else if (reserve) look.fill = reserve;
    if (back) look.back = back;
    if (f[0] === '3') {
      var variants = f[6].split(',');
      ids.forEach(function (id, index) {
        var allowed = catalogValue(recipes.allowedGear, id);
        var paint = catalogValue(recipes.gear, variants[index]);
        if (!Array.isArray(allowed) || allowed.indexOf(variants[index]) < 0 || !paint || !paint.line) return;
        // This code reward already has its authored red/blue vector paint, including the face mask.
        if (id === gear && id !== 'madhouse-hat' && catalogValue(src.tint_masks, id)) look.gearPaint = paint;
        if (id === back) look.backPaint = paint;
      });
    }
    return look;
  }

  var SVG_NS = 'http://www.w3.org/2000/svg';
  var avatarDefs = null;
  // One <symbol> per fighter, outfit and gear, built once from the baked markup.
  // The markup is our own static file, never player data.
  function avatarSymbol(body, outfit, gear, back, painted) {
    var id = 'av-' + body + (outfit === '1' ? '' : '-o' + outfit) + (gear ? '-' + gear : '') + (painted ? '-tint' : '') + (back ? '-' + back : '');
    if (document.getElementById(id)) return id;
    var f = avatarArt.fighters[body];
    var src = outfit === '1' ? f : f.outfits[outfit];
    var head = ((painted && catalogValue(src.neutral_gear, gear)) || gearHead(src, gear) || src.head).slice();
    if (painted && catalogValue(src.tint_masks, gear)) {
      head = head.map(function (neutral, i) {
        var mask = '{id}variant' + i;
        var colored = neutral.split('{id}').join('{id}paint' + i + '-').split('var(--m').join('var(--gm').split('var(--o').join('var(--ge');
        return neutral + '<clipPath id="' + mask + '"><path d="' + src.tint_masks[gear][i] + '" fill-rule="evenodd"/></clipPath>' +
          '<g style="filter:var(--gear-glow,none)"><g clip-path="url(#' + mask + ')">' + colored + '</g></g>';
      });
    }
    var markup = ((catalogValue(f.back, back) || []).join('') + src.parts.map(function (part) { return part === '' ? head.shift() : part; }).join(''))
      .split('{id}').join(id + '-');
    var doc = new DOMParser().parseFromString('<svg xmlns="' + SVG_NS + '">' + markup + '</svg>', 'image/svg+xml');
    var symbol = document.createElementNS(SVG_NS, 'symbol');
    symbol.setAttribute('id', id);
    symbol.setAttribute('viewBox', '0 0 ' + f.box[2] + ' ' + f.box[2]);
    Array.prototype.slice.call(doc.documentElement.childNodes).forEach(function (n) {
      symbol.appendChild(document.importNode(n, true));
    });
    if (!avatarDefs) {
      avatarDefs = document.createElementNS(SVG_NS, 'svg');
      avatarDefs.setAttribute('aria-hidden', 'true');
      avatarDefs.setAttribute('class', 'av-defs');
      document.body.appendChild(avatarDefs);
    }
    avatarDefs.appendChild(symbol);
    return id;
  }

  function rgb(c, k) {
    return 'rgb(' + Math.round(c[0] * k) + ',' + Math.round(c[1] * k) + ',' + Math.round(c[2] * k) + ')';
  }

  // Native paper-ink mapping: fill at the art's #082D2B green, line at full green.
  function paintRGB(line, fill, level) {
    if (!fill) return rgb(line, level);
    return 'rgb(' + line.map(function (v, i) {
      return Math.max(0, Math.min(255, Math.round(fill[i] + (v - fill[i]) * (level - 45/255) / (1 - 45/255))));
    }).join(',') + ')';
  }

  // Tints exactly as the app does: each path's colour is its green level times the player
  // colour; draw layers 0 and 1 (key and line) take the outline colour when a two-tone
  // bonus look has one.
  function paintAvatar(tile, raw) {
    var look = avatarLook(raw, avatarArt);
    tile.style.setProperty('--c', rgb(look.fill, 1));
    if (!avatarArt || !avatarArt.fighters[look.body]) return;
    var f = avatarArt.fighters[look.body];
    var svg = document.createElementNS(SVG_NS, 'svg');
    svg.setAttribute('viewBox', '0 0 ' + f.box[2] + ' ' + f.box[2]);
    svg.setAttribute('aria-hidden', 'true');
    f.levels.forEach(function (level, i) {
      svg.style.setProperty('--m' + i, paintRGB(look.fill, look.fillColor, level));
      svg.style.setProperty('--o' + i, rgb(look.outline || look.fill, level));
      var gp = look.gearPaint;
      svg.style.setProperty('--gm' + i, paintRGB(gp ? gp.line : look.fill, gp ? gp.fill : look.fillColor, level));
      svg.style.setProperty('--ge' + i, rgb(gp ? gp.edge || gp.line : look.outline || look.fill, level));
      var bp = look.backPaint;
      svg.style.setProperty('--bm' + i, paintRGB(bp ? bp.line : look.fill, bp ? bp.fill : look.fillColor, level));
      svg.style.setProperty('--bo' + i, rgb(bp ? bp.edge || bp.line : look.outline || look.fill, level));
    });
    if (look.gearPaint && look.gearPaint.glow) svg.style.setProperty('--gear-glow', 'drop-shadow(0 0 2.5px ' + rgb(look.gearPaint.edge || look.gearPaint.line, 1) + ')');
    var use = document.createElementNS(SVG_NS, 'use');
    use.setAttribute('href', '#' + avatarSymbol(look.body, look.outfit || '1', look.gear, look.back, !!look.gearPaint));
    svg.appendChild(use);
    tile.textContent = '';
    tile.appendChild(svg);
  }

  function avatarTile(raw, large) {
    var tile = el('span', large ? 'av lg' : 'av');
    tile.setAttribute('aria-hidden', 'true');
    if (avatarArt) { paintAvatar(tile, raw); }
    else { tile.style.setProperty('--c', rgb(DEFAULT_LOOK.fill, 1)); avatarWaiting.push([tile, raw]); }
    return tile;
  }

  function loadAvatarArt() {
    fetch(AVATARS_URL).then(function (r) { if (!r.ok) throw new Error(String(r.status)); return r.json(); })
      .then(function (art) {
        if (!art || !art.fighters) return;
        avatarArt = art;
        avatarWaiting.forEach(function (w) { try { paintAvatar(w[0], w[1]); } catch (e) {} });
        avatarWaiting = [];
      })
      .catch(function () { avatarWaiting = []; });   // the tiles stay as plain tinted squares
  }

  // One card. The headline is whichever number ranks the board, so the row leads with the
  // thing the list is sorted by; everything else is behind the disclosure.
  function card(e) {
    var endless = !!state.drill;
    var arcade = endless && drillMode(state.drill, state.mode) === 'arcade';
    var arcadeInfo = arcade ? arcadePresentation(e) : null;
    var byScore = endless || state.view === 'score';
    var feed = state.view === 'recent';
    // A feed has no standings, so it has no podium either.
    var drillRank = arcade ? {tier: e.completed ? 'ARCADE CLEAR' : 'ARCADE', podium: !feed && e.rank >= 1 && e.rank <= 3} : endless ? drillsPresentation(e, feed) : null;
    var podium = endless ? drillRank.podium : !feed && e.rank <= 3;
    var cls = 'row';
    if (endless) { cls += ' drill'; }
    var tierName = endless ? drillRank.tier : e.tier;
    var tier = endless && tierName === 'GRAND MASTER' ? 't-grandmaster' : tierToken(tierName);
    if (tier) { cls += ' ' + tier; }        // sets --tier for everything in the row
    if (podium) { cls += ' m' + e.rank; }   // sets --headline: the metal overrides the tier
    // The two rankings reward different things, so their podiums look different. FASTEST
    // gets the metal flat; HIGH SCORE — the one that pays for three consistent attempts,
    // not one lucky rep — gets it as struck foil, a slow sheen across the number.
    if (podium && byScore) { cls += ' foil'; }
    if (feed) { cls += ' feed'; }
    var row = el('details', cls);
    var head = el('summary');

    // A feed has no standings, so no rank number and no medals: position here is only
    // "how recently", and numbering it would read as a placing.
    head.appendChild(el('span', 'rk', feed ? '·' : String(e.rank)));
    // #1 on a ranked board gets the larger headshot, as in the app (52 vs 40).
    head.appendChild(avatarTile(e.avatar, !feed && e.rank === 1));

    var who = el('div', 'who');
    var identity = el('div', 'player-identity');
    identity.appendChild(el('b', null, e.display_name));
    var playerID = el('span', 'player-id');
    if (e.player_short) playerID.appendChild(el('span', null, '· ' + e.player_short));
    var origin = platformInfo(e.platform);
    if (origin.icon) playerID.appendChild(platformGlyph(origin));
    identity.appendChild(playerID);
    who.appendChild(identity);
    var platform = platformInfo(e.platform);
    var subline = el('small', 'device-subline');
    var date = when(e.created_at);
    if (date) subline.appendChild(el('span', 'entry-date', date));
    var device = el('span', 'device-identity');
    // The separator rides with the device, so a wrap never leaves it dangling after the date.
    if (date && e.device_label) device.appendChild(el('span', 'meta-sep', '· '));
    device.appendChild(el('span', 'device-label', e.device_label || ''));
    subline.appendChild(device);
    head.appendChild(who);

    var big = el('div', 'big');
    big.appendChild(el('b', null, arcade ? arcadeInfo.headline : byScore ? String(e.score) : frames(e.best_ms)));
    big.appendChild(el('small', null, arcade ? arcadeInfo.label : endless ? 'POINTS' : (byScore ? frames(e.best_ms) : 'avg ' + frames(e.avg_ms))));
    head.appendChild(big);

    head.appendChild(el('span', 'chev', '›'));
    head.appendChild(subline);
    row.appendChild(head);

    var dl = el('dl', 'detail');
    if (!endless) {
      def(dl, 'best', frames(e.best_ms) + ' · ' + Math.round(e.best_ms) + ' ms');
      def(dl, 'avg', frames(e.avg_ms) + ' · ' + Math.round(e.avg_ms) + ' ms');
    }
    if (typeof e.accuracy === 'number') def(dl, 'landed', Math.round(e.accuracy * 100) + '%');
    def(dl, 'score', String(e.score));
    if (arcade) {
      def(dl, 'run', e.completed ? 'ARCADE CLEAR' : 'GAME OVER');
      def(dl, 'progress', arcadeInfo.progress);
      def(dl, 'time', arcadePresentation(Object.assign({}, e, {completed:true})).headline);
      def(dl, 'hits', String(e.hits));
      def(dl, 'perfect opponents', String(e.perfect_opponents));
      def(dl, 'consistency bonus', '+' + e.consistency_bonus);
      def(dl, 'time bonus', '+' + e.time_bonus);
    }
    // The tier is a SPEED rank (TierName is a pure function of best_ms), so on a score
    // board it tracks the winning run's time and would read as a lower rank beside a
    // higher score. Shown only where it agrees with what ranks the list.
    // Shown on every board now that the headline wears this colour: a coloured number with
    // no name for the colour is an unexplained decoration. On the score board the RANKED_BY
    // line says out loud that the tier is a SPEED rank, which is what makes a DIAMOND tier
    // beside the top score read as correct rather than as a bug.
    def(dl, 'tier', tierName, ('tier ' + tier).trim());
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
    def(dl, 'set', when(e.created_at));
    def(dl, 'player', e.player_short);
    row.appendChild(dl);
    return row;
  }

  function render(board) {
    var activity = document.getElementById('endless-drill');
    var title = activity.options[activity.selectedIndex].text;
    document.getElementById('board-heading').textContent = title.toUpperCase() + (state.drill ? ' · ' + drillMode(state.drill, state.mode).toUpperCase() : '') + ' LEADERBOARD';
    board_.textContent = '';
    if (!board.entries.length) {
      board_.hidden = true;
      ranked.textContent = '';
      // state.source died with the per-input filter; reading it here rendered the literal
      // "No undefined scores yet." on any empty ranking.
      var empty = emptyBoardPresentation(state.drill, state.view === 'recent', board.board_size);
      status.textContent = empty.message;
      if (empty.offerHighScores) {
        var highScores = el('button', 'view-high-scores', 'VIEW HIGH SCORES');
        highScores.type = 'button';
        highScores.addEventListener('click', function () {
          // Reuse the native view control, keeping the selected drill unchanged.
          document.querySelector('[data-view="score"]').click();
        });
        status.appendChild(highScores);
      }
      return;
    }
    board.entries.forEach(function (e) { board_.appendChild(card(e)); });
    status.textContent = '';
    ranked.textContent = state.drill && drillMode(state.drill, state.mode) === 'arcade' ? (state.view === 'recent' ? 'Newly shared Arcade attempts, newest first.' : 'Clears first, fastest clear time wins. Partial runs rank by enemies defeated, then points, then time. Ladder length is shown in each row.') : state.drill ? (state.view === 'recent' ? 'Every newly shared completed drill run, newest first.' : 'Highest active-reaction count per player. Three lives, 100% speed. Correct non-actions earn no points.') : ((state.view === 'recent' ? RANKED_BY.recent : RANKED_BY[state.view]) || '');
    board_.hidden = false;
  }

  function load() {
    status.textContent = 'Loading…';
    ranked.textContent = '';
    board_.hidden = true;
    // window=all is now fixed: Aaron, "let's remove the 30 days metric from the
    // leaderboard for now". The server still accepts the parameter, so the board can grow
    // a window control again without a server change — nothing was removed underneath.
    var generation = ++loadGeneration;
    var url = state.drill ? '/v1/drills/board?drill_id=' + state.drill + '&mode=' + drillMode(state.drill, state.mode) + '&limit=50' + (state.view === 'recent' ? '&sort=recent' : '') : state.view === 'recent'
      ? '/v1/recent?limit=' + LIMITS.recent
      : '/v1/board?source=all&window=all&sort=' + state.view + '&limit=' + LIMITS[state.view];
    fetch(url, { headers: { Accept: 'application/json' } })
      .then(function (r) { if (!r.ok) throw new Error(String(r.status)); return r.json(); })
      .then(function (board) {
        if (generation !== loadGeneration) return;
        if (state.drill && drillMode(state.drill, state.mode) === 'arcade' && board.mode !== 'arcade') throw new Error('Arcade board unavailable');
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
        if (generation !== loadGeneration) return;
        board_.hidden = true;
        status.textContent = 'BOARD OFFLINE — beta, may go down.';
      });
  }

  Array.prototype.forEach.call(document.querySelectorAll('[data-view]'), function (button) {
    button.addEventListener('click', function () {
      state.view = button.getAttribute('data-view');
      if (state.drill && state.view === 'fastest') state.view = 'score';
      Array.prototype.forEach.call(document.querySelectorAll('[data-view]'), function (other) {
        other.setAttribute('aria-pressed', String(other.getAttribute('data-view') === state.view));
      });
      load();
    });
  });

  document.getElementById('endless-drill').addEventListener('change', function () {
    state.drill = this.value;
    state.mode = drillMode(state.drill, state.mode);
    document.getElementById('drill-mode').value = state.mode;
    document.getElementById('drill-mode-choice').hidden = !state.drill || state.drill === 'mental-stack';
    if (state.drill && state.view === 'fastest') state.view = 'score';
    document.querySelector('[data-view="fastest"]').disabled = !!state.drill;
    Array.prototype.forEach.call(document.querySelectorAll('[data-view]'), function (button) {
      button.setAttribute('aria-pressed', String(button.getAttribute('data-view') === state.view));
    });
    load();
  });
  document.getElementById('drill-mode').addEventListener('change', function () {
    state.mode = drillMode(state.drill, this.value);
    load();
  });
  loadAvatarArt();
  load();
  setInterval(function () { if (state.view === 'recent' && !document.hidden) load(); }, 15000);
})();
`

// boardScript is boardScriptSource with the avatar file's content-addressed URL filled in,
// so a new bake is a new URL and the old one can be cached forever.
var boardScript = strings.Replace(boardScriptSource, "__AVATARS_URL__", avatarsURL, 1)

// scriptHash is the CSP hash source for boardScript.
var scriptHash = func() string {
	sum := sha256.Sum256([]byte(boardScript))
	return base64.StdEncoding.EncodeToString(sum[:])
}()

// pageVersion is the page's own revision, shown at the top right and bumped on EVERY merge
// to main, visible change or not, API-only changes included (Aaron 2026-09-11: "at a Ver
// number to the far right (start with Ver .01) for each revision"; 2026-10-12: bump on
// every merge). Deliberately NOT the build sha: one merge is one revision, however many
// pushes it took.
const pageVersion = ".47"

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
    --cell:#2C1F42;
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
  .view-high-scores { display:block; margin:.75rem 0 0; padding:.5rem .75rem; font:inherit;
    min-height:44px; color:var(--ink); background:var(--panel); border:1px solid var(--line2); border-radius:4px; cursor:pointer; }
  .view-high-scores:focus-visible { outline:2px solid var(--accent3); outline-offset:2px; }

  /* The board is a list of cards, not a table. Aaron, on the app's own score log:
     "have scores present more like the app where you can click down into them to view
     more and doesn't make the leaderboard selection take up so much of the app." */
  .board { list-style:none; margin:0; padding:0 .75rem .75rem; display:flex; flex-direction:column; gap:.5rem; }
  .row { border:1px solid var(--line); border-radius:.6rem; background:var(--well); overflow:hidden; }
  /* The date and device sit directly under the name, in the identity column, while the score
     spans both lines on the right. Before, the metadata started below the two-line score and
     spread across under it, leaving a tall empty band in every row on a phone (Aaron, 2026-10-20). */
  .row > summary { display:grid; grid-template-columns:1.4rem auto minmax(0,1fr) auto .5rem;
                   grid-template-areas:"rank avatar identity score disclosure" "rank avatar metadata score disclosure";
                   grid-template-rows:auto 1fr;
                   align-items:start; column-gap:.65rem; row-gap:.15rem; padding:.7rem .85rem; cursor:pointer; list-style:none; }
  /* A feed has no standings, so it has no rank column at all (it used to print a lone dot). */
  .row.feed > summary { grid-template-columns:auto minmax(0,1fr) auto .5rem;
                        grid-template-areas:"avatar identity score disclosure" "avatar metadata score disclosure"; }
  .row.feed .rk { display:none; }
  .row > summary::-webkit-details-marker { display:none; }
  .row > summary:focus-visible { outline:2px solid var(--accent3); outline-offset:-2px; }
  .row:hover { border-color:var(--line2); }
  .rk { grid-area:rank; font-family:var(--mono); font-size:1rem; color:var(--mute); min-width:1.4rem; text-align:right; }
  .who { grid-area:identity; min-width:0; }
  /* The app's headshot tile (CTBoardAvatarView): the cell, then the player colour at 26%
     fading to clear by 0.573 x the side from (50%, 38%), then a 1 px inset ring of the line
     colour with the colour at 45% over it. Radius 10 at 40 px, 12 at 52 px. No other glow. */
  .av { grid-area:avatar; align-self:start; display:block; width:40px; height:40px; border-radius:10px; overflow:hidden;
        --av-reach:22.92px;
        background-color:var(--cell);
        background-image:radial-gradient(circle var(--av-reach) at 50% 38%, color-mix(in srgb, var(--c) 26%, transparent), transparent);
        box-shadow:inset 0 0 0 1px color-mix(in srgb, var(--c) 45%, transparent), inset 0 0 0 1px var(--line); }
  .av.lg { width:52px; height:52px; border-radius:12px; --av-reach:29.8px; }
  .av svg { display:block; width:100%; height:100%; }
  .av-defs { position:absolute; width:0; height:0; overflow:hidden; }
  .player-identity { display:flex; align-items:center; gap:5px; flex-wrap:wrap; }
  .player-id { display:inline-flex; align-items:center; gap:4px; font: .65rem var(--mono); color:var(--mute); }
  .player-id .platform-glyph { width:10px; height:10px; flex-basis:10px; }
  .who b { display:block; font-size:1.12rem; font-weight:700; letter-spacing:.01em; overflow-wrap:anywhere; }
  .big { grid-area:score; white-space:nowrap; text-align:right; font-family:var(--mono); flex:0 0 auto; }
  /* The number that ranks the row wears the rank's colour. --headline (the podium metal)
     wins over --tier (the rank name's colour) and falls back to plain ink, all resolved
     in one declaration so nothing has to out-specify anything. */
  .big b { display:block; font-size:1.3rem; font-weight:400; color:var(--headline, var(--tier, var(--ink))); }
  .big small { display:block; color:var(--mute); font-size:.74rem; }
  .chev { grid-area:disclosure; color:var(--mute); font-size:1rem; transition:transform .15s ease; flex:0 0 auto; }
  .platform-glyph { width:13px; height:13px; flex:0 0 13px; color:var(--dim); }
  /* Metadata spans the identity and score columns. A date cannot squeeze the
     controller label into a thin side column, and ordinary words stay intact. */
  .device-subline { grid-area:metadata; min-width:0; display:flex; flex-wrap:wrap; align-items:baseline;
                    column-gap:.4rem; row-gap:.05rem; color:var(--mute);
                    font-family:var(--mono); font-size:.74rem; line-height:1.4; }
  .device-identity, .device-label, .entry-date { min-width:0; white-space:normal; overflow-wrap:anywhere; word-break:normal; }
  .device-identity { display:block; }
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

  .detail { padding:.1rem .85rem .8rem 3.1rem; display:grid; grid-template-columns:auto minmax(0,1fr); gap:.25rem .8rem;
            font-size:.82rem; }
  .detail dt { color:var(--mute); font-family:var(--mono); font-size:.7rem; letter-spacing:.1em;
               text-transform:uppercase; align-self:center; }
  .detail dd { margin:0; min-width:0; overflow-wrap:anywhere; font-family:var(--mono); color:var(--dim); }
  .detail dd.tier { font-weight:700; letter-spacing:.06em; color:var(--tier, var(--dim)); }

  /* One rank colour per row, declared as a VARIABLE on the row and inherited by both the
     headline number and the tier name. These rules only ever declare --tier, so they never
     compete on specificity with the rules that declare colour — which is what previously
     rendered every tier grey (a bare .t-gold at (0,1,0) losing to .detail dd at (0,1,1)). */
  .row.t-legend    { --tier:var(--t-legend); }
  .row.t-ultimate  { --tier:var(--t-ultimate); }
  .row.t-grandmaster { --tier:#FFD23A; } /* app GRAND metal midtone */
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
  .controls { position:sticky; display:flex; flex-wrap:wrap; align-items:flex-end; gap:.75rem 1rem; top:0; z-index:2; background:var(--panel);
              padding:.25rem 1.25rem .7rem; border-bottom:1px solid var(--line); }
  .tabs { display:flex; gap:.35rem; flex-wrap:wrap; align-items:center; }
  /* An author display rule beats the UA stylesheet, so .tabs{display:flex} silently
     defeated the hidden attribute: the element reported hidden===true and still
     rendered at full height. Caught by measuring offsetParent, not by reading the
     attribute back. */
  .tabs[hidden] { display:none; }
  .leaderboard-choice[hidden] { display:none; }
  .tabs button { font:inherit; font-size:.7rem; font-weight:700; letter-spacing:.09em; color:var(--dim);
                 background:var(--well); border:1px solid var(--line); border-radius:.3rem;
                 padding:.26rem .55rem; cursor:pointer; }
  .leaderboard-choice { display:grid; gap:.3rem; width:min(100%,20rem); min-width:0; }
  .leaderboard-choice label { font-size:.7rem; font-weight:700; letter-spacing:.09em; color:var(--dim); }
  .leaderboard-choice select { background:var(--panel); color:var(--ink); border:1px solid var(--line2); border-radius:4px; padding:.5rem .75rem; font:inherit; font-size:1rem; min-height:44px; width:100%; min-width:0; max-width:100%; box-sizing:border-box; }
  .tabs button:hover { color:var(--ink); border-color:var(--line2); }
  .tabs button:focus-visible { outline:2px solid var(--accent3); outline-offset:2px; }
  .tabs button[aria-pressed="true"] { color:var(--bg); background:var(--accent); border-color:var(--accent); }

  @media (max-width:600px) {
    .controls { display:block; }
    .leaderboard-choice { width:100%; }
    .tabs { display:grid; grid-template-columns:repeat(3,minmax(0,1fr)); align-items:stretch; margin-top:.75rem; gap:.4rem; }
    .tabs button { min-height:44px; font-size:.75rem; letter-spacing:.03em; padding:.5rem .3rem; }
    .who b { font-size:1rem; }
    .row > summary { column-gap:.55rem; padding:.6rem .7rem; }
    .big b { font-size:1.15rem; }
  }

  /* Drills' headline follows its earned tier; the standing numeral keeps the
     existing podium medal. Reaction's medal/foil rules above are unchanged. */
  .row.drill .big b { background-image:none; color:var(--tier, var(--ink)); animation:none; }

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
    <p>Reaction Test and Drills leaderboards for Fighter CT. Scores are opt-in from the app.</p>
  </header>
  <section class="panel" aria-labelledby="board-heading">
    <div class="panelhead">
      <h2 id="board-heading">Reaction Test leaderboard</h2>
      <p id="ranked" class="ranked"></p>
    </div>
    <div class="controls">
      <div class="leaderboard-choice">
        <label for="endless-drill">LEADERBOARD</label>
        <select id="endless-drill" aria-label="Choose leaderboard">
          <option value="" selected>Reaction Test</option>
          <option value="whiff-punish">Whiff Punish</option>
          <option value="hit-confirm">Hit Confirm</option>
          <option value="anti-air">Anti-Air</option>
          <option value="crosscut">Crosscut</option>
          <option value="drive-impact">Impact</option>
          <option value="mental-stack">Mental Stack</option>
        </select>
      </div>
      <div class="leaderboard-choice" id="drill-mode-choice" hidden>
        <label for="drill-mode">MODE</label>
        <select id="drill-mode" aria-label="Choose drill mode">
          <option value="endless" selected>Endless</option>
          <option value="arcade">Arcade</option>
        </select>
      </div>
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
    <p>Reaction Test score rewards consistency, not just one good rep: the biggest bonuses go to a trial whose
       <strong>three attempts are all under 13 frames</strong> (216.7&nbsp;ms). A slower best can outscore a faster one.</p>
    <p>Reaction scores are recomputed on the server from attempt timings. Endless scores count successful active reactions; correct non-actions earn no points. Arcade ranks clears by time and partial attempts by whole enemies defeated, with consistency and clear-time bonus points. No accounts, no tracking; erase your data from the app.</p>
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

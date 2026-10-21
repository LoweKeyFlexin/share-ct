const {test} = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const vm = require('node:vm');
const source = fs.readFileSync(__dirname + '/page.go', 'utf8');
const start = source.indexOf('  function drillsPresentation(');
const end = source.indexOf('  function platformInfo(', start);
const context = vm.createContext({});
vm.runInContext(source.slice(start, end), context);
const present = (score, rank = 10, recent = false) => context.drillsPresentation({score, rank}, recent);

test('every points threshold and its lower boundary match the Drills ladder', () => {
  const steps = [[0,'ROOKIE'],[3,'BRONZE'],[6,'SILVER'],[10,'GOLD'],[15,'PLATINUM'],
    [20,'DIAMOND'],[28,'MASTER'],[35,'HIGH MASTER'],[42,'GRAND MASTER'],[50,'ULTIMATE MASTER']];
  steps.forEach(([points, name], index) => {
    assert.equal(present(points).tier, name);
    if (index) assert.equal(present(points - 1).tier, steps[index - 1][1]);
  });
  assert.equal(present(900).tier, 'ULTIMATE MASTER');
});
test('positive actual Top podium has Legend; zero and missing standing do not', () => {
  [1,2,3].forEach(rank => {
    assert.equal(present(1, rank).tier, 'LEGEND');
    assert.equal(present(1, rank).podium, true);
    assert.equal(present(0, rank).tier, 'ROOKIE');
    assert.equal(present(0, rank).podium, false);
  });
  [0,4].forEach(rank => {
    assert.equal(present(6, rank).tier, 'SILVER');
    assert.equal(present(6, rank).podium, false);
  });
  assert.equal(context.drillsPresentation({score: 6}, false).podium, false);
});
test('Recent uses this run points regardless of a supplied lifetime rank', () => {
  [0,1,2,3,50].forEach(rank => {
    assert.equal(present(0, rank, true).tier, 'ROOKIE');
    assert.equal(present(42, rank, true).tier, 'GRAND MASTER');
    assert.equal(present(50, rank, true).podium, false);
  });
});

test('empty Drills Recent offers existing Top records without changing other empty states', () => {
  const existing = context.emptyBoardPresentation('whiff-punish', true, 1);
  assert.equal(existing.offerHighScores, true);
  assert.match(existing.message, /No recent runs/);
  assert.match(existing.message, /HIGH SCORE/);
  for (const args of [['',true,1],['',false,1],['whiff-punish',false,1],['whiff-punish',true,0],['whiff-punish',true,undefined]]) {
    const empty = context.emptyBoardPresentation(...args);
    assert.equal(empty.offerHighScores, false);
    assert.equal(empty.message, 'No scores yet.');
  }
});

// Run the actual card builder against a tiny DOM surface. Layout geometry is
// checked in Chrome; this pins who owns identity, score and full metadata.
test('card metadata owns readable sibling lines outside the identity and score columns', () => {
  class Element {
    constructor(tag) { this.tag = tag; this.children = []; this.className = ''; this.text = ''; this.style = {setProperty() {}}; }
    set textContent(value) { this.text = String(value); this.children = []; }
    get textContent() { return this.text + this.children.map(child => child.textContent).join(''); }
    appendChild(child) { this.children.push(child); child.parent = this; return child; }
    setAttribute(key, value) { this[key] = value; }
  }
  const document = {createElement: tag => new Element(tag), createElementNS: (_,tag) => new Element(tag)};
  const ctx = vm.createContext({document, FRAME_MS: 1000/60, state:{drill:'',view:'recent'}});
  vm.runInContext(source.slice(source.indexOf('  function el('),source.indexOf('  function render(')),ctx);
  const find = (node, cls) => node.className.split(' ').includes(cls) ? node : node.children.map(child=>find(child,cls)).find(Boolean);
  for (const label of ['DualSense Edge Wireless Controller','X'.repeat(180)]) {
    const card = ctx.card({rank:1,display_name:'ABCDEFGHIJKLMNOP',player_short:'AB12',source:'pad',platform:'ios',
      device_label:label,created_at:'2026-10-14T22:54:49Z',score:2100,best_ms:200,avg_ms:210,tier:'MASTER'});
    const summary = card.children[0], metadata = find(card,'device-subline');
    assert.equal(metadata.parent,summary);
    assert.equal(find(card,'who').parent,summary);
    assert.equal(find(card,'big').parent,summary);
    assert.equal(find(card,'av').parent,summary); // the headshot tile sits in the summary row
    assert.equal(find(card,'device-identity').parent,metadata);
    assert.equal(find(card,'entry-date').parent,metadata);
    assert.equal(find(card,'device-label').textContent,label);
    assert.match(find(card,'player-identity').textContent,/ABCDEFGHIJKLMNOP/);
    assert.equal(find(card,'big').children[0].textContent,'12.0f');
  }
});


test('Arcade mode is available only for the five atomic drills', () => {
  for (const drill of ['whiff-punish','hit-confirm','anti-air','crosscut','drive-impact']) {
    assert.equal(context.drillMode(drill, 'arcade'), 'arcade');
    assert.equal(context.drillMode(drill, 'endless'), 'endless');
  }
  for (const drill of ['', 'mental-stack', 'unknown']) assert.equal(context.drillMode(drill, 'arcade'), 'endless');
});

test('Arcade partial headline counts whole enemies, while clear headline is elapsed time', () => {
  const partial = context.arcadePresentation({completed:false,enemies_defeated:7,total_opponents:13,elapsed_ms:123456});
  assert.equal(partial.headline, '7');
  assert.equal(partial.label, 'ENEMIES DEFEATED');
  assert.equal(partial.progress, '7 / 13 enemies');
  const clear = context.arcadePresentation({completed:true,enemies_defeated:12,total_opponents:12,elapsed_ms:123456});
  assert.equal(clear.headline, '2:03.45');
  assert.equal(clear.label, 'CLEAR TIME');
  assert.equal(clear.progress, '12 / 12 enemies');
});

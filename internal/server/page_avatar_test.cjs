// The website's avatar descriptor rules, run against the page's own script text.
const {test} = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const vm = require('node:vm');
const source = fs.readFileSync(__dirname + '/page.go', 'utf8');
const start = source.indexOf('  var DEFAULT_LOOK');
const end = source.indexOf('  var SVG_NS', start);
assert.ok(start > 0 && end > start, 'avatar parser block not found in page.go');
const context = vm.createContext({});
vm.runInContext(source.slice(start, end) + '\nthis.avatarLook = avatarLook; this.DEFAULT_LOOK = DEFAULT_LOOK;', context);

const art = { fighters: {
  'puppet': { palette: [[100,210,255],[124,242,166]], bonus: ['bonus-ex01'], hat: ['<g/>'],
    gear: { 'scouter-visor': ['<s/>'], 'switch-cap': ['<c/>'] } },
  'puppet-oni': { palette: [[200,32,42],[90,40,120]], hat: ['<g/>'] },
  'puppet-nohat': { palette: [[1,2,3]] },
  'puppet-ronin': { palette: [[131,138,234],[1,1,1]], hat: ['<g/>'], gear: { 'knight-helmet': ['<k/>'] },
    outfits: { '2': { palette: [[131,138,234],[79,200,90]], parts: [''], head: ['<o/>'],
                      gear: { 'pirate-hat': ['<p/>'], 'diving-helmet': ['<d/>'] } } } },
}};
const look = (raw) => JSON.parse(JSON.stringify(context.avatarLook(raw, art)));
const DEFAULT = { body: 'puppet', outfit: '1', gear: null, fill: [100,210,255], outline: null, hat: false };

test('v1: fighter, colour index into its palette, pirate hat', () => {
  assert.deepEqual(look('1:puppet-oni:1:1:pirate-hat'), { body:'puppet-oni', outfit:'1', gear:'pirate-hat', fill:[90,40,120], outline:null, hat:true });
  assert.deepEqual(look('1:puppet-oni:1:0'), { body:'puppet-oni', outfit:'1', gear:null, fill:[200,32,42], outline:null, hat:false });
});

test('colour past the palette draws the first colour; unknown gear is dropped', () => {
  assert.deepEqual(look('1:puppet-oni:1:9:top-hat'), { body:'puppet-oni', outfit:'1', gear:null, fill:[200,32,42], outline:null, hat:false });
  assert.equal(look('1:puppet-nohat:1:0:pirate-hat').hat, false); // no hat art for that body
});

test('v2 EX01 on Standard is two-tone; on any other fighter, or another look, it is ignored', () => {
  assert.deepEqual(look('2:puppet:1:0::bonus-ex01'), { body:'puppet', outfit:'1', gear:null, fill:[100,210,255], outline:[255,201,60], hat:false });
  assert.deepEqual(look('2:puppet:1:1:pirate-hat:bonus-ex01'), { body:'puppet', outfit:'1', gear:'pirate-hat', fill:[100,210,255], outline:[255,201,60], hat:true });
  assert.deepEqual(look('2:puppet-oni:1:1::bonus-ex01'), { body:'puppet-oni', outfit:'1', gear:null, fill:[90,40,120], outline:null, hat:false });
  assert.deepEqual(look('2:puppet:1:1::bonus-zz99'), { body:'puppet', outfit:'1', gear:null, fill:[124,242,166], outline:null, hat:false });
});

test('empty, garbage, unknown fighter, bad shapes and no art draw the blue Standard default', () => {
  for (const raw of [null, undefined, 42, '', 'garbage', '1:puppet-ghost:1:0', '3:puppet:1:0',
                     '2:puppet:1:0', '1:puppet:1:0::bonus-ex01', '1:Puppet:1:0', '<b>x</b>',
                     '2:' + 'f'.repeat(32) + ':1:0:' + 'g'.repeat(13) + ':' + 'l'.repeat(12)]) {
    assert.deepEqual(look(raw), DEFAULT, String(raw));
  }
  assert.deepEqual(JSON.parse(JSON.stringify(context.avatarLook('1:puppet-oni:1:0', null))), DEFAULT);
});

test('outfit 2 draws its own colours and gear; an outfit without art, or a bonus look, is Outfit 1', () => {
  assert.deepEqual(look('1:puppet-ronin:2:1'), { body:'puppet-ronin', outfit:'2', gear:null, fill:[79,200,90], outline:null, hat:false });
  assert.deepEqual(look('1:puppet-ronin:2:0:diving-helmet'), { body:'puppet-ronin', outfit:'2', gear:'diving-helmet', fill:[131,138,234], outline:null, hat:false });
  assert.equal(look('1:puppet-ronin:2:0:knight-helmet').gear, null); // no Outfit 2 knight art in this fixture
  assert.deepEqual(look('1:puppet-ronin:1:0:knight-helmet'), { body:'puppet-ronin', outfit:'1', gear:'knight-helmet', fill:[131,138,234], outline:null, hat:false });
  assert.deepEqual(look('1:puppet-oni:2:1'), { body:'puppet-oni', outfit:'1', gear:null, fill:[90,40,120], outline:null, hat:false });
  assert.equal(look('2:puppet:2:0::bonus-ex01').outfit, '1');
});

// #7853: gear shows when this file has art for it, so newly baked hats (Ver .36) appear with no
// list in the page to update; EX01 keeps its hat.
test('any baked head gear shows, EX01 included; the first id with art wins', () => {
  assert.deepEqual(look('1:puppet:1:0:scouter-visor'), { body:'puppet', outfit:'1', gear:'scouter-visor', fill:[100,210,255], outline:null, hat:false });
  assert.deepEqual(look('2:puppet:1:0:switch-cap:bonus-ex01'), { body:'puppet', outfit:'1', gear:'switch-cap', fill:[100,210,255], outline:[255,201,60], hat:false });
  assert.equal(look('1:puppet:1:0:top-hat,scouter-visor').gear, 'scouter-visor');
  assert.equal(look('1:puppet:1:0:crash-helmet').gear, null); // no art for it in this fixture
});

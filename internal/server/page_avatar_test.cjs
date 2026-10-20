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
  'puppet': { palette: [[100,210,255],[124,242,166]], bonus: ['bonus-ex01'], hat: ['<g/>'] },
  'puppet-oni': { palette: [[200,32,42],[90,40,120]], hat: ['<g/>'] },
  'puppet-nohat': { palette: [[1,2,3]] },
}};
const look = (raw) => JSON.parse(JSON.stringify(context.avatarLook(raw, art)));
const DEFAULT = { body: 'puppet', fill: [255,159,10], outline: null, hat: false };

test('v1: fighter, colour index into its palette, pirate hat', () => {
  assert.deepEqual(look('1:puppet-oni:1:1:pirate-hat'), { body:'puppet-oni', fill:[90,40,120], outline:null, hat:true });
  assert.deepEqual(look('1:puppet-oni:1:0'), { body:'puppet-oni', fill:[200,32,42], outline:null, hat:false });
});

test('colour past the palette draws the first colour; unknown gear is dropped', () => {
  assert.deepEqual(look('1:puppet-oni:1:9:top-hat'), { body:'puppet-oni', fill:[200,32,42], outline:null, hat:false });
  assert.equal(look('1:puppet-nohat:1:0:pirate-hat').hat, false); // no hat art for that body
});

test('v2 EX01 on Standard is two-tone; on any other fighter, or another look, it is ignored', () => {
  assert.deepEqual(look('2:puppet:1:0::bonus-ex01'), { body:'puppet', fill:[100,210,255], outline:[255,201,60], hat:false });
  assert.deepEqual(look('2:puppet:1:1:pirate-hat:bonus-ex01'), { body:'puppet', fill:[100,210,255], outline:[255,201,60], hat:true });
  assert.deepEqual(look('2:puppet-oni:1:1::bonus-ex01'), { body:'puppet-oni', fill:[90,40,120], outline:null, hat:false });
  assert.deepEqual(look('2:puppet:1:1::bonus-zz99'), { body:'puppet', fill:[124,242,166], outline:null, hat:false });
});

test('empty, garbage, unknown fighter, bad shapes and no art draw the orange default', () => {
  for (const raw of [null, undefined, 42, '', 'garbage', '1:puppet-ghost:1:0', '3:puppet:1:0',
                     '2:puppet:1:0', '1:puppet:1:0::bonus-ex01', '1:Puppet:1:0', '<b>x</b>',
                     '2:' + 'f'.repeat(32) + ':1:0:' + 'g'.repeat(13) + ':' + 'l'.repeat(12)]) {
    assert.deepEqual(look(raw), DEFAULT, String(raw));
  }
  assert.deepEqual(JSON.parse(JSON.stringify(context.avatarLook('1:puppet-oni:1:0', null))), DEFAULT);
});

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

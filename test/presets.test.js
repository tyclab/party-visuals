'use strict';
// The live-audio visualizer's presets: a shuffled round of the whole pack, changed only while the picture shows.
const test = require('node:test');
const assert = require('node:assert/strict');
const { PresetCycle, PRESET_SECONDS } = require('../shared/presets.js');

test('presets change after their time with the picture showing, each once a round, never twice in a row', () => {
  const names = ['a', 'b', 'c', 'd'];
  let seed = 3;
  const cycle = new PresetCycle(names, { random: () => ((seed = (seed * 16807) % 2147483647) / 2147483647) });
  assert.equal(cycle.intervalMs, PRESET_SECONDS * 1000);
  const shown = [cycle.next()];
  for (let round = 0; round < 50 * names.length; round++) {
    let due = false;
    for (let ms = 0; !due; ms += 16) due = cycle.advance(16);
    shown.push(cycle.next());
  }
  for (let r = 0; r + names.length <= shown.length; r += names.length) assert.deepEqual([...shown.slice(r, r + names.length)].sort(), names, 'each once a round');
  assert.ok(shown.every((name, i) => i === 0 || name !== shown[i - 1]), 'never the same twice in a row');
  assert.equal(cycle.advance(PRESET_SECONDS * 1000 - 1000), false, 'a stall of the page counts at most a second');
  assert.equal(new PresetCycle([]).next(), null);
});

test('a preset that does not compile is counted, left out from then on, and with none left there is no preset', () => {
  const cycle = new PresetCycle(['a', 'b', 'c', 'd'], { random: () => 0.5 });
  const tried = [], load = (name) => { tried.push(name); if (name === 'b' || name === 'd') throw new SyntaxError(name); };
  const shown = Array.from({ length: 8 }, () => cycle.load(load));
  assert.deepEqual([...cycle.failed].sort(), ['b', 'd']);
  assert.ok(shown.every((name) => name === 'a' || name === 'c'), shown.join());
  assert.equal(tried.filter((name) => name === 'b').length, 1, 'tried once, then left out');
  assert.deepEqual([...cycle.names].sort(), ['a', 'c']);
  const none = new PresetCycle(['x', 'y']);
  assert.equal(none.load(() => { throw new Error('no'); }), null);
  assert.equal(none.failed.length, 2);
});

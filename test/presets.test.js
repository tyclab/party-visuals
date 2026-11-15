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

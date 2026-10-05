'use strict';
const test = require('node:test');
const assert = require('node:assert/strict');
const { effectivePalette, paletteFrom, toHex, DEFAULT_PALETTE } = require('../shared/palette.js');

test('the palette override wins over the look', () => {
  assert.deepEqual(effectivePalette({ override: ['#112233'], palette: ['#FF0000', '#00FF00'] }),
    { colours: ['#112233'], source: 'override' });
});

test('no override, or an empty one: the look\'s colours', () => {
  for (const override of [null, undefined, []]) {
    assert.deepEqual(effectivePalette({ override, palette: ['#FF0000', '#00FF00'] }),
      { colours: ['#FF0000', '#00FF00'], source: 'look' });
  }
});

test('neither override nor look: the default palette, never an empty one', () => {
  for (const palette of [null, undefined, [], ['not a colour']]) {
    const result = effectivePalette({ override: null, palette });
    assert.deepEqual(result, { colours: DEFAULT_PALETTE, source: 'default' });
  }
  assert.ok(DEFAULT_PALETTE.length >= 2);
});

test('colours are cleaned up: upper-case hex, duplicates dropped, at most eight', () => {
  assert.deepEqual(paletteFrom(['#ff0000', '#FF0000', '#00ff00']), ['#FF0000', '#00FF00']);
  const many = Array.from({ length: 12 }, (_, i) => `#0000${(i * 16).toString(16).padStart(2, '0')}`);
  assert.equal(paletteFrom(many).length, 8);
  assert.equal(paletteFrom('nope'), null);
  assert.equal(paletteFrom([]), null);
});

test('white, amber and UV show on screen the way the lightshow\'s own swatches show them', () => {
  // #RRGGBBWW: the white channel added to all three, then scaled back into range.
  assert.equal(toHex('#00000080'), '#808080');
  assert.equal(toHex({ r: 0, g: 0, b: 0, w: 0, a: 0, uv: 255 }), '#3300E6');
  assert.equal(toHex({ r: 200, g: 150, b: 0, w: 0, a: 255, uv: 0 }), '#FF9C00');
  assert.equal(toHex(3, [{ r: 1, g: 2, b: 3 }]), null, 'an index past the catalogue');
  assert.equal(toHex(0, [{ r: 1, g: 2, b: 3 }]), '#010203');
  assert.equal(toHex('#12345'), null);
});

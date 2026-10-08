'use strict';
const test = require('node:test');
const assert = require('node:assert/strict');
const { effectivePalette, paletteFrom, paletteBodyFrom, toHex, DEFAULT_PALETTE } = require('../shared/palette.js');

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

test('RGB, RGBW, RGBWA and RGBWAUV bytes are emitters and match the release swatches', () => {
  const samples = [
    ['#f08', '#FF0088'], ['#123456', '#123456'], ['#000000ff', '#FFFFFF'],
    ['#00000000ff', '#FF8000'], ['#0000000000ff', '#3300E6'],
    ['#C8960000FF00', '#FF9C00'],
  ];
  for (const [input, expected] of samples) assert.equal(toHex(input), expected, input);
  assert.equal(toHex('#20406080C010'), toHex({ r: 32, g: 64, b: 96, w: 128, a: 192, uv: 16 }));
  for (const invalid of ['#1234', '#1234567', '#123456789', '#123456789AB', '#123456789ABCDE', '#0000000000xx']) {
    assert.equal(toHex(invalid), null, invalid);
  }
});

test('palette bodies retain published random colours and use matching legacy slots for unresolved markers', () => {
  const body = { colours: ['#123456', { random: true }, 'random', '#0000000000ff'] };
  const fallback = ['#000', '#0f0', '#f00'];
  assert.deepEqual(paletteBodyFrom(body, fallback), ['#123456', '#00FF00', '#FF0000', '#3300E6']);
  assert.deepEqual(paletteBodyFrom(structuredClone(body), fallback), paletteBodyFrom(body, fallback));
  assert.deepEqual(paletteBodyFrom(body), ['#123456', '#3300E6'], 'unresolved slots are omitted without inventing colours');
  assert.equal(paletteBodyFrom({ colours: [{ random: true }] }), null);
});

test('selected gradients become ordered stop colours while wash and bar keep their own geometry', () => {
  const body = {
    colours: ['#f00', '#f00', '#00f'],
    gradients: [
      { name: 'forward', space: 'oklch', wrap: true, stops: [{ at: 0, slot: 0 }, { at: 0.7, slot: 2 }] },
      { name: 'back', space: 'step', wrap: false, stops: [{ at: 0.1, slot: 2 }, { at: 0.4, colour: '#00000000ff' }, { at: 1, slot: 1 }] },
    ],
    sets: [{ name: 'roles', roles: ['forward', 'back'] }],
    gradient: 'back',
  };
  assert.deepEqual(paletteBodyFrom(body), ['#0000FF', '#FF8000', '#FF0000']);
  assert.deepEqual(paletteBodyFrom({ ...body, gradientSet: 'roles' }), ['#FF0000', '#0000FF']);
  assert.deepEqual(paletteBodyFrom({ ...body, gradientSet: 'roles', gradientRole: 3 }), ['#0000FF', '#FF8000', '#FF0000']);
  assert.deepEqual(paletteBodyFrom({ ...body, gradient: null }), ['#FF0000', '#0000FF'], 'the engine defaults to the first gradient');
  assert.deepEqual(paletteBodyFrom({ colours: body.colours, gradients: [{ stops: [null, { colour: 'invalid' }] }] }), ['#FF0000', '#0000FF']);
});

test('palette conversion and gradient approximation also work as a plain browser script', () => {
  const fs = require('node:fs');
  const vm = require('node:vm');
  const browser = vm.createContext({});
  vm.runInContext(fs.readFileSync(require.resolve('../shared/palette.js'), 'utf8'), browser);
  assert.equal(browser.PartyVisuals.toHex('#0000000000FF'), '#3300E6');
  assert.equal(JSON.stringify(browser.PartyVisuals.paletteBodyFrom({ colours: ['#00000000FF'] })), '["#FF8000"]');
});

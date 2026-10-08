'use strict';
const test = require('node:test');
const assert = require('node:assert/strict');
const { StateMirror, readLook } = require('../extension/protocol.js');
const { baseState } = require('./helpers/mock-lightshow.js');

const snapshot = (state = baseState(), versions = { look: 3, catalogs: 1 }) => ({ protocol: 2, versions, state });

test('a snapshot gives the tempo, the clock source and the look colours as hex', () => {
  const mirror = new StateMirror();
  assert.equal(mirror.applySnapshot(snapshot()), true);
  assert.deepEqual(readLook(mirror.state), {
    bpm: 120,
    clockSource: 'tap',
    beat: null,
    epoch: null,
    audioMode: null,
    // Magenta, Cyan, Congo Blue, Moonlight (white mixed in and scaled back,
    // as the lightshow's own swatches show it)
    palette: ['#FF0096', '#00E1FF', '#4B00FF', '#80AEFF'],
    paletteOverride: null,
  });
});

test('the clock tempo wins over the typed bpm, and the typed bpm shows when there is no clock', () => {
  assert.equal(readLook({ ...baseState(), bpm: 120, clock: { source: 'cdj', bpm: 127.9 } }).bpm, 127.9);
  const look = readLook({ ...baseState(), bpm: 96, clock: undefined });
  assert.equal(look.bpm, 96);
  assert.equal(look.clockSource, null);
  assert.equal(readLook({ ...baseState(), bpm: 'fast', clock: { source: 'tap', bpm: -3 } }).bpm, null);
});

test('a beat position and an epoch are read when the lightshow sends them', () => {
  // `at`, the lightshow's wall clock at the reading, rides along and is not read.
  const look = readLook({ ...baseState(), clock: { source: 'auto', bpm: 128, beatPos: 64.5, epoch: 3, at: 1_760_000_000_000 } });
  assert.deepEqual(Object.keys(look).sort(), ['audioMode', 'beat', 'bpm', 'clockSource', 'epoch', 'palette', 'paletteOverride']);
  assert.equal(look.beat, 64.5);
  assert.equal(look.epoch, 3);
  assert.equal(look.clockSource, 'auto');
  assert.equal(readLook({ ...baseState(), clock: { source: 'auto', bpm: 128, beatPos: 'x' } }).beat, null);
});

test('reactive mode changes are published without inventing a clock reading', () => {
  const mirror = new StateMirror();
  mirror.applySnapshot(snapshot({ ...baseState(), clock: { bpm: 120, beatPos: 3, epoch: 1 } }));
  assert.equal(mirror.applyPatch({ d: 'system', v: 1, set: { audio: { mode: 'reactive' } } }), 'ok');
  assert.equal(mirror.touchedLook(), true);
  assert.equal(mirror.lastChanged.has('clock'), false);
  assert.equal(readLook(mirror.state).audioMode, 'reactive');
  assert.equal(readLook({ ...mirror.state, audio: { mode: 'bad' } }).audioMode, null);
});

test('a look of one colour on all four slots is one colour', () => {
  const look = readLook({ ...baseState(), colorA: 0, colorB: 0, colorC: 0, colorD: 0 });
  assert.deepEqual(look.palette, ['#FF0000']);
});

test('slots on presets the catalogue does not have give no palette', () => {
  assert.equal(readLook({ ...baseState(), colorPresets: undefined }).palette, null);
  assert.equal(readLook({ ...baseState(), colorA: 99, colorB: 99, colorC: 99, colorD: 99 }).palette, null);
});

test('a palette override arrives as hex or as colours, and a missing one is no override', () => {
  assert.equal(readLook(baseState()).paletteOverride, null);
  assert.equal(readLook({ ...baseState(), paletteOverride: null }).paletteOverride, null);
  assert.equal(readLook({ ...baseState(), paletteOverride: [] }).paletteOverride, null);
  assert.equal(readLook({ ...baseState(), paletteOverride: 'red' }).paletteOverride, null);
  assert.deepEqual(readLook({ ...baseState(), paletteOverride: ['#ff0000', '#00ff00'] }).paletteOverride, ['#FF0000', '#00FF00']);
  assert.deepEqual(readLook({ ...baseState(), paletteOverride: [{ r: 0, g: 0, b: 255, w: 0, a: 0, uv: 0 }] }).paletteOverride, ['#0000FF']);
});

test('patches apply in order; an old one is ignored and a skipped one asks for a resync', () => {
  const mirror = new StateMirror();
  mirror.applySnapshot(snapshot());
  assert.equal(mirror.applyPatch({ d: 'look', v: 4, set: { bpm: 128, clock: { source: 'tap', bpm: 128 } } }), 'ok');
  assert.equal(readLook(mirror.state).bpm, 128);
  assert.equal(mirror.applyPatch({ d: 'look', v: 4, set: { bpm: 90 } }), 'stale');
  assert.equal(readLook(mirror.state).bpm, 128);
  assert.equal(mirror.applyPatch({ d: 'look', v: 6, set: { bpm: 140 } }), 'gap');
  assert.equal(readLook(mirror.state).bpm, 128, 'a patch after a gap is not applied');
  // A domain the snapshot did not list starts at 0.
  assert.equal(mirror.applyPatch({ d: 'sources', v: 1, set: { nowPlaying: { title: 'x' } } }), 'ok');
  assert.equal(mirror.applyPatch({ d: 'look', v: 5, set: { bpm: 110 }, del: ['clock'] }), 'ok');
  assert.equal(mirror.state.clock, undefined);
  assert.equal(readLook(mirror.state).bpm, 110, 'with the clock gone, the typed bpm shows');
});

test('a patch before any snapshot, or a malformed one, is a gap or ignored', () => {
  const mirror = new StateMirror();
  assert.equal(mirror.applyPatch({ d: 'look', v: 1, set: { bpm: 100 } }), 'gap');
  mirror.applySnapshot(snapshot());
  assert.equal(mirror.applyPatch(null), 'invalid');
  assert.equal(mirror.applyPatch({ d: 'look', v: 'x', set: {} }), 'invalid');
  assert.equal(mirror.applySnapshot({ protocol: 2 }), false);
});

test('the original full-state event replaces the mirror and the next patch resyncs', () => {
  const mirror = new StateMirror();
  mirror.applySnapshot(snapshot());
  mirror.applyFullState({ ...baseState(), bpm: 100, clock: { source: 'tap', bpm: 100 }, dmxSnapshot: { 1: [0] } });
  assert.equal(readLook(mirror.state).bpm, 100);
  assert.equal(mirror.state.dmxSnapshot, undefined, 'the DMX picture is not kept');
  assert.equal(mirror.applyPatch({ d: 'look', v: 9, set: { bpm: 101 } }), 'gap');
});

test('patches report which keys they changed', () => {
  const mirror = new StateMirror();
  mirror.applySnapshot(snapshot());
  mirror.applyPatch({ d: 'look', v: 4, set: { colorA: 0 }, del: ['paletteOverride'] });
  assert.deepEqual([...mirror.lastChanged].sort(), ['colorA', 'paletteOverride']);
});

test('new palette bodies take precedence, and clearing them restores the legacy base slots', () => {
  const state = { ...baseState(), basePalette: { colours: ['#000000FF', '#00000000FF'] },
    overridePalette: { colours: ['#0000000000FF'] }, paletteOverride: ['#ff0000'] };
  assert.deepEqual(readLook(state).palette, ['#FFFFFF', '#FF8000']);
  assert.deepEqual(readLook(state).paletteOverride, ['#3300E6']);
  assert.equal(readLook({ ...state, overridePalette: null }).paletteOverride, null, 'an explicit clear wins over a stale legacy override');
  assert.deepEqual(readLook({ ...state, basePalette: null }).palette, readLook(baseState()).palette);
  assert.deepEqual(readLook({ ...state, overridePalette: undefined }).paletteOverride, ['#FF0000']);
});

test('unresolved random slots follow their legacy values without shifting slot indices', () => {
  const state = { ...baseState(), basePalette: { colours: [{ random: true }, '#f00', 'random'] },
    overridePalette: { colours: [{ random: true }, '#00000000ff'] }, paletteOverride: ['#000000ff', '#f00'] };
  assert.deepEqual(readLook(state).palette, ['#FF0096', '#FF0000', '#4B00FF']);
  assert.deepEqual(readLook(state).paletteOverride, ['#FFFFFF', '#FF8000']);
});

test('base and override patches touch the look; palette catalogue changes do not replay the clock', () => {
  const mirror = new StateMirror();
  mirror.applySnapshot(snapshot());
  assert.equal(mirror.applyPatch({ d: 'look', v: 4, set: { basePalette: { colours: ['#00000000ff'] } } }), 'ok');
  assert.equal(mirror.touchedLook(), true);
  assert.deepEqual(readLook(mirror.state).palette, ['#FF8000']);
  assert.equal(mirror.lastChanged.has('clock'), false);
  assert.equal(mirror.applyPatch({ d: 'look', v: 5, set: { overridePalette: { colours: ['#0000000000ff'] } } }), 'ok');
  assert.equal(mirror.touchedLook(), true);
  assert.deepEqual(readLook(mirror.state).paletteOverride, ['#3300E6']);
  assert.equal(mirror.applyPatch({ d: 'catalogs', v: 2, set: { builtinPalettes: [] } }), 'ok');
  assert.equal(mirror.touchedLook(), false);
  assert.equal(mirror.applyPatch({ d: 'library', v: 1, set: { userPalettes: [] } }), 'ok');
  assert.equal(mirror.touchedLook(), false);
  assert.equal(mirror.applyPatch({ d: 'look', v: 6, del: ['overridePalette'] }), 'ok');
  assert.equal(mirror.touchedLook(), true);
  assert.equal(readLook(mirror.state).paletteOverride, null);
});

'use strict';
const test = require('node:test');
const assert = require('node:assert/strict');
const { BeatClock, MAX_BPM, MIN_BPM } = require('../shared/beat-clock.js');

const near = (actual, expected, eps = 1e-9, msg) => assert.ok(Math.abs(actual - expected) <= eps, `${msg || ''} ${actual} ≉ ${expected}`);
const msPerBeat = (bpm) => 60000 / bpm;

test('free-running, a tempo change from 120 to 128 keeps the position and changes the rate', () => {
  const clock = new BeatClock();
  assert.equal(clock.update({ bpm: 120, atMs: 0 }), 'start');
  near(clock.at(1000), 2);
  assert.equal(clock.update({ bpm: 128, atMs: 1000 }), 'tempo');
  near(clock.at(1000), 2, 1e-9, 'no jump at the change');
  near(clock.at(1000 + msPerBeat(128)), 3, 1e-9, 'one beat later at the new tempo');
  near(clock.at(1000 + 8 * msPerBeat(128)), 10);
  assert.equal(clock.locked, false, 'no beat position from the lightshow: phase is the clock\'s own');
});

test('phase-locked, a tempo change mid-beat carries on from where the music is', () => {
  const clock = new BeatClock();
  clock.update({ bpm: 120, beat: 16, atMs: 0 });
  // 750 ms later the lightshow reports the new tempo and where it is: 17.5.
  assert.equal(clock.update({ bpm: 128, beat: 17.5, atMs: 750 }), 'slew');
  near(clock.at(750), 17.5);
  near(clock.at(750 + msPerBeat(128) / 2), 18);
  assert.equal(clock.locked, true);
  assert.equal(clock.bpm, 128);
});

test('a reading slightly off is slewed in, and the position never runs backwards', () => {
  const clock = new BeatClock();
  clock.update({ bpm: 128, beat: 0, atMs: 0 });
  // The lightshow says we are 0.2 beats behind where the clock has got to.
  const at = 2000;
  const predicted = clock.at(at);
  assert.equal(clock.update({ bpm: 128, beat: predicted - 0.2, atMs: at }), 'slew');
  near(clock.at(at), predicted, 1e-9, 'no step at the reading');
  let previous = clock.at(at);
  for (let t = at + 5; t <= at + 2000; t += 5) {
    const beat = clock.at(t);
    assert.ok(beat > previous, `moved backwards at ${t}`);
    previous = beat;
  }
  // Once the correction is worked off the clock runs on from the reading.
  near(clock.at(at + 2000), predicted - 0.2 + 2000 * 128 / 60000, 1e-9);
});

test('a reading far from the position is a jump: it is taken at once and the epoch moves on', () => {
  const clock = new BeatClock();
  clock.update({ bpm: 120, beat: 0, atMs: 0 });
  const epoch = clock.epoch;
  assert.equal(clock.update({ bpm: 120, beat: 200, atMs: 1000 }), 'jump');
  near(clock.at(1000), 200);
  assert.equal(clock.epoch, epoch + 1);
});

test('the lightshow\'s own epoch change re-anchors even for a small difference', () => {
  const clock = new BeatClock();
  clock.update({ bpm: 120, beat: 0, atMs: 0, epoch: 4 });
  assert.equal(clock.update({ bpm: 120, beat: 1.9, atMs: 1000, epoch: 5 }), 'jump');
  near(clock.at(1000), 1.9);
});

test('a time before the anchor reads as the anchor, not earlier', () => {
  const clock = new BeatClock();
  clock.update({ bpm: 120, beat: 8, atMs: 1000 });
  near(clock.at(500), 8);
});

test('a tempo out of the lightshow\'s range is clamped, and a missing one keeps the last', () => {
  const clock = new BeatClock();
  clock.update({ bpm: 1000, atMs: 0 });
  assert.equal(clock.bpm, MAX_BPM);
  clock.update({ bpm: 1, atMs: 0 });
  assert.equal(clock.bpm, MIN_BPM);
  clock.update({ bpm: Number.NaN, atMs: 0 });
  assert.equal(clock.bpm, MIN_BPM);
  assert.equal(new BeatClock().at(1234), 0, 'a clock that never heard anything stands at 0');
});

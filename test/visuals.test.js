'use strict';
const test = require('node:test');
const assert = require('node:assert/strict');
const { BeatVisual, FrameLimiter, Ease, pulseDivisor, MAX_FLASH_HZ } = require('../shared/visuals.js');

/**
 * Drive a BeatVisual the way a graphic does: one frame every `frameMs`, the
 * beat from `beatAt(t)`, live while `liveAt(t)`. Returns every frame and the
 * times a new pulse started.
 */
function run({ ms, frameMs = 1000 / 60, bpm = 120, beatAt = (t) => t * bpm / 60000, liveAt = () => true, visual = new BeatVisual() }) {
  const frames = [];
  const onsets = [];
  for (let t = 0; t <= ms; t += frameMs) {
    const frame = visual.frame({ nowMs: t, beat: beatAt(t), bpm, live: liveAt(t) });
    if (frame.onset) onsets.push(t);
    frames.push({ t, ...frame });
  }
  return { frames, onsets };
}

const gaps = (times) => times.slice(1).map((t, i) => t - times[i]);

test('at 120 bpm the graphics pulse once a beat', () => {
  const { onsets, frames } = run({ ms: 10000, bpm: 120 });
  assert.ok(onsets.length >= 19 && onsets.length <= 20, `${onsets.length} pulses in 10 s`);
  assert.ok(Math.max(...frames.map((f) => f.pulse)) > 0.9, 'a pulse reaches the top');
});

test('nothing pulses faster than 5 Hz, whatever tempo arrives', () => {
  assert.equal(MAX_FLASH_HZ, 5);
  for (const bpm of [300, 301, 450, 600, 1200]) {
    const { onsets } = run({ ms: 10000, bpm, frameMs: 1000 / 144 });
    assert.ok(onsets.length <= 50 + 1, `${bpm} bpm: ${onsets.length} pulses in 10 s`);
    assert.ok(Math.min(...gaps(onsets)) >= 200 - 1e-6, `${bpm} bpm: pulses ${Math.min(...gaps(onsets))} ms apart`);
  }
  assert.equal(pulseDivisor(300), 1, 'five beats a second is the limit itself');
  assert.equal(pulseDivisor(301), 2);
  assert.equal(pulseDivisor(1200), 4);
});

test('a beat position that jumps about cannot fire pulses closer than 200 ms', () => {
  // A reading every 50 ms that throws the position a whole beat forward each time.
  const { onsets } = run({ ms: 5000, bpm: 120, frameMs: 10, beatAt: (t) => Math.floor(t / 50) });
  assert.ok(onsets.length > 0);
  assert.ok(Math.min(...gaps(onsets)) >= 200 - 1e-6, `pulses ${Math.min(...gaps(onsets))} ms apart`);
  // And a jump backwards is never a pulse.
  const visual = new BeatVisual();
  visual.frame({ nowMs: 0, beat: 10.5, bpm: 120, live: true });
  assert.equal(visual.frame({ nowMs: 300, beat: 2.1, bpm: 120, live: true }).onset, false);
});

test('a disconnect freezes the graphics calm: no new pulse, the glow fades, the motion stops', () => {
  const dropAt = 4000;
  const { frames, onsets } = run({ ms: 14000, bpm: 128, liveAt: (t) => t < dropAt });
  assert.ok(onsets.every((t) => t < dropAt), 'no pulse starts after the drop');
  const after = frames.filter((f) => f.t >= dropAt);
  for (let i = 1; i < after.length; i++) {
    assert.ok(after[i].pulse <= after[i - 1].pulse + 1e-12, `the glow rose again at ${after[i].t}`);
  }
  const settled = frames.filter((f) => f.t >= dropAt + 1000);
  assert.ok(settled.every((f) => f.pulse < 0.01), 'calm within a second');
  const motion = new Set(settled.map((f) => f.motion));
  assert.equal(motion.size, 1, 'the motion holds still');
  assert.ok(settled.every((f) => f.live === 0), 'fully calm');
});

test('coming back eases the beat in rather than switching it on', () => {
  const backAt = 2000;
  const { frames } = run({ ms: 5000, bpm: 120, liveAt: (t) => t >= backAt, frameMs: 1000 / 30 });
  const rising = frames.filter((f) => f.t >= backAt && f.t < backAt + 1000).map((f) => f.live);
  for (let i = 1; i < rising.length; i++) assert.ok(rising[i] - rising[i - 1] <= 0.05, 'eased, not stepped');
  assert.equal(frames.at(-1).live, 1);
});

test('the frame-rate cap holds on a fast display', () => {
  for (const maxFps of [24, 30, 60]) {
    const limiter = new FrameLimiter(maxFps);
    let drawn = 0;
    for (let t = 0; t < 10000; t += 1000 / 144) if (limiter.ready(t)) drawn++;
    assert.ok(drawn <= maxFps * 10 + 1, `${maxFps} fps cap drew ${drawn} frames in 10 s`);
    assert.ok(drawn >= maxFps * 10 * 0.8, `${maxFps} fps cap drew only ${drawn} frames in 10 s`);
  }
  assert.throws(() => new FrameLimiter(0));
});

test('an ease moves toward its target at a fixed rate', () => {
  const ease = new Ease(1000, 0);
  assert.equal(ease.step(1, 0), 0);
  assert.ok(Math.abs(ease.step(1, 500) - 0.5) < 1e-9);
  assert.equal(ease.step(1, 5000), 1);
  assert.ok(Math.abs(ease.step(0, 5250) - 0.75) < 1e-9);
});

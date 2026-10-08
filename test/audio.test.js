'use strict';
const test = require('node:test');
const assert = require('node:assert/strict');
const { audioFrame } = require('../shared/audio.js');
const { BeatVisual } = require('../shared/visuals.js');

test('audio input is bounded, drops unknown fields and preserves event identity', () => {
  assert.equal(audioFrame(null), null);
  assert.equal(audioFrame({ t: NaN }), null);
  assert.equal(audioFrame({ t: -1, party: {}, spl: {} }), null);
  assert.deepEqual(audioFrame({ t: 42, party: { bass: 2, mid: -1, high: Infinity },
    spl: { eventT: 40, level: 52, beat: 'loud', section: 'bad' }, token: 'not forwarded' }), {
    t: 42, eventT: 40, energy: 0.52, bass: 1, mid: 0, high: 0, beat: 'loud', section: null,
  });
  assert.equal(audioFrame({ t: 42, party: {}, spl: { beat: 'loud' } }).eventT, null,
    'older producers without event identity do not invent repeat hits');
});

test('shared audio hits join beat pulses under one five-per-second cap and never repeat a held event', () => {
  const visual = new BeatVisual();
  const onsets = [];
  for (let t = 0; t <= 2000; t += 10) {
    const frame = visual.frame({ nowMs: t, beat: t / 500, bpm: 120, live: true,
      audio: { t, eventT: Math.floor(t / 30) * 30, beat: 'loud', energy: 0.6 } });
    if (frame.onset) onsets.push(t);
  }
  assert.ok(onsets.length > 4, 'off-beat music events drive additional visual response');
  assert.ok(onsets.length <= 10);
  for (let i = 1; i < onsets.length; i++) assert.ok(onsets[i] - onsets[i - 1] >= 200);
  const held = new BeatVisual();
  held.frame({ nowMs: 0, beat: 0, bpm: 120, live: true, audio: { t: 0, eventT: 0, beat: null, energy: 0 } });
  assert.equal(held.frame({ nowMs: 100, beat: 0, bpm: 120, live: true,
    audio: { t: 100, eventT: 100, beat: 'loud', energy: 1 } }).onset, true);
  for (let t = 110; t < 1000; t += 10) {
    assert.equal(held.frame({ nowMs: t, beat: 0, bpm: 120, live: true,
      audio: { t, eventT: 100, beat: 'loud', energy: 1 } }).onset, false);
  }
});

test('soft classifications, which arrive every 100 ms, add no pulses beyond the tempo', () => {
  const count = (beat) => {
    const visual = new BeatVisual();
    let onsets = 0;
    for (let t = 0; t <= 4000; t += 10) {
      const frame = visual.frame({ nowMs: t, beat: t / 500, bpm: 120, live: true,
        audio: { t, eventT: Math.floor(t / 100) * 100, beat, energy: 0.5 } });
      if (frame.onset) onsets++;
    }
    return onsets;
  };
  assert.equal(count('soft'), count('quiet'));
  assert.ok(count('loud') > count('soft'));
});

test('audio stream restarts and disconnects cannot introduce a pulse or brighten a fading frame', () => {
  const visual = new BeatVisual();
  visual.frame({ nowMs: 0, beat: 0, bpm: 120, live: true, audio: { t: 100, eventT: 100, beat: 'loud', energy: 1 } });
  assert.equal(visual.frame({ nowMs: 300, beat: 0, bpm: 120, live: true,
    audio: { t: 1, eventT: 1, beat: 'loud', energy: 1 } }).onset, false);
  visual.frame({ nowMs: 500, beat: 1, bpm: 120, live: true, audio: { t: 2, eventT: 2, beat: 'loud', energy: 1 } });
  let previous = Infinity;
  for (let t = 510; t < 1500; t += 10) {
    const frame = visual.frame({ nowMs: t, beat: 1 + t / 500, bpm: 120, live: false,
      audio: { t, eventT: t, beat: 'loud', energy: 1 } });
    assert.equal(frame.onset, false);
    assert.ok(frame.pulse <= previous);
    previous = frame.pulse;
  }
});

'use strict';
const test = require('node:test');
const assert = require('node:assert/strict');
const { Backoff } = require('../extension/backoff.js');

test('the wait doubles from the minimum up to the cap, and a success starts it again', () => {
  const backoff = new Backoff({ minMs: 500, maxMs: 8000, jitter: 0 });
  assert.deepEqual(Array.from({ length: 7 }, () => backoff.next()), [500, 1000, 2000, 4000, 8000, 8000, 8000]);
  assert.equal(backoff.attempt, 7);
  backoff.reset();
  assert.equal(backoff.attempt, 0);
  assert.equal(backoff.next(), 500);
});

test('jitter spreads the waits but keeps them inside the bounds', () => {
  for (const r of [0, 0.5, 0.999]) {
    const backoff = new Backoff({ minMs: 1000, maxMs: 4000, jitter: 0.2, random: () => r });
    const waits = Array.from({ length: 6 }, () => backoff.next());
    for (const w of waits) assert.ok(w >= 800 && w <= 4000, `${w} out of bounds`);
  }
  const low = new Backoff({ minMs: 1000, maxMs: 4000, jitter: 0.2, random: () => 0 });
  assert.equal(low.next(), 800);
});

test('a nonsense setting falls back to sane bounds', () => {
  const backoff = new Backoff({ minMs: -5, maxMs: 1, jitter: 7 });
  // A floor of 50 ms, the cap raised to it, jitter at most half.
  for (let i = 0; i < 5; i++) {
    const wait = backoff.next();
    assert.ok(wait >= 25 && wait <= 50, `${wait}`);
  }
});

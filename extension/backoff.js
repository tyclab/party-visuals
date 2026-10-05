'use strict';

// Below this a retry loop only burns CPU and fills the lightshow's log.
const FLOOR_MS = 50;

/**
 * Waits between reconnection attempts: doubling from `minMs` up to `maxMs`,
 * each shortened by up to `jitter` of itself at random so several clients do
 * not retry in step. `reset()` after a success starts again from the minimum.
 */
class Backoff {
  constructor({ minMs = 500, maxMs = 15000, factor = 2, jitter = 0.2, random = Math.random } = {}) {
    this.minMs = Math.max(FLOOR_MS, Number.isFinite(minMs) ? minMs : 500);
    this.maxMs = Math.max(this.minMs, Number.isFinite(maxMs) ? maxMs : 15000);
    this.factor = Number.isFinite(factor) && factor >= 1 ? factor : 2;
    this.jitter = Math.max(0, Math.min(0.5, Number.isFinite(jitter) ? jitter : 0.2));
    this.random = random;
    this.attempt = 0;
  }

  next() {
    const base = Math.min(this.maxMs, this.minMs * this.factor ** this.attempt);
    this.attempt++;
    return Math.round(base * (1 - this.jitter * (1 - this.random())));
  }

  reset() {
    this.attempt = 0;
  }
}

module.exports = { Backoff };

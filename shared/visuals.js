// Enforce at most five pulses/second with a 200 ms gap; fade and stop motion on disconnection.
(function (root, factory) {
  const api = factory();
  if (typeof module === 'object' && module.exports) module.exports = api;
  else root.PartyVisuals = Object.assign(root.PartyVisuals || {}, api);
})(typeof globalThis !== 'undefined' ? globalThis : this, function () {
  'use strict';

  const MAX_FLASH_HZ = 5;

  /** Beats per pulse: 1, 2, 4… so the pulses stay at or under `maxHz`. */
  function pulseDivisor(bpm, maxHz = MAX_FLASH_HZ) {
    if (!(bpm > 0) || !(maxHz > 0)) return 1;
    let n = 1;
    while (bpm / 60 / n > maxHz + 1e-9) n *= 2;
    return n;
  }

  /** A value that moves toward its target at a fixed rate: 0 to 1 in `durMs`. */
  class Ease {
    constructor(durMs, value = 0) {
      this.rate = 1 / Math.max(1, durMs);
      this.value = value;
      this.lastMs = null;
    }

    step(target, nowMs) {
      if (this.lastMs !== null) {
        const room = target - this.value;
        const move = Math.min(Math.abs(room), Math.max(0, nowMs - this.lastMs) * this.rate);
        this.value += Math.sign(room) * move;
      }
      this.lastMs = nowMs;
      return this.value;
    }
  }

  /**
   * A cap on how often a graphic draws. A frame is drawn only when a whole
   * interval (less a millisecond of timer slack) has passed since the last.
   */
  class FrameLimiter {
    constructor(maxFps) {
      if (!(maxFps > 0)) throw new RangeError('maxFps must be above 0');
      this.intervalMs = 1000 / maxFps;
      this.slackMs = Math.min(1, this.intervalMs / 10);
      this.lastMs = -Infinity;
    }

    ready(nowMs) {
      if (nowMs - this.lastMs < this.intervalMs - this.slackMs) return false;
      this.lastMs = nowMs;
      return true;
    }
  }

  /**
   * One graphic's view of the beat, frame by frame:
   *   pulse   0..1, the glow of the latest pulse, faded out while not live
   *   motion  the beat position for things that drift or scroll; it stops
   *           where it was when the lightshow went away
   *   live    0..1, eased, so a lost or regained connection never switches
   *           the beat on or off in one frame
   *   onset   whether a pulse started on this frame
   */
  class BeatVisual {
    constructor({ maxHz = MAX_FLASH_HZ, fadeMs = 800, attackMs = 30 } = {}) {
      this.maxHz = Math.min(maxHz, MAX_FLASH_HZ);
      this.minGapMs = 1000 / this.maxHz;
      this.attackMs = attackMs;
      this.decayMs = 150;
      this.fade = new Ease(fadeMs, 0);
      this.index = null;
      this.lastOnsetMs = -Infinity;
      this.lastPulse = 0;
      this.motion = 0;
    }

    frame({ nowMs, beat, bpm, live }) {
      const liveMix = this.fade.step(live ? 1 : 0, nowMs);
      let onset = false;
      if (live && Number.isFinite(beat)) {
        this.motion = beat;
        const div = pulseDivisor(bpm, this.maxHz);
        const index = Math.floor(beat / div);
        if (this.index === null || index < this.index) {
          // The first frame, or the music jumped back: no pulse for that.
          this.index = index;
        } else if (index > this.index && nowMs - this.lastOnsetMs >= this.minGapMs) {
          // A crossing that comes too soon waits for the gap rather than
          // being dropped, so a tempo right at the limit loses no beats.
          this.index = index;
          this.lastOnsetMs = nowMs;
          const periodMs = div * 60000 / Math.max(1, bpm);
          this.decayMs = Math.max(50, Math.min(250, periodMs * 0.3));
          onset = true;
        }
      } else {
        this.index = null;
      }
      let pulse = this.envelope(nowMs) * liveMix;
      // While calming down nothing may brighten, not even an attack in progress.
      if (!live) pulse = Math.min(pulse, this.lastPulse);
      this.lastPulse = pulse;
      return { pulse, motion: this.motion, live: liveMix, onset };
    }

    envelope(nowMs) {
      const t = nowMs - this.lastOnsetMs;
      if (!(t >= 0)) return 0;
      if (t < this.attackMs) return t / this.attackMs;
      return Math.exp(-(t - this.attackMs) / this.decayMs);
    }
  }

  return { MAX_FLASH_HZ, pulseDivisor, Ease, FrameLimiter, BeatVisual };
});

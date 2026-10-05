/**
 * Where the music is, in beats, between the lightshow's readings.
 *
 * The lightshow sends its tempo whenever it changes and, where it knows one,
 * a beat position. Graphics draw at their own frame rate, so between two
 * readings the position is extrapolated at the last tempo. A new reading
 * re-bases the clock where it has got to, so a tempo change carries on from
 * the same place at the new rate rather than jumping. A beat position close to
 * the extrapolated one is worked in gradually; one far from it (a seek, a new
 * track, another source taking over) is taken at once.
 *
 * Loaded by the extension (CommonJS) and by the graphics (a plain script that
 * adds `PartyVisuals.BeatClock`).
 */
(function (root, factory) {
  const api = factory();
  if (typeof module === 'object' && module.exports) module.exports = api;
  else root.PartyVisuals = Object.assign(root.PartyVisuals || {}, api);
})(typeof globalThis !== 'undefined' ? globalThis : this, function () {
  'use strict';

  // The lightshow's own tempo range (its conductor clamps to it).
  const MIN_BPM = 20;
  const MAX_BPM = 300;

  // Further than this from the extrapolated position, a reading is a jump.
  const JUMP_BEATS = 0.5;
  // Drift is worked off at no more than half the tempo's own rate, so the
  // position keeps moving forward while it is corrected.
  const MAX_CORRECTION_RATE = 0.5;
  const MIN_SLEW_MS = 250;

  function clampBpm(bpm) {
    return Math.max(MIN_BPM, Math.min(MAX_BPM, bpm));
  }

  class BeatClock {
    constructor() {
      this.anchor = null;
      this.correction = null;
      // Counts discontinuities, so a consumer can tell a jump from motion.
      this.epoch = 0;
      // Whether the phase came from the lightshow, or is this clock's own.
      this.locked = false;
      this.sourceEpoch = null;
    }

    get started() {
      return this.anchor !== null;
    }

    get bpm() {
      return this.anchor ? this.anchor.bpm : null;
    }

    /** The beat position at `nowMs` (any monotonic millisecond clock). */
    at(nowMs) {
      const a = this.anchor;
      if (!a) return 0;
      let beat = a.beat + Math.max(0, nowMs - a.atMs) * a.bpm / 60000;
      const c = this.correction;
      if (c) beat += c.beats * Math.min(1, Math.max(0, (nowMs - c.fromMs) / c.durMs));
      return beat;
    }

    /**
     * Take a reading: the tempo, and the beat position when the lightshow
     * sent one. Returns what it did: 'start', 'tempo', 'slew' or 'jump'.
     */
    update({ bpm, beat = null, atMs, epoch = null }) {
      const tempo = Number.isFinite(bpm) && bpm > 0 ? clampBpm(bpm) : (this.bpm ?? 120);
      const hasBeat = Number.isFinite(beat);
      if (!this.anchor) {
        this.anchor = { beat: hasBeat ? beat : 0, atMs, bpm: tempo };
        this.locked = hasBeat;
        this.sourceEpoch = epoch;
        return 'start';
      }

      // Re-base where the clock has got to, any correction in progress included.
      const here = this.at(atMs);
      this.anchor = { beat: here, atMs, bpm: tempo };
      this.correction = null;
      if (!hasBeat) return 'tempo';

      this.locked = true;
      const sourceJumped = epoch !== null && this.sourceEpoch !== null && epoch !== this.sourceEpoch;
      this.sourceEpoch = epoch;
      const error = beat - here;
      if (sourceJumped || Math.abs(error) > JUMP_BEATS) {
        this.anchor.beat = beat;
        this.epoch++;
        return 'jump';
      }
      if (error !== 0) {
        const durMs = Math.max(MIN_SLEW_MS, Math.abs(error) / (MAX_CORRECTION_RATE * tempo / 60000));
        this.correction = { beats: error, fromMs: atMs, durMs };
      }
      return 'slew';
    }
  }

  return { BeatClock, clampBpm, MIN_BPM, MAX_BPM, JUMP_BEATS };
});

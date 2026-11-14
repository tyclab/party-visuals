// The lightshow's live input as heard, for the live-audio visualizer: newest samples only. A packet that
// lands more than MAX_AGE_MS behind the quickest delivery of the last few seconds is dropped where it
// lands, and the renderer draws silence rather than a window that old. Shared by the extension and the page.
(function (root, factory) {
  const api = factory();
  if (typeof module === 'object' && module.exports) module.exports = api;
  else root.PartyVisuals = Object.assign(root.PartyVisuals || {}, api);
})(typeof globalThis !== 'undefined' ? globalThis : this, function () {
  'use strict';

  /** Sound to picture: no sample is drawn more than this after its quickest delivery. */
  const MAX_AGE_MS = 100;
  /** How long the quickest delivery is remembered: clock drift moves it by well under a millisecond in that. */
  const CLOCK_WINDOW_MS = 5000;
  const RING_SAMPLES = 4096;
  /** The most one pull hands the page: 46 ms at 22.05 kHz, twice what the renderer reads. */
  const PULL_SAMPLES = 1024;
  /** The lightshow's own quiet: three seconds under −80 dBFS, in 16-bit units. */
  const QUIET_LEVEL = 1e-4 * 32768;
  const QUIET_AFTER_MS = 3000;
  /** No sample for this long: the lightshow sends no audio. */
  const NO_AUDIO_MS = 1000;
  /** Why the page is calm, in the order it is told. */
  const WAITS = ['nodecg', 'lightshow', 'live-input', 'audio', 'silence'];

  /** How late a packet lands against the quickest of the last few seconds, whatever the two clocks read. */
  class ArrivalClock {
    constructor() { this.reset(); }

    reset() { this.seconds = []; }

    lateness(streamMs, nowMs) {
      const offset = nowMs - streamMs, second = Math.floor(nowMs / 1000);
      while (this.seconds.length && this.seconds[0].second <= second - CLOCK_WINDOW_MS / 1000) this.seconds.shift();
      const last = this.seconds[this.seconds.length - 1];
      if (last && last.second === second) last.min = Math.min(last.min, offset);
      else this.seconds.push({ second, min: offset });
      let best = Infinity;
      for (const s of this.seconds) best = Math.min(best, s.min);
      return offset - best;
    }
  }

  /** The newest samples of one live input process, by their place in its stream. */
  class SampleRing {
    constructor(size = RING_SAMPLES) {
      this.ring = new Int16Array(size);
      this.reset();
    }

    reset(g = null) {
      Object.assign(this, { g, rate: 0, start: 0, end: 0, freshAt: -Infinity, heardAt: -Infinity, loudAt: -Infinity });
    }

    /** Add a packet that lands `behindMs` after its quickest delivery; false when it is dropped instead. */
    take({ g, end, rate, samples }, nowMs, behindMs) {
      if (!Number.isSafeInteger(g) || !Number.isSafeInteger(end) || !(rate > 0) || !(samples instanceof Int16Array) || !samples.length || samples.length > end) return false;
      if (g !== this.g || rate !== this.rate) {
        this.reset(g);
        this.rate = rate;
        this.start = this.end = end - samples.length;
      } else if (end <= this.end) return false;
      if (behindMs > MAX_AGE_MS) return false;
      // Samples between what is held and this packet never came: the ring starts again at it.
      if (end - samples.length > this.end) this.start = end - samples.length;
      const size = this.ring.length, keep = Math.min(samples.length, size), first = end - keep;
      let energy = 0;
      for (let i = 0; i < keep; i++) {
        const v = samples[samples.length - keep + i];
        this.ring[(first + i) % size] = v;
        energy += v * v;
      }
      this.start = Math.max(this.start, end - size);
      this.end = end;
      this.freshAt = nowMs - Math.max(0, behindMs);
      this.heardAt = nowMs;
      if (Math.sqrt(energy / keep) >= QUIET_LEVEL) this.loudAt = nowMs;
      return true;
    }

    /** Samples after stream position `after`, the newest `max` of them at most. */
    since(after, max = PULL_SAMPLES) {
      const from = Math.max(after, this.start, this.end - max), out = new Int16Array(Math.max(0, this.end - from));
      for (let i = 0; i < out.length; i++) out[i] = this.ring[(from + i) % this.ring.length];
      return { from, samples: out };
    }

    /** How long ago the newest sample was delivered at the quickest. */
    ageMs(nowMs) { return nowMs - this.freshAt; }
  }

  /** What the page waits for, or null while it has sound; `link` is false when the page cannot reach NodeCG. */
  function liveAudioWait({ link = true, lightshow, live, heardAgoMs, loudAgoMs }) {
    if (!link) return 'nodecg';
    if (!lightshow) return 'lightshow';
    if (live === null || (live && live.listening === false)) return 'live-input';
    if (!(heardAgoMs <= NO_AUDIO_MS)) return 'audio';
    if (!(loudAgoMs <= QUIET_AFTER_MS)) return 'silence';
    return null;
  }

  /**
   * The newest samples as the renderer's 1024 time-domain bytes (128 is zero): the 22.05 kHz stream
   * doubled to the 44.1 kHz its band levels assume, the newest 512 samples in, linear between.
   */
  function timeBytes(samples, out = new Uint8Array(1024)) {
    const half = out.length / 2, n = samples.length;
    const at = (i) => (i < 0 ? 0 : samples[i]);
    for (let k = 0; k < half; k++) {
      const i = n - half + k, a = at(i), b = k + 1 < half ? at(i + 1) : a;
      out[2 * k] = Math.max(0, Math.min(255, 128 + Math.round(a / 256)));
      out[2 * k + 1] = Math.max(0, Math.min(255, 128 + Math.round((a + b) / 512)));
    }
    return out;
  }

  return { MAX_AGE_MS, CLOCK_WINDOW_MS, PULL_SAMPLES, QUIET_LEVEL, QUIET_AFTER_MS, NO_AUDIO_MS, WAITS, ArrivalClock, SampleRing, liveAudioWait, timeBytes };
});

'use strict';
/**
 * The lightshow's live state, kept in step with its protocol 2:
 *
 *   snapshot  { protocol: 2, versions: { <domain>: n }, state }, on connect
 *             and on request (`sync`)
 *   patch     { d: domain, v: version, set: { key: value }, del?: [key] },
 *             each domain counting its own versions; a patch that is not the
 *             next for its domain means one was missed, and the whole state
 *             is asked for again rather than drifting
 *   state     the original protocol's whole state, sent to a client that did
 *             not ask for protocol 2; GET /api/state returns the same shape
 *
 * Read-only: nothing here sends anything to the lightshow but `sync`.
 */
const { paletteFrom } = require('../shared/palette.js');

// The keys the graphics read. A patch touching none of them is applied but
// not reported, so a moving progress bar elsewhere does not re-base the beat.
const LOOK_KEYS = new Set(['bpm', 'clock', 'colorA', 'colorB', 'colorC', 'colorD', 'paletteOverride', 'colorPresets']);

class StateMirror {
  constructor() {
    this.state = {};
    this.versions = null;
    this.lastChanged = new Set();
  }

  applySnapshot(snapshot) {
    if (!snapshot || typeof snapshot.state !== 'object' || snapshot.state === null) return false;
    this.state = withoutDmx(snapshot.state);
    this.versions = { ...(snapshot.versions || {}) };
    this.lastChanged = new Set(Object.keys(this.state));
    return true;
  }

  /** The whole state without versions: the original protocol, or a poll. */
  applyFullState(state) {
    if (!state || typeof state !== 'object') return false;
    this.state = withoutDmx(state);
    // Versions unknown: a protocol 2 patch after this asks for a snapshot.
    this.versions = null;
    this.lastChanged = new Set(Object.keys(this.state));
    return true;
  }

  /** 'ok', 'stale' (already have it), 'gap' (ask for a snapshot) or 'invalid'. */
  applyPatch(patch) {
    if (!patch || typeof patch.d !== 'string' || !Number.isInteger(patch.v)) return 'invalid';
    if (!this.versions) return 'gap';
    const at = this.versions[patch.d] ?? 0;
    if (patch.v <= at) return 'stale';
    if (patch.v !== at + 1) return 'gap';
    const changed = new Set();
    for (const [key, value] of Object.entries(patch.set || {})) {
      this.state[key] = value;
      changed.add(key);
    }
    for (const key of Array.isArray(patch.del) ? patch.del : []) {
      delete this.state[key];
      changed.add(key);
    }
    this.versions[patch.d] = patch.v;
    this.lastChanged = changed;
    return 'ok';
  }

  /** Whether the last change touched anything the graphics read. */
  touchedLook() {
    for (const key of this.lastChanged) if (LOOK_KEYS.has(key)) return true;
    return false;
  }
}

function withoutDmx(state) {
  // The DMX picture rides along on a full state; nothing here needs it.
  const { dmxSnapshot: _dmx, ...rest } = state;
  return rest;
}

const positive = (v) => (typeof v === 'number' && Number.isFinite(v) && v > 0 ? v : null);

/**
 * What the graphics need from the lightshow's state:
 *   bpm              the clock's tempo, else the operator's typed bpm
 *   clockSource      what the clock is locked to (auto, cdj, track, live, tap)
 *   beat, epoch      the clock's beat position and discontinuity count, when
 *                    the lightshow sends them; null otherwise. A `clock.at`
 *                    (the lightshow's wall clock at the reading) is left
 *                    alone: it is another machine's clock, and the constant
 *                    lag it would correct is what graphics.offsetMs is for
 *   palette          the look's colours as hex: its four slots, which name
 *                    entries of the colour preset catalogue, deduplicated
 *   paletteOverride  the override's colours as hex, null when there is none
 */
function readLook(state) {
  const clock = state && typeof state.clock === 'object' && state.clock !== null ? state.clock : {};
  const slots = ['colorA', 'colorB', 'colorC', 'colorD'].map((k) => state[k]);
  return {
    bpm: positive(clock.bpm) ?? positive(state.bpm),
    clockSource: typeof clock.source === 'string' ? clock.source : null,
    beat: typeof clock.beatPos === 'number' && Number.isFinite(clock.beatPos) ? clock.beatPos : null,
    epoch: Number.isInteger(clock.epoch) ? clock.epoch : null,
    palette: paletteFrom(slots, state.colorPresets),
    paletteOverride: paletteFrom(state.paletteOverride),
  };
}

module.exports = { StateMirror, readLook, LOOK_KEYS };

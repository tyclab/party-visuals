'use strict';
// Mirror protocol 2 snapshots and versioned patches; request sync after a version gap.
// Legacy snapshots and HTTP polls have no versions; subscriptions remain read-only.
const { paletteFrom, paletteBodyFrom } = require('../shared/palette.js');

// The keys the graphics read. A patch touching none of them is applied but
// not reported, so a moving progress bar elsewhere does not re-base the beat.
const LOOK_KEYS = new Set(['bpm', 'clock', 'colorA', 'colorB', 'colorC', 'colorD',
  'basePalette', 'overridePalette', 'paletteOverride', 'colorPresets', 'audio']);

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

// Read clock and colour slots; ignore the remote wall clock because graphics.offsetMs handles fixed latency.
function readLook(state) {
  const clock = state && typeof state.clock === 'object' && state.clock !== null ? state.clock : {};
  const slots = ['colorA', 'colorB', 'colorC', 'colorD'].map((k) => state[k]);
  return {
    bpm: positive(clock.bpm) ?? positive(state.bpm),
    clockSource: typeof clock.source === 'string' ? clock.source : null,
    beat: typeof clock.beatPos === 'number' && Number.isFinite(clock.beatPos) ? clock.beatPos : null,
    epoch: Number.isInteger(clock.epoch) ? clock.epoch : null,
    audioMode: ['off', 'tempo', 'reactive'].includes(state.audio?.mode) ? state.audio.mode : null,
    palette: paletteBodyFrom(state.basePalette, slots, state.colorPresets) ?? paletteFrom(slots, state.colorPresets),
    paletteOverride: state.overridePalette === null ? null
      : paletteBodyFrom(state.overridePalette, state.paletteOverride ?? []) ?? paletteFrom(state.paletteOverride),
  };
}

module.exports = { StateMirror, readLook, LOOK_KEYS };

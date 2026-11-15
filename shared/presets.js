// The live-audio visualizer's presets: every preset of the pack once a round in a shuffled order, a new
// round in a new order that does not start with the preset just shown. Only time with the picture
// showing counts toward a change, so a quiet room never changes it.
(function (root, factory) {
  const api = factory();
  if (typeof module === 'object' && module.exports) module.exports = api;
  else root.PartyVisuals = Object.assign(root.PartyVisuals || {}, api);
})(typeof globalThis !== 'undefined' ? globalThis : this, function () {
  'use strict';

  const PRESET_SECONDS = 30;
  /** Each change blends the old preset into the new one over this long. */
  const BLEND_SECONDS = 2.7;

  class PresetCycle {
    constructor(names, { intervalMs = PRESET_SECONDS * 1000, random = Math.random } = {}) {
      this.names = [...names];
      this.intervalMs = intervalMs;
      this.random = random;
      this.round = [];
      this.current = null;
      this.shownMs = 0;
      this.failed = [];
    }

    next() {
      if (!this.names.length) return null;
      if (!this.round.length) {
        this.round = [...this.names];
        for (let i = this.round.length - 1; i > 0; i--) {
          const j = Math.floor(this.random() * (i + 1));
          [this.round[i], this.round[j]] = [this.round[j], this.round[i]];
        }
        if (this.round.length > 1 && this.round[this.round.length - 1] === this.current) this.round.unshift(this.round.pop());
      }
      this.current = this.round.pop();
      this.shownMs = 0;
      return this.current;
    }

    /** The next preset `load(name)` takes; one it throws on is counted in `failed` and left out from then on. Null when none loads. */
    load(load) {
      // A preset that does not compile was never shown: the next round must not start with the one still on screen.
      const shown = this.current;
      while (this.names.length) {
        const name = this.next();
        try {
          load(name);
          return name;
        } catch {
          this.current = shown;
          this.failed.push(name);
          this.names = this.names.filter((n) => n !== name);
          this.round = this.round.filter((n) => n !== name);
        }
      }
      return null;
    }

    /** `ms` more of the picture showing; true when the next preset is due. */
    advance(ms) {
      this.shownMs += Math.max(0, Math.min(ms, 1000));
      return this.shownMs >= this.intervalMs;
    }
  }

  return { PresetCycle, PRESET_SECONDS, BLEND_SECONDS };
});

// Match the lightshow screen mix of RGB, white, amber and UV; scale overflow to preserve hue.
// Shared by CommonJS consumers and browser PartyVisuals.
(function (root, factory) {
  const api = factory();
  if (typeof module === 'object' && module.exports) module.exports = api;
  else root.PartyVisuals = Object.assign(root.PartyVisuals || {}, api);
})(typeof globalThis !== 'undefined' ? globalThis : this, function () {
  'use strict';

  // The lightshow's palettes hold at most eight colours.
  const MAX_COLOURS = 8;

  // Fallback until the lightshow sends a palette.
  const DEFAULT_PALETTE = Object.freeze(['#FF0096', '#00E1FF', '#4B00FF', '#FF9C00']);

  const HEX = /^#([0-9a-f]{6})([0-9a-f]{2})?$/i;

  const byte = (v) => (Number.isFinite(v) ? Math.max(0, Math.min(255, v)) : 0);
  const hex2 = (v) => Math.round(v).toString(16).padStart(2, '0').toUpperCase();

  /** A colour with emitters, mixed for a screen. */
  function mixToHex({ r, g, b, w = 0, a = 0, uv = 0 }) {
    let rr = byte(r) + byte(w) + byte(a) + byte(uv) * 0.2;
    let gg = byte(g) + byte(w) + byte(a) * 0.5;
    let bb = byte(b) + byte(w) + byte(uv) * 0.9;
    const peak = Math.max(rr, gg, bb);
    if (peak > 255) {
      const k = 255 / peak;
      rr *= k; gg *= k; bb *= k;
    }
    return `#${hex2(rr)}${hex2(gg)}${hex2(bb)}`;
  }

  /**
   * One colour as `#RRGGBB`, or null. Takes the lightshow's hex (`#RRGGBB`,
   * or `#RRGGBBWW` with a white channel), a colour object, or an index into
   * its colour presets (how the look's four slots are sent).
   */
  function toHex(value, presets) {
    if (typeof value === 'string') {
      const m = HEX.exec(value.trim());
      if (!m) return null;
      const n = parseInt(m[1], 16);
      const w = m[2] ? parseInt(m[2], 16) : 0;
      return mixToHex({ r: (n >> 16) & 255, g: (n >> 8) & 255, b: n & 255, w });
    }
    if (Number.isInteger(value)) {
      const preset = Array.isArray(presets) ? presets[value] : undefined;
      return preset ? toHex(preset) : null;
    }
    if (value && typeof value === 'object' && ['r', 'g', 'b'].every((k) => Number.isFinite(value[k]))) {
      return mixToHex(value);
    }
    return null;
  }

  /** A list of colours as distinct hex, in order, or null when none is usable. */
  function paletteFrom(list, presets) {
    if (!Array.isArray(list)) return null;
    const out = [];
    for (const item of list) {
      const hex = toHex(item, presets);
      if (hex && !out.includes(hex)) out.push(hex);
      if (out.length === MAX_COLOURS) break;
    }
    return out.length ? out : null;
  }

  /** Override, look colours, or fallback; never empty. */
  function effectivePalette({ override, palette }) {
    const fromOverride = paletteFrom(override);
    if (fromOverride) return { colours: fromOverride, source: 'override' };
    const fromLook = paletteFrom(palette);
    if (fromLook) return { colours: fromLook, source: 'look' };
    return { colours: DEFAULT_PALETTE, source: 'default' };
  }

  return { DEFAULT_PALETTE, MAX_COLOURS, toHex, paletteFrom, effectivePalette };
});

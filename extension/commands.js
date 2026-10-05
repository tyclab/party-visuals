'use strict';
/**
 * The graphics' switches, and the commands that set them. One vocabulary for
 * the dashboard, Companion and other bundles, in EclipseGraphics' form: a
 * target and a `<property>.<value>`.
 *
 *   target   wash | bar | all
 *   air.on · air.off · air.toggle
 *   intensity.<0-100> · intensity.+<n> · intensity.-<n>
 */

const TARGETS = ['wash', 'bar'];

const DEFAULT_CONTROLS = Object.freeze({
  wash: Object.freeze({ on: true, intensity: 50 }),
  bar: Object.freeze({ on: true, intensity: 80 }),
});

const clampPercent = (n) => Math.max(0, Math.min(100, Math.round(n)));
const quote = (v) => JSON.stringify(String(v ?? '').slice(0, 60));

/** The controls with every field present and in range (a stored value may be from an older version). */
function normaliseControls(value) {
  const out = {};
  for (const t of TARGETS) {
    const given = value && typeof value === 'object' && value[t] && typeof value[t] === 'object' ? value[t] : {};
    out[t] = {
      on: typeof given.on === 'boolean' ? given.on : DEFAULT_CONTROLS[t].on,
      intensity: Number.isFinite(given.intensity) ? clampPercent(given.intensity) : DEFAULT_CONTROLS[t].intensity,
    };
  }
  return out;
}

/** Apply a command: `{ ok: true, controls }` with new controls, or `{ ok: false, error }`. */
function runCommand(controls, target, cmd) {
  const targets = target === 'all' ? TARGETS : TARGETS.includes(target) ? [target] : null;
  if (!targets) return { ok: false, error: `unknown target ${quote(target)}: wash, bar or all` };
  const m = /^(air|intensity)\.(.+)$/.exec(typeof cmd === 'string' ? cmd : '');
  const unknown = { ok: false, error: `unknown command ${quote(cmd)}: air.on, air.off, air.toggle, intensity.<0-100>, intensity.+<n> or intensity.-<n>` };
  if (!m) return unknown;

  const next = normaliseControls(controls);
  for (const t of targets) {
    const g = next[t];
    if (m[1] === 'air') {
      if (m[2] === 'on') g.on = true;
      else if (m[2] === 'off') g.on = false;
      else if (m[2] === 'toggle') g.on = !g.on;
      else return unknown;
    } else {
      const v = /^([+-]?)(\d{1,3})$/.exec(m[2]);
      if (!v) return unknown;
      const n = Number(v[2]);
      if (!v[1] && n > 100) return { ok: false, error: `intensity ${n} is past 100` };
      g.intensity = v[1] ? clampPercent(g.intensity + (v[1] === '+' ? n : -n)) : n;
    }
  }
  return { ok: true, controls: next };
}

module.exports = { TARGETS, DEFAULT_CONTROLS, normaliseControls, runCommand };

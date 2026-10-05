/**
 * What a party graphic reads each frame: the beat (extrapolated here, from
 * the extension's last reading), whether the lightshow is live, the palette
 * and the graphic's switches. All of it comes from Replicants, so a browser
 * source refreshed mid-show comes back exactly as it was.
 */
(function () {
  'use strict';
  const PV = window.PartyVisuals;
  const config = (window.nodecg && nodecg.bundleConfig && nodecg.bundleConfig.graphics) || {};
  const clampInt = (v, lo, hi, d) => (Number.isFinite(v) ? Math.max(lo, Math.min(hi, Math.round(v))) : d);

  const DEFAULT_CONTROLS = { wash: { on: true, intensity: 50 }, bar: { on: true, intensity: 80 } };
  const names = ['beatPos', 'palette', 'paletteOverride', 'connection', 'controls'];
  const reps = {};
  for (const name of names) reps[name] = nodecg.Replicant(name);

  const clock = new PV.BeatClock();
  reps.beatPos.on('change', (value) => {
    if (!value || !Number.isFinite(value.beat) || !(value.at > 0)) return;
    // The reading is from the extension's clock; on the show PC that is this
    // machine's, so the time it took to get here is known. Clamped, in case
    // this page runs somewhere whose clock disagrees.
    const delayMs = Math.max(0, Math.min(1000, Date.now() - value.at));
    clock.update({
      bpm: value.bpm,
      beat: value.beat + delayMs * value.bpm / 60000,
      epoch: value.epoch,
      atMs: performance.now(),
    });
  });

  const offsetMs = clampInt(config.offsetMs, -1000, 1000, 0);

  window.PartyFeed = {
    maxFps: clampInt(config.maxFps, 10, 60, 30),
    ready: NodeCG.waitForReplicants(...names.map((n) => reps[n])),

    read(nowMs) {
      const connection = reps.connection.value || {};
      const controls = reps.controls.value || DEFAULT_CONTROLS;
      return {
        beat: clock.started ? clock.at(nowMs + offsetMs) : null,
        bpm: clock.bpm || 120,
        live: connection.status === 'connected' && clock.started,
        palette: PV.effectivePalette({ override: reps.paletteOverride.value, palette: reps.palette.value }).colours,
        controls,
      };
    },
  };
})();

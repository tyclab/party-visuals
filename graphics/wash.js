/**
 * The colour wash: the whole frame in the lightshow's palette, to sit behind
 * cards. The colours turn and flow with the beat position; each pulse lifts
 * the wash a little and swells a glow in the middle. The steady part never
 * goes away, so a pulse is a lift, not a flash from nothing.
 */
(function () {
  'use strict';
  const { rgba, along, run } = window.PartyDraw;

  // One turn of the gradient every 64 beats; the colours move one place every 8.
  const TURN_BEATS = 64;
  const FLOW_BEATS = 8;
  const STOPS = 6;

  run('wash', (ctx, f) => {
    const { width: w, height: h, palette, motion, pulse, level } = f;
    const n = palette.length;
    const flow = motion / FLOW_BEATS;

    const angle = (motion / TURN_BEATS) * Math.PI * 2;
    const r = Math.hypot(w, h) / 2;
    const dx = Math.cos(angle) * r;
    const dy = Math.sin(angle) * r;
    const wash = ctx.createLinearGradient(w / 2 - dx, h / 2 - dy, w / 2 + dx, h / 2 + dy);
    const alpha = level * (0.45 + 0.25 * pulse);
    for (let i = 0; i < STOPS; i++) {
      wash.addColorStop(i / (STOPS - 1), rgba(along(palette, flow + (i * n) / (STOPS - 1)), alpha));
    }
    ctx.fillStyle = wash;
    ctx.fillRect(0, 0, w, h);

    if (pulse > 0.002) {
      const glow = ctx.createRadialGradient(w / 2, h / 2, 0, w / 2, h / 2, h * 0.75);
      const c = along(palette, flow + n / 2);
      glow.addColorStop(0, rgba(c, 0.35 * level * pulse));
      glow.addColorStop(1, rgba(c, 0));
      ctx.fillStyle = glow;
      ctx.fillRect(0, 0, w, h);
    }
  });
})();

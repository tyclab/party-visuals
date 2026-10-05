/**
 * Drawing helpers shared by the party graphics, and the loop that runs one:
 * a frame only when the frame-rate cap allows, the switch on and off eased
 * so a graphic never pops in or out in one frame.
 */
(function () {
  'use strict';
  const PV = window.PartyVisuals;

  function rgb(hex) {
    const n = parseInt(hex.slice(1, 7), 16);
    return [(n >> 16) & 255, (n >> 8) & 255, n & 255];
  }

  function rgba(c, a) {
    return `rgba(${c[0]},${c[1]},${c[2]},${Math.max(0, Math.min(1, a)).toFixed(3)})`;
  }

  /** The palette read as a loop: position 0 is the first colour, 1 the next, blended between. */
  function along(palette, position) {
    const n = palette.length;
    const i = ((Math.floor(position) % n) + n) % n;
    const t = position - Math.floor(position);
    // Smoothstep, so a colour lingers before it moves on.
    const s = t * t * (3 - 2 * t);
    const a = rgb(palette[i]);
    const b = rgb(palette[(i + 1) % n]);
    return [0, 1, 2].map((k) => Math.round(a[k] + (b[k] - a[k]) * s));
  }

  /**
   * Run `paint(ctx, frame)` for the graphic named `name` ('wash' or 'bar').
   * `frame`: { level (0..1, switch and intensity), pulse, motion, live,
   * palette, width, height }.
   */
  function run(name, paint) {
    const canvas = document.getElementById('stage');
    const ctx = canvas.getContext('2d');
    const feed = window.PartyFeed;
    const limiter = new PV.FrameLimiter(feed.maxFps);
    const beat = new PV.BeatVisual();
    const shown = new PV.Ease(400, 0);

    function tick(now) {
      requestAnimationFrame(tick);
      if (!limiter.ready(now)) return;
      const s = feed.read(now);
      const control = s.controls[name] || { on: false, intensity: 0 };
      const f = beat.frame({ nowMs: now, beat: s.beat, bpm: s.bpm, live: s.live });
      const level = shown.step(control.on ? 1 : 0, now) * control.intensity / 100;
      ctx.clearRect(0, 0, canvas.width, canvas.height);
      if (level <= 0.001) return;
      paint(ctx, { level, pulse: f.pulse, motion: f.motion, live: f.live, palette: s.palette, width: canvas.width, height: canvas.height });
    }

    feed.ready.then(() => requestAnimationFrame(tick));
  }

  window.PartyDraw = { rgb, rgba, along, run };
})();

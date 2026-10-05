/**
 * The beat bar: a band of the lightshow's colours across the bottom of the
 * frame (`?position=top` for the top), scrolling half a block a beat and
 * swelling and brightening on each pulse. When the lightshow goes away it
 * stops where it is and the swell dies down.
 */
(function () {
  'use strict';
  const { rgb, rgba, run } = window.PartyDraw;

  const top = new URLSearchParams(location.search).get('position') === 'top';
  const BLOCK = 160;
  const HEIGHT = 56;
  const SWELL = 14;
  const MARGIN = 72;

  run('bar', (ctx, f) => {
    const { width: w, height: h, palette, motion, pulse, level } = f;
    const n = palette.length;
    const barHeight = HEIGHT + SWELL * pulse;
    const middle = top ? MARGIN + HEIGHT / 2 : h - MARGIN - HEIGHT / 2;
    const y = middle - barHeight / 2;

    const scroll = (motion * BLOCK) / 2;
    const first = Math.floor(scroll / BLOCK);
    const x0 = -(scroll - first * BLOCK);

    ctx.save();
    ctx.shadowBlur = 24 * pulse;
    for (let i = 0, x = x0; x < w; i++, x += BLOCK) {
      const c = rgb(palette[(((first + i) % n) + n) % n]);
      ctx.shadowColor = rgba(c, level);
      ctx.fillStyle = rgba(c, 0.8 * level);
      ctx.fillRect(x + 2, y, BLOCK - 4, barHeight);
    }
    ctx.restore();

    // The brightening, over the whole band at once.
    if (pulse > 0.002) {
      ctx.fillStyle = rgba([255, 255, 255], 0.3 * level * pulse);
      ctx.fillRect(0, y, w, barHeight);
    }
  });
})();

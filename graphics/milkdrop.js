/**
 * The live-audio visualizer: a MilkDrop picture drawn from the lightshow's live input, which this
 * bundle's extension relays (api/samples). With no sound to draw it fades out and says what it waits
 * for; the browser helper reads that fade as idle and releases the lights it streams to. What it asks
 * for, keeps and may draw is shared/samples.js's LivePageFeed, the code the timing test runs.
 */
(function () {
  'use strict';
  const PV = window.PartyVisuals;
  const view = document.querySelector('[data-party-visualizer]');
  const layer = view.querySelector('[data-party-visualizer-layer]');
  const canvas = view.querySelector('[data-party-visualizer-canvas]');
  const status = view.querySelector('[role="status"]');
  const graphics = (window.nodecg && window.nodecg.bundleConfig && window.nodecg.bundleConfig.graphics) || {};
  const presetSeconds = Number.isFinite(graphics.presetSeconds) ? Math.max(10, Math.min(600, graphics.presetSeconds)) : PV.PRESET_SECONDS;
  const FADE_MS = 1000, RETRY_MS = 1000, AGE_EVERY_MS = 1000;
  // A lost graphics context is not restored in place: the page loads again, at once or when it gets its context back.
  const RELOAD_AFTER_LOSS_MS = 3000, RELOAD_WITHOUT_WEBGL_MS = 30_000;
  const SAY = { webgl: 'This browser cannot draw the visualizer (no WebGL 2)', context: 'The graphics context was lost; reloading',
    nodecg: 'No connection to NodeCG', busy: 'Too many live-audio pages are open', endpoint: 'The samples endpoint refused this page',
    lightshow: 'No connection to the lightshow', 'live-input': 'The lightshow has no live input', audio: 'No audio from the lightshow', silence: 'Silence' };

  const feed = new PV.LivePageFeed();
  let wait = 'audio', link = null, lost = false;
  const pause = (ms) => new Promise((resolve) => setTimeout(resolve, ms));

  async function pull() {
    for (;;) {
      const asked = performance.now(), abort = new AbortController(), timer = setTimeout(() => abort.abort(), PV.PULL_TIMEOUT_MS);
      try {
        const response = await fetch(`../api/samples${feed.query()}`, { cache: 'no-store', signal: abort.signal });
        if (!response.ok) link = response.status === 503 ? 'busy' : 'endpoint';
        else {
          const body = await response.json().catch(() => null);
          link = body && typeof body === 'object' ? null : 'endpoint';
          if (!link) {
            wait = PV.WAITS.includes(body.wait) ? body.wait : null;
            feed.take(body, performance.now(), asked);
          }
        }
      } catch {
        link = 'nodecg';
      } finally {
        clearTimeout(timer);
      }
      if (link) await pause(RETRY_MS);
    }
  }

  function size() {
    const width = Math.max(1, Math.round(window.innerWidth)), height = Math.max(1, Math.round(window.innerHeight));
    if (canvas.width === width && canvas.height === height) return false;
    canvas.width = width;
    canvas.height = height;
    return true;
  }

  function createVisualizer() {
    try {
      size();
      // The browser helper copies this canvas from its own frames: keep each picture until the next replaces it.
      const gl = canvas.getContext('webgl2', { alpha: false, antialias: false, depth: false, stencil: false, premultipliedAlpha: false, preserveDrawingBuffer: true });
      if (!gl || !window.butterchurn) return null;
      return window.butterchurn.default.createVisualizer(null, canvas, { width: canvas.width, height: canvas.height, pixelRatio: 1, textureRatio: 1 });
    } catch {
      return null;
    }
  }

  canvas.addEventListener('webglcontextlost', (event) => {
    event.preventDefault();
    lost = true;
    // Hidden at once, not faded: the helper must not stream a blank canvas to the lights.
    layer.style.transition = 'none';
    layer.style.opacity = '0';
    setTimeout(() => window.location.reload(), RELOAD_AFTER_LOSS_MS);
  });
  canvas.addEventListener('webglcontextrestored', () => window.location.reload());

  const visualizer = createVisualizer();
  const presets = (window.butterchurnPresets && window.butterchurnPresets.getPresets()) || {};
  const cycle = new PV.PresetCycle(Object.keys(presets), { intervalMs: presetSeconds * 1000 });
  function nextPreset(blendSeconds) {
    for (let tries = 0; tries < cycle.names.length; tries++) {
      try { visualizer.loadPreset(presets[cycle.next()], blendSeconds); return; } catch { /* one this renderer cannot compile: the next */ }
    }
  }
  if (visualizer) nextPreset(0);
  else {
    canvas.width = canvas.height = 0;
    setTimeout(() => window.location.reload(), RELOAD_WITHOUT_WEBGL_MS);
  }

  const bytes = new Uint8Array(1024), silence = new Uint8Array(1024).fill(128);
  let last = performance.now(), hiddenAt = -Infinity, agedAt = -Infinity, said;
  function frame(now) {
    requestAnimationFrame(frame);
    const reason = !visualizer ? 'webgl' : lost ? 'context' : link || wait, shown = reason === null, dt = now - last;
    last = now;
    if (reason !== said) {
      said = reason;
      status.textContent = reason ? SAY[reason] : '';
      if (reason) view.setAttribute('data-party-wait', reason); else view.removeAttribute('data-party-wait');
      if (!lost) layer.style.opacity = shown ? '1' : '0';
      if (!shown) hiddenAt = now;
    }
    if (now - agedAt >= AGE_EVERY_MS) {
      agedAt = now;
      const ages = feed.ageStats(now);
      if (ages) view.setAttribute('data-party-age', `${Math.round(ages.medianMs)},${Math.round(ages.worstMs)}`);
      else view.removeAttribute('data-party-age');
    }
    if (!visualizer || lost || (!shown && now - hiddenAt > FADE_MS)) return;
    if (size()) visualizer.setRendererSize(canvas.width, canvas.height);
    if (shown && cycle.advance(dt)) nextPreset(PV.BLEND_SECONDS);
    const audio = (shown && feed.window(now, bytes, Date.now())) || silence;
    visualizer.render({ audioLevels: { timeByteArray: audio, timeByteArrayL: audio, timeByteArrayR: audio } });
  }

  pull();
  requestAnimationFrame(frame);
})();

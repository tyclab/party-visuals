/**
 * The live-audio visualizer: a MilkDrop picture drawn from the lightshow's live input, which this
 * bundle's extension relays (api/samples). With no sound to draw it fades out and says what it waits
 * for; the browser helper reads that fade as idle and releases the lights it streams to.
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
  const FADE_MS = 1000;
  const SAY = { nodecg: 'No connection to NodeCG', lightshow: 'No connection to the lightshow', 'live-input': 'The lightshow has no live input',
    audio: 'No audio from the lightshow', silence: 'Silence', webgl: 'This browser cannot draw the visualizer (no WebGL 2)' };

  const ring = new PV.SampleRing();
  let wait = 'audio', link = true;

  function decode(text) {
    const raw = atob(text || ''), out = new Int16Array(raw.length >> 1);
    for (let i = 0; i < out.length; i++) out[i] = ((raw.charCodeAt(2 * i) | (raw.charCodeAt(2 * i + 1) << 8)) << 16) >> 16;
    return out;
  }

  async function pull() {
    for (;;) {
      try {
        const response = await fetch(`../api/samples${ring.g === null ? '' : `?g=${ring.g}&after=${ring.end}`}`, { cache: 'no-store' });
        if (!response.ok) throw new Error(`HTTP ${response.status}`);
        const body = await response.json();
        link = true;
        wait = PV.WAITS.includes(body.wait) ? body.wait : null;
        const samples = decode(body.data);
        // Behind its quickest delivery by the extension's count plus the trip here, on this machine's clock.
        if (samples.length && Number.isFinite(body.ageMs)) {
          ring.take({ g: body.g, end: body.end, rate: body.rate, samples }, performance.now(), body.ageMs + Math.max(0, Date.now() - body.sentAt));
        }
      } catch {
        link = false;
        await new Promise((resolve) => setTimeout(resolve, 1000));
      }
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

  const visualizer = createVisualizer();
  const presets = (window.butterchurnPresets && window.butterchurnPresets.getPresets()) || {};
  const cycle = new PV.PresetCycle(Object.keys(presets), { intervalMs: presetSeconds * 1000 });
  function nextPreset(blendSeconds) {
    for (let tries = 0; tries < cycle.names.length; tries++) {
      try { visualizer.loadPreset(presets[cycle.next()], blendSeconds); return; } catch { /* one this renderer cannot compile: the next */ }
    }
  }
  if (visualizer) nextPreset(0);
  else canvas.width = canvas.height = 0;

  const bytes = new Uint8Array(1024), silence = new Uint8Array(1024).fill(128);
  let last = performance.now(), hiddenAt = -Infinity, said;
  function frame(now) {
    requestAnimationFrame(frame);
    const reason = !visualizer ? 'webgl' : !link ? 'nodecg' : wait, shown = reason === null, dt = now - last;
    last = now;
    if (reason !== said) {
      said = reason;
      status.textContent = reason ? SAY[reason] : '';
      if (reason) view.setAttribute('data-party-wait', reason); else view.removeAttribute('data-party-wait');
      layer.style.opacity = shown ? '1' : '0';
      if (!shown) hiddenAt = now;
    }
    if (!visualizer || (!shown && now - hiddenAt > FADE_MS)) return;
    if (size()) visualizer.setRendererSize(canvas.width, canvas.height);
    if (shown && cycle.advance(dt)) nextPreset(PV.BLEND_SECONDS);
    const audio = shown && ring.ageMs(now) <= PV.MAX_AGE_MS ? PV.timeBytes(ring.since(-Infinity, 512).samples, bytes) : silence;
    visualizer.render({ audioLevels: { timeByteArray: audio, timeByteArrayL: audio, timeByteArrayR: audio } });
  }

  pull();
  requestAnimationFrame(frame);
})();

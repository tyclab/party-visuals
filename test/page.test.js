'use strict';
// The live-audio page in headless Chromium: the real graphics/milkdrop.html, its scripts and the
// renderer from node_modules served under the paths NodeCG serves them at, and the real extension's
// api/samples fed by the mock lightshow and its relay (helpers/mock-relay.js). Every request outside the
// page's own origin is blocked and recorded. Needs a Chromium: `npx playwright-core install
// chromium-headless-shell`, or PLAYWRIGHT_BROWSERS_PATH. A host whose headless shell has no WebGL 2 sets
// PARTY_VISUALS_BROWSER_CHANNEL=chromium and PARTY_VISUALS_BROWSER_ARGS (space-separated launch flags).
const test = require('node:test');
const { before, after } = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const http = require('node:http');
const os = require('node:os');
const path = require('node:path');
const { EventEmitter } = require('node:events');
const { chromium } = require('playwright-core');
const { startMockLightshow } = require('./helpers/mock-lightshow.js');
const extension = require('../extension/index.js');

const ROOT = path.join(__dirname, '..'), TOKEN = 'mock-page-token-23', RATE = 22050, HOP = 256;
const TYPES = { '.html': 'text/html', '.js': 'text/javascript', '.css': 'text/css', '.svg': 'image/svg+xml' };

async function until(check, what, timeoutMs = 10_000) {
  let last;
  for (const end = Date.now() + timeoutMs; Date.now() < end;) {
    last = await check();
    if (last) return last;
    await new Promise((resolve) => setTimeout(resolve, 50));
  }
  throw new Error(`timed out waiting for ${what}`);
}

/** The live input at the pace of a sound card: a tone, silence, or a restart that counts from zero under the same name. */
function source(show) {
  const g = 1_800_000_000_000;
  let started = Date.now(), end = 0, tone = true;
  const timer = setInterval(() => {
    const due = Math.floor(((Date.now() - started) / 1000) * RATE / HOP) * HOP;
    while (end < due) {
      const data = Buffer.alloc(HOP * 2);
      for (let i = 0; i < HOP; i++) data.writeInt16LE(tone ? Math.round(12000 * Math.sin(((end + i) / RATE) * 2 * Math.PI * 220)) : 0, 2 * i);
      end += HOP;
      show.block({ g, end, rate: RATE, at: Date.now(), data });
    }
  }, 5);
  return {
    tone(on) { tone = on; },
    restart() { started = Date.now(); end = 0; },
    stop() { clearInterval(timer); },
  };
}

/** The extension under the parts of NodeCG's API it uses, and the bundle's files at NodeCG's paths; `samples(req, res)`, when set, answers api/samples instead. */
async function serveBundle(show) {
  const dir = fs.mkdtempSync(path.join(os.tmpdir(), 'party-visuals-page-'));
  fs.writeFileSync(path.join(dir, 'token'), `${TOKEN}\n`);
  const events = new EventEmitter();
  let router = null;
  extension({
    bundleConfig: { lightshow: { url: show.url, tokenFile: path.join(dir, 'token'), reconnect: { minMs: 50, maxMs: 400 } } },
    log: Object.fromEntries(['info', 'warn', 'error', 'debug', 'trace'].map((level) => [level, () => {}])),
    Replicant: (name, opts = {}) => ({ value: structuredClone(opts.defaultValue) }),
    listenFor() {}, sendMessage() {},
    Router: () => { const handlers = []; return { use: (fn) => handlers.push(fn), handlers }; },
    mount: (prefix, mounted) => { router = mounted; },
    on: (event, fn) => events.on(event, fn),
  });
  const bundle = { samples: null };
  const server = http.createServer((req, res) => {
    const url = new URL(req.url, 'http://bundle.invalid'), prefix = '/bundles/party-visuals';
    const notFound = () => { res.statusCode = 404; res.end(); };
    if (!url.pathname.startsWith(`${prefix}/`)) return notFound();
    const rest = url.pathname.slice(prefix.length);
    if (rest === '/api/samples' && bundle.samples) return bundle.samples(req, res);
    if (rest.startsWith('/api/')) {
      req.url = rest + url.search;
      return router.handlers[0](req, res, notFound);
    }
    const [, top] = rest.split('/');
    const file = path.join(ROOT, decodeURIComponent(rest));
    if (!['graphics', 'shared', 'node_modules'].includes(top) || !file.startsWith(path.join(ROOT, top)) || !fs.existsSync(file)) return notFound();
    res.setHeader('content-type', TYPES[path.extname(file)] ?? 'application/octet-stream');
    return res.end(fs.readFileSync(file));
  });
  await new Promise((resolve) => server.listen(0, '127.0.0.1', resolve));
  return Object.assign(bundle, { origin: `http://127.0.0.1:${server.address().port}`, async close() {
    events.emit('serverStopping');
    server.closeAllConnections();
    await new Promise((resolve) => server.close(resolve));
    fs.rmSync(dir, { recursive: true, force: true });
  } });
}

// For each frame: how far the samples handed to the renderer swing from silence (128), and the renderer's own
// band energy computed from them (what presets read as bass, mid and treble), taken after the frame.
function recordRenders() {
  let real;
  Object.defineProperty(window, 'butterchurn', { configurable: true, get: () => real, set(value) {
    const create = value.default.createVisualizer.bind(value.default);
    value.default.createVisualizer = (...args) => {
      const visualizer = create(...args), render = visualizer.render.bind(visualizer);
      visualizer.render = (options) => {
        const bytes = options && options.audioLevels && options.audioLevels.timeByteArray;
        const result = render(options);
        if (bytes) {
          let swing = 0;
          for (const b of bytes) swing = Math.max(swing, Math.abs(b - 128));
          const levels = visualizer.renderer && visualizer.renderer.audioLevels;
          (window.__frames = window.__frames || []).push({ swing, energy: levels ? levels.imm[0] + levels.imm[1] + levels.imm[2] : NaN });
        }
        return result;
      };
      return visualizer;
    };
    real = value;
  } });
}

const shared = {};
before(async () => {
  shared.show = await startMockLightshow({ token: TOKEN, state: { live: { running: true, listening: true, quiet: false } } });
  shared.sound = source(shared.show);
  shared.bundle = await serveBundle(shared.show);
  const args = (process.env.PARTY_VISUALS_BROWSER_ARGS || '').split(' ').filter(Boolean);
  shared.browser = await chromium.launch({ args, channel: process.env.PARTY_VISUALS_BROWSER_CHANNEL || undefined });
});
after(async () => {
  await shared.browser?.close();
  shared.sound?.stop();
  await shared.bundle?.close();
  await shared.show?.close();
});

/** A fresh page on the bundle, every request outside its origin blocked and recorded; `script` runs before the page's own. */
async function openPage(t, script) {
  const { origin } = shared.bundle;
  // Small, so a software renderer on CI keeps its frames short enough for samples to stay fresh.
  const context = await shared.browser.newContext({ viewport: { width: 320, height: 180 } });
  t.after(() => context.close());
  const outside = [], errors = [];
  await context.route('**/*', (route) => {
    if (new URL(route.request().url()).origin === origin) return route.continue();
    outside.push(route.request().url());
    return route.abort();
  });
  await context.addInitScript(recordRenders);
  if (script) await context.addInitScript(script);
  const page = await context.newPage();
  page.on('pageerror', (error) => errors.push(String(error)));
  const look = (since = 0) => page.evaluate((from) => {
    const view = document.querySelector('[data-party-visualizer]'), frames = (window.__frames || []).slice(from);
    return { wait: view.getAttribute('data-party-wait'), age: view.getAttribute('data-party-age'), frames: (window.__frames || []).length,
      loud: Math.max(0, ...frames.map((f) => f.swing)), energy: Math.max(0, ...frames.map((f) => f.energy)),
      silentFrames: frames.filter((f) => f.swing === 0 && f.energy === 0).length, failures: view.getAttribute('data-party-preset-failures'),
      canvas: document.querySelector('[data-party-visualizer-canvas]').width,
      opacity: Number(getComputedStyle(document.querySelector('[data-party-visualizer-layer]')).opacity), status: document.querySelector('[role="status"]').textContent };
  }, since);
  // Each test needs a Chromium that draws WebGL 2; without one it says so instead of failing on the page's `webgl`.
  const open = async () => {
    await page.goto(`${origin}/bundles/party-visuals/graphics/milkdrop.html`);
    assert.equal(await page.evaluate(() => !!document.createElement('canvas').getContext('webgl2')), true,
      'this Chromium draws no WebGL 2: see PARTY_VISUALS_BROWSER_CHANNEL in this file');
  };
  return { page, look, outside, errors, open };
}

test('the live-audio page draws from the live input, goes calm on silence and on a refused or hung endpoint, survives a lost context and a restart, and asks nothing beyond its origin', { timeout: 120_000 }, async (t) => {
  const { sound } = shared;
  const { page, look, outside, errors, open } = await openPage(t);
  await open();
  // Any render since `count` handed loud samples, and the renderer's own band energy rose with them. On a slow
  // renderer many frames get silence, the bound holding, so not every frame has to.
  const loudAfter = (count) => until(async () => { const now = await look(count); return now.frames > count + 10 && now.loud > 30 && now.energy > 1 && now; },
    'loud samples handed to the renderer, and its band energy up');

  // Requirement 1: the picture from the room's sound. The renderer's band energy is what presets read; how the
  // picture looks is the operator's to judge, and presets also move by time, so pixels are not compared here.
  const live = await until(async () => { const now = await look(); return now.wait === null && now.opacity === 1 && now; }, 'the picture showing');
  assert.equal(live.status, '');
  await loudAfter(0);
  // Read while fresh samples are being drawn: only those frames feed the measure.
  const age = (await until(async () => (await look()).age, 'the age measure')).split(',').map(Number);
  assert.ok(age.every((ms) => ms >= 0 && ms < 2000), `newest drawn sample ${age} ms old against the stamp`);

  // Calm on silence, and back with the sound.
  const quietFrom = (await look()).frames;
  sound.tone(false);
  await until(async () => { const now = await look(); return now.wait === 'silence' && now.status === 'Silence' && now; }, 'silence said', 8000);
  assert.ok((await look(quietFrom)).silentFrames > 0, 'fed silence, the renderer got flat samples and its band energy fell to zero');
  await until(async () => (await look()).opacity === 0, 'the picture faded out', 3000);
  sound.tone(true);
  const back = await until(async () => { const now = await look(); return now.wait === null && now; }, 'sound again', 3000);
  await until(async () => (await look()).opacity === 1, 'the picture faded in', 3000);
  await loudAfter(back.frames);

  // A restarted lightshow that counts from zero under the same name.
  sound.restart();
  // Past the old stream's last fresh window, so only the new count can be loud from here.
  await page.waitForTimeout(500);
  await loudAfter((await look()).frames);

  // Its own reason for a refused, an erroring, a garbled or a hung samples endpoint, and back when it answers again.
  // A body cut short after its headers, by a reset or by the page's own two-second abort, is the connection's fault.
  const cut = (end) => (req, res) => {
    res.writeHead(200, { 'content-type': 'application/json', 'content-length': '4096' });
    res.write('{"wait":null,"g":1,');
    end(res);
  };
  const json = { status: 200, contentType: 'application/json' };
  for (const [wait, why, answer] of [
    ['busy', 'a refusal', { route: (route) => route.fulfill({ status: 503, body: '{}' }) }],
    ['endpoint', 'an error', { route: (route) => route.fulfill({ status: 500, body: 'no' }) }],
    ['data', 'garbled samples', { route: (route) => route.fulfill({ ...json, body: JSON.stringify({ wait: null, g: 1, end: 9, rate: 22050, ageMs: 1, heldMs: 0, data: '@@@' }) }) }],
    ['data', 'a whole body that is not JSON', { route: (route) => route.fulfill({ ...json, body: '{"wait":null,' }) }],
    ['nodecg', 'no answer', { route: () => {} }],
    ['nodecg', 'a body reset after its headers', { samples: cut((res) => setTimeout(() => res.destroy(), 100)) }],
    ['nodecg', 'a body held past the pull timeout', { samples: cut(() => {}) }],
  ]) {
    if (answer.route) await page.route('**/api/samples*', answer.route);
    shared.bundle.samples = answer.samples || null;
    const calm = await until(async () => { const now = await look(); return now.wait === wait && now; }, `${wait} said for ${why}`, 5000);
    assert.notEqual(calm.status, '');
    await until(async () => (await look()).opacity === 0, `the picture faded out for ${why}`, 3000);
    await page.unrouteAll({ behavior: 'ignoreErrors' });
    shared.bundle.samples = null;
    await until(async () => { const now = await look(); return now.wait === null && now.opacity === 1 && now; }, `back after ${why}`, 6000);
  }

  // A lost graphics context: hidden at once with its reason, then the page loads again by itself.
  const reloaded = page.waitForEvent('load', { timeout: 10_000 });
  await page.evaluate(() => document.querySelector('[data-party-visualizer-canvas]').getContext('webgl2').getExtension('WEBGL_lose_context').loseContext());
  const lost = await until(async () => { const now = await look(); return now.wait === 'context' && now; }, 'the lost context said', 1000);
  assert.equal(lost.opacity, 0, 'hidden as the reason shows, not faded: no blank canvas left showing for the lights');
  await reloaded;
  await until(async () => { const now = await look(); return now.wait === null && now.opacity === 1 && now; }, 'the picture back after the reload');
  await loudAfter(0);

  // Requirement 6: nothing asked of the internet, or of any other origin.
  assert.deepEqual(outside, []);
  assert.deepEqual(errors, []);
});

test('a renderer or preset pack that did not load, and presets that do not compile, each say so instead of "no WebGL 2"', { timeout: 60_000 }, async (t) => {
  const missing = async (file) => {
    const { page, look, open } = await openPage(t);
    await page.route(`**/node_modules/${file}`, (route) => route.abort());
    await open();
    return until(async () => { const now = await look(); return now.wait && now; }, `the page's reason without ${file}`, 5000);
  };
  const noRenderer = await missing('butterchurn/lib/butterchurn.min.js');
  assert.deepEqual([noRenderer.wait, noRenderer.canvas, noRenderer.status], ['renderer', 0, 'The renderer did not load; reloading in 30 s'],
    'no canvas, so the helper says the visualizer is unavailable');
  const noPack = await missing('butterchurn-presets/lib/butterchurnPresets.min.js');
  assert.deepEqual([noPack.wait, noPack.status], ['presets', 'No preset of the pack loaded; reloading in 30 s']);
  assert.ok(noPack.canvas > 0 && noPack.opacity === 0, 'a renderer without a preset: hidden, so the helper reads it as idle');

  // Two presets whose equations do not compile: each counted, and with none left the page says so.
  const { look, open } = await openPage(t, () => {
    let real;
    Object.defineProperty(window, 'butterchurnPresets', { configurable: true, get: () => real, set(value) {
      const broken = (name) => ({ name, baseVals: {}, shapes: [], waves: [], init_eqs_str: 'a.x = (;', frame_eqs_str: '', pixel_eqs_str: '' });
      real = { getPresets: () => ({ one: broken('one'), two: broken('two') }), pack: value };
    } });
  });
  await open();
  const broken = await until(async () => { const now = await look(); return now.wait === 'presets' && now; }, 'no preset compiled', 5000);
  assert.equal(broken.failures, '2', 'both failures counted in data-party-preset-failures');
});

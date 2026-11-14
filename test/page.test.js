'use strict';
// The live-audio page in headless Chromium: the real graphics/milkdrop.html, its scripts and the
// renderer from node_modules served under the paths NodeCG serves them at, and the real extension's
// api/samples fed by the mock lightshow and its relay (helpers/mock-relay.js). Every request outside the
// page's own origin is blocked and recorded. Needs a Chromium: `npx playwright-core install
// chromium-headless-shell`, or PLAYWRIGHT_BROWSERS_PATH. A host whose headless shell has no WebGL 2 sets
// PARTY_VISUALS_BROWSER_CHANNEL=chromium and PARTY_VISUALS_BROWSER_ARGS (space-separated launch flags).
const test = require('node:test');
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

/** The extension under the parts of NodeCG's API it uses, and the bundle's files at NodeCG's paths. */
async function serveBundle(t, show) {
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
  const server = http.createServer((req, res) => {
    const url = new URL(req.url, 'http://bundle.invalid'), prefix = '/bundles/party-visuals';
    const notFound = () => { res.statusCode = 404; res.end(); };
    if (!url.pathname.startsWith(`${prefix}/`)) return notFound();
    const rest = url.pathname.slice(prefix.length);
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
  t.after(async () => {
    events.emit('serverStopping');
    server.closeAllConnections();
    await new Promise((resolve) => server.close(resolve));
    fs.rmSync(dir, { recursive: true, force: true });
  });
  return `http://127.0.0.1:${server.address().port}`;
}

// Counts how far each frame's samples, as handed to the renderer, swing from silence (128).
function recordRenders() {
  let real;
  Object.defineProperty(window, 'butterchurn', { configurable: true, get: () => real, set(value) {
    const create = value.default.createVisualizer.bind(value.default);
    value.default.createVisualizer = (...args) => {
      const visualizer = create(...args), render = visualizer.render.bind(visualizer);
      visualizer.render = (options) => {
        const bytes = options && options.audioLevels && options.audioLevels.timeByteArray;
        if (bytes) {
          let swing = 0;
          for (const b of bytes) swing = Math.max(swing, Math.abs(b - 128));
          (window.__swings = window.__swings || []).push(swing);
        }
        return render(options);
      };
      return visualizer;
    };
    real = value;
  } });
}

test('the live-audio page draws from the live input, goes calm on silence, survives a lost context and a restart, and asks nothing beyond its origin', { timeout: 120_000 }, async (t) => {
  const show = await startMockLightshow({ token: TOKEN, state: { live: { running: true, listening: true, quiet: false } } });
  t.after(() => show.close());
  const sound = source(show);
  t.after(() => sound.stop());
  const origin = await serveBundle(t, show);
  const args = (process.env.PARTY_VISUALS_BROWSER_ARGS || '').split(' ').filter(Boolean);
  const browser = await chromium.launch({ args, channel: process.env.PARTY_VISUALS_BROWSER_CHANNEL || undefined });
  t.after(() => browser.close());
  const context = await browser.newContext({ viewport: { width: 640, height: 360 } });
  const outside = [];
  await context.route('**/*', (route) => {
    if (new URL(route.request().url()).origin === origin) return route.continue();
    outside.push(route.request().url());
    return route.abort();
  });
  await context.addInitScript(recordRenders);
  const page = await context.newPage();
  const errors = [];
  page.on('pageerror', (error) => errors.push(String(error)));
  await page.goto(`${origin}/bundles/party-visuals/graphics/milkdrop.html`);
  assert.equal(await page.evaluate(() => !!document.createElement('canvas').getContext('webgl2')), true,
    'this Chromium draws no WebGL 2: see PARTY_VISUALS_BROWSER_CHANNEL in this file');
  const look = () => page.evaluate(() => {
    const view = document.querySelector('[data-party-visualizer]'), swings = window.__swings || [];
    return { wait: view.getAttribute('data-party-wait'), age: view.getAttribute('data-party-age'), swings: swings.length, loud: Math.max(0, ...swings.slice(-20)),
      opacity: Number(getComputedStyle(document.querySelector('[data-party-visualizer-layer]')).opacity), status: document.querySelector('[role="status"]').textContent };
  });
  const loudAfter = (count) => until(async () => { const now = await look(); return now.swings > count + 10 && now.loud > 30 && now; }, 'loud samples handed to the renderer');

  // Requirement 1: the picture from the room's sound.
  const live = await until(async () => { const now = await look(); return now.wait === null && now.opacity === 1 && now; }, 'the picture showing');
  assert.equal(live.status, '');
  await loudAfter(0);
  const canvas = page.locator('[data-party-visualizer-canvas]');
  const first = await canvas.screenshot(), second = await (async () => { await page.waitForTimeout(400); return canvas.screenshot(); })();
  assert.notDeepEqual(first, second, 'the picture moves');
  const age = (await until(async () => (await look()).age, 'the age measure')).split(',').map(Number);
  assert.ok(age.every((ms) => ms >= 0 && ms < 2000), `newest drawn sample ${age} ms old against the stamp`);

  // Calm on silence, and back with the sound.
  sound.tone(false);
  await until(async () => { const now = await look(); return now.wait === 'silence' && now.status === 'Silence' && now; }, 'silence said', 8000);
  await until(async () => (await look()).opacity === 0, 'the picture faded out', 3000);
  sound.tone(true);
  const back = await until(async () => { const now = await look(); return now.wait === null && now; }, 'sound again', 3000);
  await until(async () => (await look()).opacity === 1, 'the picture faded in', 3000);
  await loudAfter(back.swings);

  // A restarted lightshow that counts from zero under the same name.
  const before = await look();
  sound.restart();
  await loudAfter(before.swings);

  // A lost graphics context: hidden at once with its reason, then the page loads again by itself.
  const reloaded = page.waitForEvent('load', { timeout: 10_000 });
  await page.evaluate(() => document.querySelector('[data-party-visualizer-canvas]').getContext('webgl2').getExtension('WEBGL_lose_context').loseContext());
  const lost = await look();
  assert.deepEqual([lost.wait, lost.opacity], ['context', 0], 'no blank canvas left showing for the lights');
  await reloaded;
  await until(async () => { const now = await look(); return now.wait === null && now.opacity === 1 && now; }, 'the picture back after the reload');
  await loudAfter(0);

  // Requirement 6: nothing asked of the internet, or of any other origin.
  assert.deepEqual(outside, []);
  assert.deepEqual(errors, []);
});

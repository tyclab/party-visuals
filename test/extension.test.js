'use strict';
const test = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const os = require('node:os');
const path = require('node:path');
const { EventEmitter } = require('node:events');
const { startMockLightshow } = require('./helpers/mock-lightshow.js');
const extension = require('../extension/index.js');

const TOKEN = 'mock-extension-token-42';

/** The parts of NodeCG's extension API the bundle uses, with hooks to look inside. */
function fakeNodecg(bundleConfig) {
  const replicants = new Map();
  const listeners = new Map();
  const events = new EventEmitter();
  const logs = [];
  const log = (level) => (...args) => logs.push(`${level} ${args.join(' ')}`);
  const nodecg = {
    bundleName: 'party-visuals',
    bundleConfig,
    log: { info: log('info'), warn: log('warn'), error: log('error'), debug: log('debug'), trace: log('trace') },
    Replicant(name, opts = {}) {
      if (!replicants.has(name)) replicants.set(name, { value: structuredClone(opts.defaultValue), opts });
      return replicants.get(name);
    },
    listenFor(name, fn) { listeners.set(name, fn); },
    sendMessage() {},
    Router() {
      const handlers = [];
      return { use(fn) { handlers.push(fn); }, handlers };
    },
    mount(prefix, router) { nodecg.mounted = { prefix, router }; },
    util: { authCheck: (_req, _res, next) => next() },
    on(event, fn) { events.on(event, fn); },
  };
  return { nodecg, replicants, listeners, events, logs };
}

async function until(check, timeoutMs = 4000, what = 'condition') {
  const end = Date.now() + timeoutMs;
  while (Date.now() < end) {
    if (check()) return;
    await new Promise((r) => setTimeout(r, 10));
  }
  throw new Error(`timed out waiting for ${what}`);
}

function tokenFile(t, content = `${TOKEN}\n`) {
  const dir = fs.mkdtempSync(path.join(os.tmpdir(), 'party-visuals-'));
  t.after(() => fs.rmSync(dir, { recursive: true, force: true }));
  const file = path.join(dir, 'lightshow.token');
  fs.writeFileSync(file, content);
  return file;
}

function start(t, show, over = {}) {
  const fake = fakeNodecg({
    lightshow: { url: show.url, tokenFile: tokenFile(t), pollMs: 100, reconnect: { minMs: 50, maxMs: 400 }, ...over },
  });
  const api = extension(fake.nodecg);
  t.after(() => fake.events.emit('serverStopping'));
  const value = (name) => fake.replicants.get(name).value;
  return { ...fake, api, value };
}

test('the extension mirrors the lightshow into replicants and keeps the token out of them', async (t) => {
  const show = await startMockLightshow({ token: TOKEN });
  t.after(() => show.close());
  const { value, replicants, logs, nodecg } = start(t, show);

  await until(() => value('connection').status === 'connected' && value('bpm') === 120, 4000, 'connected');
  assert.equal(value('clockSource'), 'tap');
  assert.deepEqual(value('palette'), ['#FF0096', '#00E1FF', '#4B00FF', '#80AEFF']);
  assert.equal(value('paletteOverride'), null);
  const beat = value('beatPos');
  assert.equal(beat.bpm, 120);
  assert.equal(beat.locked, false);
  assert.equal(typeof beat.beat, 'number');
  assert.equal(typeof beat.at, 'number');
  assert.deepEqual(value('controls'), { wash: { on: true, intensity: 50 }, bar: { on: true, intensity: 80 } });
  assert.equal(replicants.get('controls').opts.persistent, true, 'a restarted NodeCG keeps the switches');

  const everything = JSON.stringify([...replicants].map(([k, r]) => [k, r.value])) + logs.join('\n');
  assert.ok(!everything.includes(TOKEN), 'the token is in no replicant and no log line');
  assert.equal(nodecg.mounted.prefix, '/bundles/party-visuals');
});

test('a palette override from the lightshow lands in its replicant, and leaving it out clears it', async (t) => {
  const show = await startMockLightshow({ token: TOKEN });
  t.after(() => show.close());
  const { value } = start(t, show);
  await until(() => value('connection').status === 'connected', 4000, 'connected');
  show.publish({ paletteOverride: ['#ff8800'] });
  await until(() => Array.isArray(value('paletteOverride')), 2000, 'override');
  assert.deepEqual(value('paletteOverride'), ['#FF8800']);
  show.publish({ paletteOverride: null });
  await until(() => value('paletteOverride') === null, 2000, 'override cleared');
  assert.deepEqual(value('palette'), ['#FF0096', '#00E1FF', '#4B00FF', '#80AEFF'], 'the look palette is untouched');
});

test('while connected the beat position is refreshed; a disconnect freezes it and says so', async (t) => {
  const show = await startMockLightshow({ token: TOKEN });
  const { value } = start(t, show);
  await until(() => value('connection').status === 'connected', 4000, 'connected');
  const first = value('beatPos').at;
  await until(() => value('beatPos').at > first, 2000, 'a refresh');

  await show.close();
  await until(() => value('connection').status === 'reconnecting', 2000, 'reconnecting');
  const frozen = structuredClone(value('beatPos'));
  await new Promise((r) => setTimeout(r, 700));
  assert.deepEqual(value('beatPos'), frozen, 'no beat moves while the lightshow is gone');
  assert.equal(typeof value('connection').lastUpdate, 'number', 'the last update stays on show');
  assert.deepEqual(value('palette'), ['#FF0096', '#00E1FF', '#4B00FF', '#80AEFF'], 'the last colours are held');
});

test('a phase-locked lightshow sets the beat position', async (t) => {
  const show = await startMockLightshow({ token: TOKEN, state: { clock: { source: 'cdj', bpm: 128, beatPos: 32 } } });
  t.after(() => show.close());
  const { value } = start(t, show);
  await until(() => value('connection').status === 'connected' && value('beatPos').locked, 4000, 'locked');
  const { beat, at, bpm } = value('beatPos');
  assert.equal(bpm, 128);
  assert.equal(value('clockSource'), 'cdj');
  assert.ok(beat >= 32 && beat < 32 + (Date.now() - at + 2000) * 128 / 60000, `beat ${beat}`);
});

test('a patch that changes only the typed bpm does not re-feed the mirror\'s old beat position', async (t) => {
  const show = await startMockLightshow({ token: TOKEN, state: { clock: { source: 'cdj', bpm: 128, beatPos: 32, epoch: 0, at: Date.now() } } });
  t.after(() => show.close());
  const { value } = start(t, show);
  await until(() => value('connection').status === 'connected' && value('beatPos').locked, 4000, 'locked');
  // Let the clock run well past the snapshot's position: at 128 bpm, 700 ms is 1.5 beats.
  await new Promise((r) => setTimeout(r, 700));
  const published = Date.now();
  show.publish({ bpm: 128.5 });
  // A tempo patch re-publishes the beat position at once; the clock's own
  // tempo still wins over the typed one, so the bpm replicant stays at 128.
  await until(() => value('beatPos').at >= published, 2000, 'the beat position after the patch');
  await new Promise((r) => setTimeout(r, 100));
  const beat = value('beatPos');
  assert.equal(value('bpm'), 128);
  assert.equal(beat.epoch, 0, 'the stale position from the snapshot was not taken as a jump');
  assert.ok(beat.beat > 33, `the clock carried on from where it was, not back to 32: ${beat.beat}`);
  assert.equal(beat.locked, true);
});

test('a lightshow without a clock drives the tempo from its typed bpm', async (t) => {
  const show = await startMockLightshow({ token: TOKEN, state: { clock: undefined } });
  t.after(() => show.close());
  const { value } = start(t, show);
  await until(() => value('connection').status === 'connected' && value('bpm') === 120, 4000, 'connected');
  assert.equal(value('clockSource'), null);
  show.publish({ bpm: 100 });
  await until(() => value('beatPos').bpm === 100, 2000, 'the new tempo on the clock');
  assert.equal(value('bpm'), 100);
  assert.equal(value('beatPos').locked, false);
  assert.equal(value('beatPos').epoch, 0, 'a tempo change is not a jump');
});

test('commands from the dashboard and from other bundles change the controls', async (t) => {
  const show = await startMockLightshow({ token: TOKEN });
  t.after(() => show.close());
  const { value, listeners, api } = start(t, show);
  const answers = [];
  listeners.get('cmd')({ target: 'wash', cmd: 'air.off' }, (err, result) => answers.push([err, result]));
  assert.equal(value('controls').wash.on, false);
  assert.equal(answers[0][0], null);
  assert.equal(answers[0][1].ok, true);
  listeners.get('cmd')({ target: 'wash', cmd: 'bogus' }, (err, result) => answers.push([err, result]));
  assert.equal(answers[1][1].ok, false);
  assert.equal(api.command('bar', 'intensity.10').ok, true);
  assert.equal(value('controls').bar.intensity, 10);
});

test('a token file that cannot be read is an error, and nothing is sent', async (t) => {
  const show = await startMockLightshow({ token: TOKEN });
  t.after(() => show.close());
  const { value } = start(t, show, { tokenFile: path.join(os.tmpdir(), 'party-visuals-no-such-dir', 'missing.token') });
  await until(() => value('connection').status === 'error', 2000, 'error');
  assert.match(value('connection').error, /token file/i);
  await new Promise((r) => setTimeout(r, 200));
  assert.equal(show.seen.handshakes.length, 0);
  assert.equal(show.seen.stateRequests, 0);
});

test('no token file at all is a lightshow without a token', async (t) => {
  const show = await startMockLightshow({ token: '' });
  t.after(() => show.close());
  const { value } = start(t, show, { tokenFile: '' });
  await until(() => value('connection').status === 'connected', 4000, 'connected');
  assert.equal(show.seen.handshakes[0].token, '');
});

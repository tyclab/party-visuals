'use strict';
// The live input for the live-audio page: the extension subscribes only while a page pulls, answers
// each pull with the newer samples as soon as they come, keeps the token to itself, and says what the
// page waits for.
const test = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const http = require('node:http');
const os = require('node:os');
const path = require('node:path');
const { EventEmitter } = require('node:events');
const { startMockLightshow } = require('./helpers/mock-lightshow.js');
const extension = require('../extension/index.js');
const { SampleFeed, KEEP_MS, HOLD_MS } = require('../extension/samples.js');
const { createHttpHandler } = require('../extension/http.js');

const TOKEN = 'mock-samples-token-17';

async function until(check, what, timeoutMs = 4000) {
  for (const end = Date.now() + timeoutMs; Date.now() < end;) {
    if (check()) return;
    await new Promise((resolve) => setTimeout(resolve, 10));
  }
  throw new Error(`timed out waiting for ${what}`);
}

/** The extension under NodeCG's API as far as it uses it, its HTTP routes served on loopback. */
async function startExtension(t, show) {
  const dir = fs.mkdtempSync(path.join(os.tmpdir(), 'party-visuals-samples-'));
  fs.writeFileSync(path.join(dir, 'token'), `${TOKEN}\n`);
  const events = new EventEmitter(), logs = [];
  let mounted = null;
  const nodecg = {
    bundleConfig: { lightshow: { url: show.url, tokenFile: path.join(dir, 'token'), reconnect: { minMs: 50, maxMs: 400 } } },
    log: Object.fromEntries(['info', 'warn', 'error', 'debug', 'trace'].map((level) => [level, (...args) => logs.push(args.join(' '))])),
    Replicant: (name, opts = {}) => ({ value: structuredClone(opts.defaultValue) }),
    listenFor() {}, sendMessage() {},
    Router: () => { const handlers = []; return { use: (fn) => handlers.push(fn), handlers }; },
    mount: (prefix, router) => { mounted = router; },
    on: (event, fn) => events.on(event, fn),
  };
  extension(nodecg);
  const server = http.createServer((req, res) => mounted.handlers[0](req, res, () => { res.statusCode = 404; res.end(); }));
  await new Promise((resolve) => server.listen(0, '127.0.0.1', resolve));
  t.after(async () => {
    events.emit('serverStopping');
    server.closeAllConnections();
    await new Promise((resolve) => server.close(resolve));
    fs.rmSync(dir, { recursive: true, force: true });
  });
  const base = `http://127.0.0.1:${server.address().port}`;
  const pull = async (query = '', headers = {}) => {
    const res = await fetch(`${base}/api/samples${query}`, { headers });
    return { status: res.status, text: await res.text() };
  };
  return { pull, logs };
}

const packet = (g, end, values) => {
  const data = Buffer.alloc(values.length * 2);
  values.forEach((v, i) => data.writeInt16LE(v, 2 * i));
  return { g, end, rate: 22050, at: 1_800_000_000_000, data };
};
const decode = (body) => {
  const raw = Buffer.from(body.data, 'base64');
  return Array.from({ length: raw.length / 2 }, (_, i) => raw.readInt16LE(2 * i));
};

test('the extension subscribes to the samples while a page pulls and answers each pull as they come', { timeout: 15000 }, async (t) => {
  const show = await startMockLightshow({ token: TOKEN, state: { live: { running: true, listening: true, quiet: false } } });
  t.after(() => show.close());
  const { pull, logs } = await startExtension(t, show);
  await until(() => show.seen.subscriptions.length === 1, 'the audio subscription');
  await new Promise((resolve) => setTimeout(resolve, 100));
  assert.deepEqual(show.seen.subscriptions, [['audio']], 'no page, no samples asked for');

  const first = pull();
  await until(() => show.sampleSubscribers() === 1, 'the samples subscription');
  const started = Date.now();
  show.block(packet(4, 3000, [1000, -2000, 3000]));
  const answer = await first;
  assert.equal(answer.status, 200);
  const body = JSON.parse(answer.text);
  assert.deepEqual([body.g, body.from, body.end, body.rate, body.wait, body.at], [4, 2997, 3000, 22050, null, 1_800_000_000_000]);
  assert.deepEqual(decode(body), [1000, -2000, 3000]);
  assert.ok(body.ageMs >= 0 && body.ageMs < 100);
  assert.ok(body.heldMs > 0, 'it waited here for the samples, and says how long');
  await until(() => show.relay.seen.acks === 1, 'the acknowledgement');

  const second = pull(`?g=4&after=${body.end}`);
  await new Promise((resolve) => setTimeout(resolve, 50));
  // On time: as far on in the stream as on the clock.
  const end = 3000 + Math.round(((Date.now() - started) / 1000) * 22050);
  show.block(packet(4, end, [7, 8]));
  const next = JSON.parse((await second).text);
  assert.deepEqual([next.from, next.end, decode(next)], [end - 2, end, [7, 8]], 'only what the page did not have');

  const refused = await pull('', { 'sec-fetch-site': 'cross-site' });
  assert.equal(refused.status, 403, 'another site\'s page may not start the samples');
  assert.ok(![answer.text, refused.text, ...logs].some((text) => text.includes(TOKEN)), 'the token reaches no page and no log');
});

/** A feed on a clock the test moves, and a client that records what it was asked. */
function feedOn({ status = { status: 'connected', via: 'socket' }, live = { listening: true } } = {}) {
  let now = 1_000_000;
  const asked = [];
  const client = { status, live: () => live, wantSamples: (on) => asked.push(on) };
  const feed = new SampleFeed({ client, now: () => now });
  const answers = [];
  const pull = (request = {}) => { const respond = (body) => answers.push(body); feed.pull(request, respond); return respond; };
  return { feed, client, asked, answers, pull, advance: (ms) => { now += ms; feed.tick(); } };
}

test('a pull waits for samples at most half a second, a page that left is forgotten, and nobody pulling unsubscribes', () => {
  const h = feedOn();
  h.pull();
  assert.deepEqual(h.asked, [true]);
  h.advance(HOLD_MS - 1);
  assert.equal(h.answers.length, 0, 'held');
  h.advance(1);
  assert.equal(h.answers.length, 1);
  assert.equal(h.answers[0].wait, 'audio', 'nothing came: the page says so');
  const gone = h.pull();
  h.feed.cancel(gone);
  h.feed.receive(packet(1, 300, Array(300).fill(4000)));
  assert.equal(h.answers.length, 1, 'a page that went away gets nothing');
  h.pull();
  assert.equal(h.answers.length, 2, 'samples it has not seen: answered at once');
  h.advance(KEEP_MS - 1);
  assert.deepEqual(h.asked, [true]);
  h.advance(1);
  assert.deepEqual(h.asked, [true, false], 'five seconds without a page: the lightshow stops sending');
  h.pull();
  assert.deepEqual(h.asked, [true, false, true]);
  h.advance(HOLD_MS);
  for (let i = 0; i < 8; i++) h.pull({ g: 1, after: 300 });
  const before = h.answers.length;
  h.pull({ g: 1, after: 300 });
  assert.deepEqual(h.answers.slice(before), [null], 'a ninth page waiting is refused, not answered empty at once');
});

test('the page waits for the lightshow, its live input, its audio, and sound', () => {
  assert.equal(feedOn({ status: { status: 'reconnecting', via: null } }).feed.wait(0), 'lightshow');
  assert.equal(feedOn({ status: { status: 'connected', via: 'http' } }).feed.wait(0), 'lightshow', 'polling carries no samples');
  assert.equal(feedOn({ live: null }).feed.wait(0), 'live-input');
  assert.equal(feedOn({ live: { listening: false } }).feed.wait(0), 'live-input');
  const h = feedOn();
  h.feed.receive(packet(1, 256, Array(256).fill(0)));
  assert.equal(h.feed.wait(h.feed.now()), 'silence', 'digital silence from the start');
  h.feed.receive(packet(1, 512, Array(256).fill(9000)));
  assert.equal(h.feed.wait(h.feed.now()), null, 'sound');
  // Half a second apart in the stream as on the clock: on time, so taken.
  for (let k = 1; k <= 7; k++) {
    h.advance(500);
    h.feed.receive(packet(1, 512 + k * 11025, Array(256).fill(0)));
    assert.equal(h.feed.wait(h.feed.now()), k <= 6 ? null : 'silence', `${k * 500} ms of quiet`);
  }
  h.advance(1001);
  assert.equal(h.feed.wait(h.feed.now()), 'audio', 'no samples for a second');
});

test('a lightshow that restarts and reuses the stream\'s name starts a new stream here, and a page asking past it is answered from its start', () => {
  const h = feedOn();
  h.feed.receive(packet(1, 1_000_000, Array(256).fill(4000)));
  h.advance(5000);
  h.pull({ g: 1, after: 1_000_000 });
  h.feed.receive(packet(1, 256, Array(256).fill(4000)));
  assert.deepEqual([h.feed.ring.start, h.feed.ring.end], [0, 256], 'the count from zero is a new stream');
  assert.ok(h.feed.ring.ageMs(h.feed.now()) < 100, 'and its arrivals are measured afresh');
  const [held] = h.answers.splice(0);
  assert.deepEqual([held.from, held.end], [0, 256], 'the page that was waiting gets the new stream');
  h.pull({ g: 1, after: 1_000_000 });
  assert.deepEqual(decode(h.answers[0]).length, 256, 'a position this stream has not reached: from its start');
});

test('arrivals are timed on a monotonic clock: a step of the wall clock makes no packet late', (t) => {
  const real = Date.now;
  t.after(() => { Date.now = real; });
  const client = { status: { status: 'connected', via: 'socket' }, live: () => ({ listening: true }), wantSamples() {} };
  const feed = new SampleFeed({ client });
  const started = performance.now();
  feed.receive(packet(1, 22050, Array(256).fill(4000)));
  Date.now = () => real() + 3_600_000;
  const end = 22050 + Math.round(((performance.now() - started) / 1000) * 22050) + 256;
  feed.receive(packet(1, end, Array(256).fill(4000)));
  let body;
  feed.pull({}, (answer) => { body = answer; });
  assert.ok(body.ageMs < 100, `${body.ageMs} ms behind after the wall clock jumped an hour`);
});

test('packets that are not whole 16-bit samples are not taken, and a page asking past them gets no stale data', () => {
  const h = feedOn();
  for (const bad of [null, { g: 1, end: 9, rate: 22050, data: Buffer.alloc(3) }, { g: 1, end: 9, rate: 0, data: Buffer.alloc(2) },
    { g: 1.5, end: 9, rate: 22050, data: Buffer.alloc(2) }, { g: 1, end: 9e9, rate: 22050, data: Buffer.alloc(20000) }]) h.feed.receive(bad);
  assert.equal(h.feed.ring.g, null);
  h.feed.receive({ ...packet(2, 4, [1, 2, 3, 4]), data: new Uint8Array(packet(2, 4, [1, 2, 3, 4]).data) });
  assert.deepEqual([...h.feed.ring.since(-Infinity).samples], [1, 2, 3, 4], 'a typed array as well as a Buffer');
  h.pull({ g: 2, after: 4 });
  h.pull({ g: 1, after: 4 });
  assert.equal(h.answers.length, 1, 'another process: everything the page lacks, at once');
  assert.deepEqual(decode(h.answers[0]), [1, 2, 3, 4]);
  const handler = createHttpHandler({ run() {}, state() {}, samples: h.feed });
  const res = { statusCode: 0, headers: {}, setHeader(k, v) { this.headers[k] = v; }, end(text) { this.body = text; }, on() {} };
  handler({ url: '/api/samples?g=2&after=x', method: 'GET', headers: { host: '127.0.0.1:9090' } }, res, () => assert.fail('handled here'));
  assert.equal(JSON.parse(res.body).from, 0, 'a position that is not a number reads from the start');
});

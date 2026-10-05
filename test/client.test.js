'use strict';
const test = require('node:test');
const assert = require('node:assert/strict');
const { LightshowClient } = require('../extension/lightshow-client.js');
const { startMockLightshow } = require('./helpers/mock-lightshow.js');

const TOKEN = 'mock-token-0123456789';

/** Resolve with the first event `name` whose payload passes `pass`. */
function waitFor(emitter, name, pass = () => true, timeoutMs = 4000) {
  return new Promise((resolve, reject) => {
    const timer = setTimeout(() => {
      emitter.off(name, on);
      reject(new Error(`timed out waiting for ${name}`));
    }, timeoutMs);
    function on(value) {
      if (!pass(value)) return;
      clearTimeout(timer);
      emitter.off(name, on);
      resolve(value);
    }
    emitter.on(name, on);
  });
}

function makeClient(url, over = {}) {
  const client = new LightshowClient({
    url, token: TOKEN, pollMs: 100, requestTimeoutMs: 1000,
    backoff: { minMs: 50, maxMs: 400, jitter: 0 },
    ...over,
  });
  const statuses = [];
  client.on('status', (s) => statuses.push(s));
  return { client, statuses };
}

test('connects with protocol 2 and the token, and reports tempo, source and palette', async (t) => {
  const show = await startMockLightshow({ token: TOKEN });
  const { client, statuses } = makeClient(show.url);
  t.after(async () => { client.stop(); await show.close(); });

  const looked = waitFor(client, 'look');
  client.start();
  const look = await looked;
  assert.equal(look.bpm, 120);
  assert.equal(look.clockSource, 'tap');
  assert.deepEqual(look.palette, ['#FF0096', '#00E1FF', '#4B00FF', '#80AEFF']);
  assert.equal(look.paletteOverride, null);
  assert.ok(look.changed.includes('clock'), 'a snapshot is fresh in every key');

  assert.equal(show.seen.handshakes[0].protocol, 2);
  assert.ok(show.seen.handshakes[0].token === TOKEN, 'the token went in the handshake');
  const status = client.status;
  assert.equal(status.status, 'connected');
  assert.equal(status.via, 'socket');
  assert.equal(typeof status.lastUpdate, 'number');
  assert.equal(status.target, show.url);
  assert.ok(!JSON.stringify(statuses).includes(TOKEN), 'the token is in no status');
});

test('patches update the look, and a lost patch brings a fresh snapshot', async (t) => {
  const show = await startMockLightshow({ token: TOKEN });
  const { client } = makeClient(show.url);
  t.after(async () => { client.stop(); await show.close(); });
  const first = waitFor(client, 'look');
  client.start();
  await first;

  const faster = waitFor(client, 'look', (l) => l.bpm === 128);
  show.publish({ bpm: 128, clock: { source: 'tap', bpm: 128 } });
  const look = await faster;
  assert.deepEqual(look.changed.sort(), ['bpm', 'clock']);

  // An unrelated domain changes nothing the graphics read: no look event.
  let extra = 0;
  const count = () => extra++;
  client.on('look', count);
  show.publish({ nowPlaying: { title: 'x' } });
  await new Promise((r) => setTimeout(r, 100));
  client.off('look', count);
  assert.equal(extra, 0);

  // One look patch lost on the way: the next one shows the gap.
  show.publish({ bpm: 90, clock: { source: 'tap', bpm: 90 } }, { drop: true });
  const resynced = waitFor(client, 'look', (l) => l.bpm === 90);
  show.publish({ colorA: 0 });
  const after = await resynced;
  assert.equal(show.seen.syncs, 1);
  assert.equal(after.palette[0], '#FF0000');
});

test('reconnects with a growing backoff after the lightshow goes away, then starts over', async (t) => {
  let show = await startMockLightshow({ token: TOKEN });
  const port = show.port;
  const { client, statuses } = makeClient(show.url);
  t.after(async () => { client.stop(); await show.close(); });
  const up = waitFor(client, 'status', (s) => s.status === 'connected');
  client.start();
  await up;

  const down = waitFor(client, 'status', (s) => s.status === 'reconnecting');
  await show.close();
  await down;
  // Let a few attempts fail: 50, 100, 200, 400 ms.
  await waitFor(client, 'status', (s) => s.retryInMs === 400, 3000);
  const waits = [...new Set(statuses.map((s) => s.retryInMs).filter((w) => w))];
  assert.deepEqual(waits.slice(0, 4), [50, 100, 200, 400]);
  assert.ok(statuses.every((s) => s.status !== 'error'), 'a server that is down is not an error, it is a retry');

  show = await startMockLightshow({ token: TOKEN, port });
  const back = await waitFor(client, 'status', (s) => s.status === 'connected' && s.via === 'socket', 3000);
  assert.equal(back.retryInMs, null);

  // The next outage starts from the shortest wait again.
  const before = statuses.length;
  const again = waitFor(client, 'status', (s) => s.status === 'reconnecting' && s.retryInMs);
  await show.close();
  const next = await again;
  assert.equal(next.retryInMs, 50);
  assert.ok(statuses.length > before);
});

test('a refused token is an error, is retried, and is never echoed', async (t) => {
  const show = await startMockLightshow({ token: 'the-real-token' });
  const { client, statuses } = makeClient(show.url, { token: TOKEN });
  t.after(async () => { client.stop(); await show.close(); });
  client.start();
  const refused = await waitFor(client, 'status', (s) => s.status === 'error');
  assert.match(refused.error, /token/i);
  await waitFor(client, 'status', (s) => s.status === 'error' && s.retryInMs >= 100, 3000);
  assert.ok(show.seen.handshakes.length >= 2, 'tried again');
  assert.ok(!JSON.stringify(statuses).includes(TOKEN));
  assert.ok(!JSON.stringify(statuses).includes('the-real-token'));
});

test('falls back to GET /api/state when the socket cannot connect', async (t) => {
  const show = await startMockLightshow({ token: TOKEN, socket: false });
  const { client, statuses } = makeClient(show.url);
  t.after(async () => { client.stop(); await show.close(); });
  client.start();
  const status = await waitFor(client, 'status', (s) => s.status === 'connected' && s.via === 'http');
  assert.equal(typeof status.lastUpdate, 'number');
  assert.ok(show.seen.stateTokens.every((tok) => tok === TOKEN), 'the poll sends the token as a header');

  const slower = waitFor(client, 'look', (l) => l.bpm === 100);
  show.publish({ bpm: 100, clock: { source: 'tap', bpm: 100 } });
  const look = await slower;
  assert.deepEqual(look.palette, ['#FF0096', '#00E1FF', '#4B00FF', '#80AEFF']);
  assert.ok(!JSON.stringify(statuses).includes(TOKEN));
});

test('an address carrying credentials, or none at all, is refused before anything is sent', async (t) => {
  for (const url of ['', 'not a url', 'ftp://127.0.0.1:3000', 'http://127.0.0.1:9/?token=abc', 'http://user:pw@127.0.0.1:9/']) {
    const client = new LightshowClient({ url, token: TOKEN });
    t.after(() => client.stop());
    client.start();
    assert.equal(client.status.status, 'error', url);
    assert.ok(client.status.error, url);
    assert.ok(!JSON.stringify(client.status).includes('abc') && !JSON.stringify(client.status).includes('pw'), url);
  }
});

test('stop() leaves nothing running', async () => {
  const show = await startMockLightshow({ token: TOKEN });
  const { client } = makeClient(show.url);
  const up = waitFor(client, 'status', (s) => s.status === 'connected');
  client.start();
  await up;
  client.stop();
  assert.equal(client.status.status, 'stopped');
  await new Promise((r) => setTimeout(r, 100));
  assert.equal(show.clients(), 0);
  await show.close();
});

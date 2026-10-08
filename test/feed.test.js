'use strict';
const test = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const vm = require('node:vm');
const { EventEmitter } = require('node:events');
const { BeatClock } = require('../shared/beat-clock.js');
const { BeatVisual } = require('../shared/visuals.js');
const { effectivePalette } = require('../shared/palette.js');

function browserFeed({ connected = true } = {}) {
  let nowMs = 0;
  const wallEpoch = 1_790_000_000_000;
  const socket = new EventEmitter();
  socket.connected = connected;
  const reps = new Map();
  const browser = {
    PartyVisuals: { BeatClock, effectivePalette },
    performance: { now: () => nowMs },
    Date: { now: () => wallEpoch + nowMs },
    NodeCG: { waitForReplicants: () => Promise.resolve() },
    nodecg: { socket, bundleConfig: {}, Replicant(name) {
      const rep = new EventEmitter();
      reps.set(name, rep);
      return rep;
    } },
  };
  browser.window = browser;
  vm.runInNewContext(fs.readFileSync(require.resolve('../graphics/feed.js'), 'utf8'), browser);
  const change = (name, value) => {
    const rep = reps.get(name);
    rep.value = value;
    rep.emit('change', value);
  };
  change('connection', { status: 'connected' });
  change('palette', ['#FF8000', '#3300E6']);
  change('paletteOverride', null);
  return {
    feed: browser.PartyFeed, socket, change,
    at(time) { nowMs = time; return browser.PartyFeed.read(time); },
    beat({ age = 0, ...over } = {}) {
      change('beatPos', { beat: nowMs / 500, bpm: 120, epoch: 0, at: wallEpoch + nowMs - age, ...over });
    },
  };
}

test('shared audio is used only in Reactive mode and expires independently of beat heartbeats', () => {
  const browser = browserFeed();
  browser.beat();
  browser.change('audioMode', 'reactive');
  const audio = { at: 1_790_000_000_000, t: 1, eventT: 1, beat: 'soft', energy: 0.4 };
  browser.change('audio', audio);
  assert.deepEqual(browser.at(599).audio, audio);
  assert.equal(browser.at(600).audio, null);
  browser.at(100);
  browser.change('audioMode', 'tempo');
  assert.equal(browser.at(100).audio, null);
  browser.change('audioMode', 'reactive');
  browser.socket.connected = false;
  assert.equal(browser.at(100).audio, null);
});

test('a lost NodeCG socket calms graphics immediately despite a cached connected Replicant', () => {
  const browser = browserFeed();
  const visual = new BeatVisual();
  let frame;
  for (let now = 0; now <= 1540; now += 20) {
    browser.at(now);
    if (now % 500 === 0) browser.beat();
    const feed = browser.at(now);
    frame = visual.frame({ ...feed, nowMs: now });
  }
  assert.ok(frame.pulse > 0.1);
  const lastMotion = frame.motion;
  let lastPulse = frame.pulse;
  browser.socket.connected = false;
  browser.socket.emit('disconnect', 'transport close');
  for (let now = 1560; now <= 3000; now += 20) {
    const feed = browser.at(now);
    assert.equal(feed.live, false);
    assert.deepEqual(feed.palette, ['#FF8000', '#3300E6']);
    frame = visual.frame({ ...feed, nowMs: now });
    assert.equal(frame.onset, false);
    assert.equal(frame.motion, lastMotion);
    assert.ok(frame.pulse <= lastPulse);
    lastPulse = frame.pulse;
  }
  assert.equal(frame.live, 0);
  assert.ok(frame.pulse < 0.01);
});

test('a missing beat heartbeat becomes calm after two seconds even while the socket stays connected', () => {
  const browser = browserFeed();
  assert.equal(browser.at(0).live, false);
  browser.beat();
  assert.equal(browser.at(1999).live, true);
  assert.equal(browser.at(2000).live, false);
  assert.equal(browser.at(120000).live, false);
  browser.beat();
  assert.equal(browser.at(120000).live, true);
});

test('reconnection waits for a new beat heartbeat and the renderer eases back in', () => {
  const browser = browserFeed();
  const visual = new BeatVisual();
  browser.beat();
  visual.frame({ ...browser.at(0), nowMs: 0 });
  visual.frame({ ...browser.at(800), nowMs: 800 });
  browser.socket.connected = false;
  browser.socket.emit('disconnect');
  visual.frame({ ...browser.at(1600), nowMs: 1600 });
  browser.socket.connected = true;
  browser.socket.emit('connect');
  assert.equal(browser.at(1600).live, false, 'cached heartbeat is not revived by reconnect');
  browser.beat();
  const resumed = visual.frame({ ...browser.at(1620), nowMs: 1620 });
  assert.equal(browser.at(1620).live, true);
  assert.equal(resumed.onset, false, 'resuming does not add a pulse');
  assert.ok(resumed.live > 0 && resumed.live < 0.1);
});

test('an old cached beat stays calm on page refresh and malformed updates cannot renew it', () => {
  const browser = browserFeed();
  browser.beat({ age: 3000 });
  assert.equal(browser.at(0).live, false);
  browser.beat();
  assert.equal(browser.at(0).live, true);
  browser.at(1900);
  for (const invalid of [{ beat: NaN }, { at: Infinity }, { bpm: undefined }, { bpm: 0 }]) browser.beat(invalid);
  assert.equal(browser.at(2000).live, false);
  browser.beat();
  browser.change('connection', { status: 'reconnecting' });
  assert.equal(browser.at(2000).live, false, 'a fresh local heartbeat cannot override lightshow disconnection');
});

test('a page whose NodeCG socket is already disconnected stays calm', () => {
  const browser = browserFeed({ connected: false });
  browser.beat();
  assert.equal(browser.at(0).live, false);
});

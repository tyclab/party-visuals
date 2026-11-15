'use strict';
// The live-audio page's sound-to-picture bound, end to end: the sound card, the lightshow's relay as the
// engine has it (helpers/mock-relay.js), the network, this extension's feed and the page's own feed and
// draw rule (LivePageFeed, which graphics/milkdrop.js runs), simulated event by event. Whatever is faster
// or slower than what drains it, no window is drawn whose newest sample is more than MAX_AGE_MS past its
// quickest delivery, and nothing queues: a hold-up drops samples.
const test = require('node:test');
const assert = require('node:assert/strict');
const { SampleFeed } = require('../extension/samples.js');
const { MockRelay } = require('./helpers/mock-relay.js');
const { SampleRing, ArrivalClock, LivePageFeed, MAX_AGE_MS, liveAudioWait, timeBytes, NO_AUDIO_MS, QUIET_AFTER_MS } = require('../shared/samples.js');

const RATE = 22050, HOP = 256, WALL = 1_800_000_000_000;

class Events {
  constructor() { this.heap = []; this.seq = 0; }
  at(t, fn) {
    const heap = this.heap, item = { t, n: this.seq++, fn };
    heap.push(item);
    for (let i = heap.length - 1; i > 0;) {
      const p = (i - 1) >> 1;
      if (before(heap[p], heap[i])) break;
      [heap[p], heap[i]] = [heap[i], heap[p]]; i = p;
    }
  }
  next() {
    const heap = this.heap, top = heap[0], last = heap.pop();
    if (heap.length) {
      heap[0] = last;
      for (let i = 0; ;) {
        const l = 2 * i + 1, r = l + 1;
        let m = i;
        if (l < heap.length && before(heap[l], heap[m])) m = l;
        if (r < heap.length && before(heap[r], heap[m])) m = r;
        if (m === i) break;
        [heap[m], heap[i]] = [heap[i], heap[m]]; i = m;
      }
    }
    return top;
  }
}
const before = (a, b) => a.t < b.t || (a.t === b.t && a.n < b.n);

function random(seed) {
  return () => {
    seed = (seed + 0x6D2B79F5) | 0;
    let t = Math.imul(seed ^ (seed >>> 15), 1 | seed);
    t = (t + Math.imul(t ^ (t >>> 7), 61 | t)) ^ t;
    return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
  };
}

// Ten seconds each, in turn: [name, page frame ms, page busy ms per frame, network jitter ms].
const PHASES = [
  ['steady', 1000 / 60, 4, 3],
  ['page faster than the samples', 1000 / 144, 2, 3],
  ['slow page', 1000 / 15, 60, 3],
  ['page stalls 2 s', 1000 / 60, 4, 3],
  ['network stalls 1.5 s', 1000 / 60, 4, 3],
  ['jittery network', 1000 / 60, 4, 20],
];
const PHASE_MS = 10_000, BASE_MS = 2;

/** `seconds` of sound through the chain. `drift`: the sound card's clock against the show PC's; `startHours`: how long the stream has run already. */
function simulate({ seconds, drift = 0, startHours = 0, seed = 7 }) {
  const events = new Events(), rnd = random(seed);
  const rate = RATE * (1 + drift), first = Math.round(startHours * 3600 * rate);
  const captured = (index) => ((index - first) / rate) * 1000;
  let now = 0;
  const phase = () => PHASES[Math.floor(now / PHASE_MS) % PHASES.length];
  const phaseStart = () => Math.floor(now / PHASE_MS) * PHASE_MS;
  const network = () => {
    const [name, , , jitter] = phase(), stalledUntil = phaseStart() + 1500;
    return (name === 'network stalls 1.5 s' && now < stalledUntil ? stalledUntil - now : 0) + BASE_MS + rnd() * jitter;
  };

  const client = { status: { status: 'connected', via: 'socket' }, live: () => ({ listening: true }), wantSamples() {} };
  const feed = new SampleFeed({ client, now: () => now });
  const sound = Buffer.alloc(HOP * 2);
  for (let i = 0; i < HOP; i++) sound.writeInt16LE(i % 2 ? 3000 : -3000, 2 * i);

  // The extension's socket to the relay: a packet arrives after the network's delay and its acknowledgement
  // goes back the same way; the relay gives up waiting for it after a second, as Socket.IO's timeout does.
  // `quickest` keeps, a second at a time, the shortest true delivery of the newest sample of a packet.
  const quickest = [];
  let inFlight = 0, mostInFlight = 0;
  const delivered = (delay) => {
    const second = Math.floor(now / 1000), last = quickest[quickest.length - 1];
    if (last && last.second === second) last.delay = Math.min(last.delay, delay);
    else quickest.push({ second, delay });
    while (quickest[0].second <= second - 5) quickest.shift();
  };
  const relay = new MockRelay();
  relay.add({ id: 'extension', connected: true, timeout: (ms) => ({ emit: (_event, packet, ack) => {
    let done = false;
    const finish = (err) => { if (!done) { done = true; ack(err); } };
    events.at(now + ms, () => finish(new Error('timeout')));
    mostInFlight = Math.max(mostInFlight, ++inFlight);
    events.at(now + network(), () => {
      inFlight--;
      delivered(now - captured(packet.end - 1));
      feed.receive(packet);
      events.at(now + network(), () => finish(null));
    });
  } }) });
  const block = (k) => events.at((k * HOP / rate) * 1000 + 0.2, () => {
    relay.push({ g: 1, end: first + k * HOP, rate: RATE, at: WALL + now, data: sound });
    block(k + 1);
  });
  block(1);
  const tick = () => { feed.tick(); events.at(now + 100, tick); };
  events.at(100, tick);

  // The page: one pull out at a time, answered when samples come; a response waits while the page is busy.
  const page = new LivePageFeed(), out = new Uint8Array(1024);
  let busyUntil = 0;
  const pull = () => {
    const asked = now, query = new URLSearchParams(page.query());
    events.at(now + 0.3, () => feed.pull({ g: query.has('g') ? Number(query.get('g')) : null, after: query.has('after') ? Number(query.get('after')) : null },
      (body) => events.at(now + 0.3, () => deliver(body, asked))));
  };
  const deliver = (body, asked) => {
    if (now < busyUntil) { events.at(busyUntil, () => deliver(body, asked)); return; }
    page.take(body, now, asked);
    pull();
  };
  pull();

  const frames = new Map(PHASES.map(([name]) => [name, { drawn: 0, silent: 0, worst: 0, over: -Infinity, resumedAfter: null }]));
  const frame = () => {
    const [name, interval, busy] = phase(), stats = frames.get(name);
    if (name === 'page stalls 2 s' && now - phaseStart() < 2000) {
      // The page's thread is blocked: no frame, and the answer to its pull waits with everything else.
      busyUntil = phaseStart() + 2000;
      events.at(busyUntil, frame);
      return;
    }
    if (page.window(now, out, WALL + now)) {
      const age = now - captured(page.ring.end - 1), path = Math.min(...quickest.map((q) => q.delay));
      stats.drawn++;
      stats.worst = Math.max(stats.worst, age);
      stats.over = Math.max(stats.over, age - path - MAX_AGE_MS);
      if (stats.resumedAfter === null && now - phaseStart() > 100) stats.resumedAfter = now - phaseStart();
    } else stats.silent++;
    busyUntil = now + busy;
    events.at(Math.max(now + interval, busyUntil), frame);
  };
  events.at(0, frame);

  const peak = { pulls: 0 };
  for (let item; (item = events.next()) && item.t <= seconds * 1000;) {
    now = item.t;
    item.fn();
    peak.pulls = Math.max(peak.pulls, feed.waiting.length);
  }
  peak.packets = mostInFlight;
  return { frames, peak, feed, page, now };
}

for (const [label, options] of [
  ['the first minute, sound card fast', { seconds: 60, drift: 0.001 }],
  ['four hours, sound card fast', { seconds: 4 * 3600, drift: 0.001, seed: 11 }],
  ['the first minute, sound card slow', { seconds: 60, drift: -0.001 }],
  ['a minute after four hours, sound card slow', { seconds: 60, drift: -0.001, startHours: 4 }],
]) {
  test(`sound to picture stays within ${MAX_AGE_MS} ms of the quickest path: ${label}`, () => {
    const { frames, peak, page, now } = simulate(options);
    for (const [name, stats] of frames) {
      console.log(`[bound] ${label}, ${name}: worst ${stats.worst.toFixed(1)} ms from sound to frame, `
        + `${(stats.over + MAX_AGE_MS).toFixed(1)} ms past the quickest path; ${stats.silent} of ${stats.drawn + stats.silent} frames silent`);
      // A sound card 0.1 % off moves the quickest delivery by up to 5 ms across the five seconds it is remembered.
      assert.ok(stats.over <= 5, `${name}: a sample drawn ${(stats.over + MAX_AGE_MS).toFixed(1)} ms past its quickest path`);
      assert.ok(stats.drawn > 0, `${name}: something was drawn`);
    }
    for (const [name, share] of [['steady', 0.01], ['page faster than the samples', 0.01], ['jittery network', 0.02]]) {
      const { drawn, silent } = frames.get(name);
      assert.ok(silent <= share * (drawn + silent), `${name}: ${silent} of ${drawn + silent} frames had no fresh samples`);
    }
    for (const name of ['page stalls 2 s', 'network stalls 1.5 s']) {
      const { resumedAfter } = frames.get(name);
      assert.ok(resumedAfter !== null && resumedAfter <= (name === 'page stalls 2 s' ? 2000 : 1500) + 150,
        `${name}: fresh samples ${resumedAfter} ms into the phase, not a backlog first`);
    }
    // One packet on the way, and a second only after the relay's one-second wait for a stalled acknowledgement.
    assert.ok(peak.packets <= 2 && peak.pulls <= 1, `nothing queues: at most ${peak.packets} packets on the way and ${peak.pulls} pulls waiting`);
    const ages = page.ageStats(now);
    assert.ok(ages && ages.medianMs >= 0 && ages.worstMs < 200, `the page's own age measure, against the engine's stamp: ${JSON.stringify(ages)}`);
  });
}

test('a held packet that lands late is not drawn: the picture is silent until a fresh one', () => {
  const clock = new ArrivalClock(), page = new LivePageFeed(), out = new Uint8Array(1024);
  const body = (end, ageMs) => ({ g: 3, end, rate: RATE, ageMs, heldMs: 0, data: Buffer.from(new Int16Array(HOP).fill(5000).buffer).toString('base64') });
  let now = 50_000;
  for (let k = 1; k <= 200; k++) {
    now = 50_000 + (k * HOP / RATE) * 1000 + 2;
    page.take(body(k * HOP, clock.lateness((k * HOP / RATE) * 1000, now)), now, now);
    assert.ok(page.window(now, out));
  }
  const held = 201 * HOP;
  now += 1500;
  page.take(body(held, clock.lateness((held / RATE) * 1000, now)), now, now);
  assert.equal(page.window(now, out), null, 'a second and a half late: silence, not the stale window');
  const fresh = Math.round(((now - 50_000 - 2) / 1000) * RATE);
  page.take(body(fresh, clock.lateness((fresh / RATE) * 1000, now)), now, now);
  assert.ok(page.window(now, out), 'the newest, on time, is drawn');
  assert.equal(page.ring.start, fresh - HOP, 'the samples that never came are a gap, not a queue');
});

test('a lightshow restarted after an hour that reuses the stream\'s name is followed within a pull', () => {
  let now = 0;
  const client = { status: { status: 'connected', via: 'socket' }, live: () => ({ listening: true }), wantSamples() {} };
  const feed = new SampleFeed({ client, now: () => now }), page = new LivePageFeed(), out = new Uint8Array(1024);
  const tone = Buffer.alloc(HOP * 2);
  for (let i = 0; i < HOP; i++) tone.writeInt16LE(i % 2 ? 4000 : -4000, 2 * i);
  const pull = () => {
    const query = new URLSearchParams(page.query());
    feed.pull({ g: query.has('g') ? Number(query.get('g')) : null, after: query.has('after') ? Number(query.get('after')) : null }, (b) => page.take(b, now, now));
  };
  let end = 0;
  const run = (seconds) => {
    for (let k = 0; k < seconds * RATE / HOP; k++) {
      end += HOP;
      now += HOP / RATE * 1000;
      feed.receive({ g: 1, end, rate: RATE, at: WALL + now, data: tone });
      if (k % 4 === 0) pull();
    }
  };
  run(3600);
  assert.ok(page.window(now, out));
  now += 5000;
  end = 0;
  run(1);
  pull();
  assert.equal(feed.ring.end, end, 'the extension follows the new count');
  assert.equal(page.ring.end, end, 'and so does the page, asking from the start');
  assert.ok(page.window(now, out), 'the picture draws again');
  assert.equal(feed.wait(now), null);
});

test('the page\'s age measure is the engine\'s stamp against this clock, median and worst of the last five seconds', () => {
  const page = new LivePageFeed(), out = new Uint8Array(1024);
  const data = Buffer.from(new Int16Array(HOP).fill(5000).buffer).toString('base64');
  assert.equal(page.ageStats(0), null);
  page.take({ g: 1, end: HOP, rate: RATE, ageMs: 0, heldMs: 0, at: 1000, data }, 0, 0);
  for (const [nowMs, wall] of [[10, 1030], [20, 1040], [30, 1090]]) page.window(nowMs, out, wall);
  assert.deepEqual(page.ageStats(30), { medianMs: 40, worstMs: 90 });
  assert.equal(page.ageStats(6000), null, 'nothing drawn in the last five seconds');
  page.take({ g: 1, end: 2 * HOP, rate: RATE, ageMs: 0, heldMs: 0, at: null, data }, 6000, 6000);
  page.window(6000, out, 9999);
  assert.equal(page.ageStats(6000), null, 'an engine without the stamp: no measure');
});

test('the page says what it waits for, in order, and sound is silence only after the lightshow\'s three quiet seconds', () => {
  const sound = { lightshow: true, live: { listening: true }, heardAgoMs: 10, loudAgoMs: 10 };
  assert.equal(liveAudioWait(sound), null);
  assert.equal(liveAudioWait({ ...sound, lightshow: false, live: null }), 'lightshow');
  assert.equal(liveAudioWait({ ...sound, live: null }), 'live-input', 'a lightshow with no live input');
  assert.equal(liveAudioWait({ ...sound, live: { listening: false } }), 'live-input');
  assert.equal(liveAudioWait({ ...sound, live: undefined }), null, 'an unknown live status is no reason');
  assert.equal(liveAudioWait({ ...sound, heardAgoMs: NO_AUDIO_MS + 1 }), 'audio');
  assert.equal(liveAudioWait({ ...sound, heardAgoMs: Infinity, loudAgoMs: Infinity }), 'audio');
  assert.equal(liveAudioWait({ ...sound, loudAgoMs: QUIET_AFTER_MS }), null);
  assert.equal(liveAudioWait({ ...sound, loudAgoMs: QUIET_AFTER_MS + 1 }), 'silence');

  const ring = new SampleRing(), quiet = new Int16Array(HOP).fill(3), loud = new Int16Array(HOP).fill(4);
  assert.equal(ring.take({ g: 1, end: HOP, rate: RATE, samples: quiet }, 0, 0), true);
  assert.equal(ring.loudAt, -Infinity, 'three sixteen-bit steps are under −80 dBFS');
  ring.take({ g: 1, end: 2 * HOP, rate: RATE, samples: loud }, 10, 0);
  assert.equal(ring.loudAt, 10, 'four are not');
});

test('the renderer gets the newest samples as 1024 time-domain bytes at twice the rate, and silence as 128', () => {
  const samples = Int16Array.from({ length: 600 }, (_, i) => (i < 88 ? 32767 : i % 2 ? 25600 : -25600));
  const bytes = timeBytes(samples);
  assert.equal(bytes.length, 1024);
  assert.deepEqual([...bytes.slice(0, 4)], [28, 128, 228, 128], 'the newest 512 samples, each followed by the mean of it and the next');
  assert.equal(bytes[1023], bytes[1022], 'the newest sample is held, not averaged with nothing');
  assert.ok(timeBytes(new Int16Array(10)).every((b) => b === 128), 'too few samples: silence before them');
  assert.deepEqual([...timeBytes(Int16Array.of(32767, -32768)).slice(-4)], [255, 128, 0, 0]);
});

test('a ring keeps one process\'s newest samples, contiguous, and starts again on a new process or a gap', () => {
  const ring = new SampleRing(8), take = (g, end, values, behind = 0) => ring.take({ g, end, rate: RATE, samples: Int16Array.from(values) }, 0, behind);
  assert.equal(take(1, 4, [1, 2, 3, 4]), true);
  assert.equal(take(1, 4, [1, 2, 3, 4]), false, 'nothing newer');
  assert.equal(take(1, 10, [5, 6, 7, 8, 9, 10]), true);
  assert.deepEqual([...ring.since(-Infinity).samples], [3, 4, 5, 6, 7, 8, 9, 10], 'the newest eight');
  assert.deepEqual(ring.since(8), { from: 8, samples: Int16Array.of(9, 10) });
  assert.equal(take(1, 20, [19, 20]), true);
  assert.deepEqual([...ring.since(-Infinity).samples], [19, 20], 'samples that never came: only what follows them');
  assert.equal(take(1, 22, [21, 22], MAX_AGE_MS + 1), true, 'kept, contiguous');
  assert.ok(!(ring.ageMs(0) <= MAX_AGE_MS), 'but too late to draw');
  assert.equal(take(2, 3, [30, 31, 32]), true, 'a new process');
  assert.deepEqual([ring.g, ring.start, ring.end], [2, 0, 3]);
  for (const bad of [{ g: 2, end: 2, rate: RATE, samples: Int16Array.of(1, 2, 3) }, { g: 2, end: 9, rate: 0, samples: Int16Array.of(1) },
    { g: 'x', end: 9, rate: RATE, samples: Int16Array.of(1) }, { g: 2, end: 9, rate: RATE, samples: [1] }]) assert.equal(ring.take(bad, 0, 0), false);
});

test('an answer the page cannot read is an error of its own, an empty one is nothing newer', () => {
  const page = new LivePageFeed(), data = Buffer.from(new Int16Array(HOP).fill(5000).buffer).toString('base64');
  const good = { g: 1, end: HOP, rate: RATE, ageMs: 0, heldMs: 0, data };
  for (const bad of [null, 'text', { ...good, data: 1 }, { ...good, data: '@@@@' }, { ...good, data: 'AA==' }, { ...good, data: data.slice(0, -1) },
    { ...good, g: null }, { ...good, end: 'x' }, { ...good, rate: 0 }, { ...good, end: 2 }, { ...good, ageMs: null }]) {
    assert.throws(() => page.take(bad, 0, 0), TypeError, JSON.stringify(bad));
  }
  assert.equal(page.take({ wait: 'audio', g: null, end: 0, rate: 0, ageMs: null, data: '' }, 0, 0), false, 'a wait with no samples');
  assert.equal(page.take(good, 0, 0), true);
  assert.equal(page.take(good, 0, 0), false, 'nothing newer');
});

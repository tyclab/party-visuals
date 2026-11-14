'use strict';
// The lightshow's live input for the live-audio page, read-only. Subscribed while a page pulls; a pull
// is answered with the samples newer than the page holds (the newest PULL_SAMPLES at most) as soon as
// there are any, so a page has one request out and never a queue. The token never reaches a page.
const { ArrivalClock, SampleRing, liveAudioWait } = require('../shared/samples.js');

/** Nobody pulled for this long: nobody draws, so the lightshow stops sending. */
const KEEP_MS = 5000;
/** A pull waits this long for samples, then is answered with what the page waits for. */
const HOLD_MS = 500;
const MAX_WAITING = 8;
const MAX_PACKET_BYTES = 16384;

/** A packet's samples as 16-bit integers; null for anything that is not whole little-endian samples. */
function int16(data) {
  const bytes = Buffer.isBuffer(data) ? data : ArrayBuffer.isView(data) ? Buffer.from(data.buffer, data.byteOffset, data.byteLength)
    : data instanceof ArrayBuffer ? Buffer.from(data) : null;
  if (!bytes || !bytes.length || bytes.length % 2 || bytes.length > MAX_PACKET_BYTES) return null;
  const out = new Int16Array(bytes.length / 2);
  for (let i = 0; i < out.length; i++) out[i] = bytes.readInt16LE(2 * i);
  return out;
}

function base64(samples) {
  const bytes = Buffer.alloc(samples.length * 2);
  for (let i = 0; i < samples.length; i++) bytes.writeInt16LE(samples[i], 2 * i);
  return bytes.toString('base64');
}

class SampleFeed {
  constructor({ client, now = Date.now }) {
    this.client = client;
    this.now = now;
    this.clock = new ArrivalClock();
    this.ring = new SampleRing();
    this.waiting = [];
    this.pulledAt = -Infinity;
    this.wanted = false;
  }

  /** A `samples` packet from the lightshow: kept unless it lands too late. */
  receive(packet) {
    const samples = packet && int16(packet.data);
    if (!samples || !Number.isSafeInteger(packet.g) || !Number.isSafeInteger(packet.end) || !Number.isInteger(packet.rate) || packet.rate <= 0) return;
    const now = this.now();
    // A new live input process counts its stream from zero: its arrivals say nothing of the last one's.
    if (packet.g !== this.ring.g) this.clock.reset();
    const behind = this.clock.lateness((packet.end / packet.rate) * 1000, now);
    if (this.ring.take({ g: packet.g, end: packet.end, rate: packet.rate, samples }, now, behind)) this.answer(now);
  }

  /** A page wants the samples after stream position `after` of process `g`; `respond(body)` is called once. */
  pull({ g = null, after = null } = {}, respond) {
    const now = this.now();
    this.pulledAt = now;
    this.want(true);
    const position = g === this.ring.g && Number.isSafeInteger(after) ? after : -Infinity;
    if (this.ring.end > Math.max(position, this.ring.start) || this.waiting.length >= MAX_WAITING) respond(this.body(position, now));
    else this.waiting.push({ position, respond, at: now });
  }

  /** A page that went away before its answer. */
  cancel(respond) {
    this.waiting = this.waiting.filter((pull) => pull.respond !== respond);
  }

  /** A few times a second: answer pulls that waited long enough, and unsubscribe once nobody pulls. */
  tick() {
    const now = this.now(), due = this.waiting.filter((pull) => now - pull.at >= HOLD_MS);
    if (due.length) this.waiting = this.waiting.filter((pull) => now - pull.at < HOLD_MS);
    for (const pull of due) pull.respond(this.body(pull.position, now));
    if (now - this.pulledAt >= KEEP_MS) this.want(false);
  }

  stop() {
    this.answer(this.now());
    this.want(false);
  }

  want(on) {
    if (this.wanted === on) return;
    this.wanted = on;
    this.client.wantSamples(on);
  }

  answer(now) {
    const waiting = this.waiting;
    this.waiting = [];
    for (const pull of waiting) pull.respond(this.body(pull.position, now));
  }

  wait(now) {
    const status = this.client.status;
    return liveAudioWait({ lightshow: status.status === 'connected' && status.via === 'socket', live: this.client.live(),
      heardAgoMs: now - this.ring.heardAt, loudAgoMs: now - this.ring.loudAt });
  }

  body(position, now) {
    const { from, samples } = this.ring.since(position);
    return { wait: this.wait(now), g: this.ring.g, rate: this.ring.rate, from, end: samples.length ? from + samples.length : this.ring.end, data: base64(samples),
      ageMs: Number.isFinite(this.ring.freshAt) ? Math.max(0, now - this.ring.freshAt) : null, sentAt: now };
  }
}

module.exports = { SampleFeed, KEEP_MS, HOLD_MS };

'use strict';
// The lightshow's `samples` relay, as tyclab/lightshow src/server/sample-relay.ts has it at 69d409f1, so
// the extension and the page are tested against what the engine sends, not against what they expect.
// Modelled: subscribers by socket; each has at most one packet unacknowledged, released by the
// acknowledgement or its 1 s timeout; samples arriving meanwhile wait in a ring of the newest 1024 and go
// with the next packet, contiguous, only the newest 1024 after a longer wait; a block from another stream,
// another rate or not following the last starts the stream again; the last subscriber leaving resets it.
// A restarted lightshow is a block whose `end` counts from zero again, under any `g`, the same one too.
// Not modelled: the live input process being asked for samples (want), which the engine does on its side.
const PACKET = 1024;
const ACK_MS = 1000;

class MockRelay {
  constructor() {
    this.subscribers = new Map();
    this.ring = Buffer.alloc(PACKET * 2);
    this.stream = -1;
    this.rate = 0;
    this.start = 0;
    this.end = 0;
    this.at = NaN;
    this.seen = { packets: 0, acks: 0, timeouts: 0 };
  }

  get count() { return this.subscribers.size; }

  add(socket) {
    if (!this.subscribers.has(socket.id)) this.subscribers.set(socket.id, { socket, waiting: false, sent: this.end });
  }

  remove(id) {
    if (this.subscribers.delete(id) && !this.subscribers.size) this.stream = -1;
  }

  /** One block of the live input: `{ g, end, rate, at, data }`, `data` 16-bit little-endian samples ending at `end`. */
  push({ g, end, rate, at, data }) {
    const count = data.length / 2, first = end - count;
    if (!this.subscribers.size || first < 0) return;
    if (g !== this.stream || rate !== this.rate || first !== this.end) {
      this.stream = g;
      this.rate = rate;
      this.start = this.end = first;
      for (const subscriber of this.subscribers.values()) subscriber.sent = first;
    }
    const keep = Math.min(count, PACKET), bytes = data.subarray(data.length - keep * 2);
    const offset = ((end - keep) % PACKET) * 2, head = Math.min(bytes.length, this.ring.length - offset);
    bytes.copy(this.ring, offset, 0, head);
    bytes.copy(this.ring, 0, head);
    this.end = end;
    this.start = Math.max(this.start, end - PACKET);
    this.at = at;
    for (const subscriber of this.subscribers.values()) if (!subscriber.waiting) this.send(subscriber);
  }

  read(from) {
    const out = Buffer.alloc((this.end - from) * 2);
    const offset = (from % PACKET) * 2, head = Math.min(out.length, this.ring.length - offset);
    this.ring.copy(out, 0, offset, offset + head);
    this.ring.copy(out, head, 0, out.length - head);
    return out;
  }

  send(subscriber) {
    const from = Math.max(subscriber.sent, this.start);
    if (from >= this.end || !subscriber.socket.connected) return;
    subscriber.sent = this.end;
    subscriber.waiting = true;
    this.seen.packets++;
    subscriber.socket.timeout(ACK_MS).emit('samples', { g: this.stream, end: this.end, rate: this.rate, data: this.read(from), at: this.at }, (err) => {
      if (err) this.seen.timeouts++; else this.seen.acks++;
      subscriber.waiting = false;
      if (this.subscribers.get(subscriber.socket.id) === subscriber) this.send(subscriber);
    });
  }
}

module.exports = { MockRelay };

'use strict';
/**
 * A read-only client of the lightshow server.
 *
 * It connects over Socket.IO asking for protocol 2 (the token in the
 * handshake's `auth`, as the lightshow's own pages send it), keeps a mirror
 * of the live state from the snapshot and the patches that follow, and
 * reports the parts the graphics use as `look` events. While the socket is
 * down it polls GET /api/state instead (the token in the X-Lightshow-Token
 * header), so a proxy that will not carry the socket still gets the tempo and
 * the colours through.
 *
 * Reconnection is this client's own, not Socket.IO's: Socket.IO stops
 * retrying after a refused handshake, and a token fixed on the lightshow's
 * side should be picked up without restarting NodeCG.
 *
 * Events:
 *   status  { status, via, since, lastUpdate, error, retryInMs, target }
 *           status: connecting | connected | reconnecting | error | stopped
 *   look    readLook()'s fields and `changed`, the keys that arrived
 */
const { EventEmitter } = require('node:events');
const { StateMirror, readLook } = require('./protocol.js');
const { Backoff } = require('./backoff.js');

const PROTOCOL = 2;

/** The lightshow's address, checked: http(s), and no credentials in it. */
function parseTarget(raw) {
  let url;
  try {
    url = new URL(String(raw || ''));
  } catch {
    return { error: 'no valid lightshow address in the bundle config (lightshow.url)' };
  }
  if (url.protocol !== 'http:' && url.protocol !== 'https:') {
    return { error: 'the lightshow address (lightshow.url) must start with http:// or https://' };
  }
  // The bundle config is served to every page of the bundle, so a secret in
  // the address would be readable by anything that can open a graphic.
  if (url.username || url.password || url.searchParams.has('token')) {
    return { error: 'the lightshow address (lightshow.url) carries credentials; put the token in lightshow.tokenFile' };
  }
  const base = url.pathname.replace(/\/+$/, '');
  return { origin: url.origin, base, display: url.origin + base };
}

class LightshowClient extends EventEmitter {
  #token;

  constructor({ url, token = '', pollMs = 1000, requestTimeoutMs = 5000, backoff = {}, io = null, fetch = globalThis.fetch, now = Date.now } = {}) {
    super();
    this.target = parseTarget(url);
    this.#token = token ? String(token) : '';
    this.pollMs = Math.max(50, Number(pollMs) || 1000);
    this.requestTimeoutMs = Math.max(100, Number(requestTimeoutMs) || 5000);
    this.backoff = new Backoff(backoff);
    this.io = io;
    this.fetch = fetch;
    this.now = now;

    this.mirror = new StateMirror();
    this.socket = null;
    this.retryTimer = null;
    this.retryInMs = null;
    this.pollTimer = null;
    this.pollAbort = null;
    this.resyncing = false;
    this.stopped = true;

    // What the status is computed from.
    this.socketUp = false;
    this.httpUp = false;
    this.attempted = false;
    this.refused = null;
    this.socketError = null;
    this.httpError = null;
    this.since = null;
    this.lastUpdate = null;
    this._status = this._compute();
  }

  get status() {
    return { ...this._status };
  }

  start() {
    if (!this.stopped) return;
    this.stopped = false;
    this._update();
    if (this.target.error) return;
    this._openSocket();
  }

  stop() {
    this.stopped = true;
    clearTimeout(this.retryTimer);
    clearTimeout(this.pollTimer);
    this.retryTimer = this.pollTimer = null;
    this.retryInMs = null;
    if (this.pollAbort) this.pollAbort.abort();
    this._disposeSocket();
    this.socketUp = this.httpUp = false;
    this._update();
  }

  // ── Socket.IO ────────────────────────────────────────────────────────────

  _openSocket() {
    const io = this.io || require('socket.io-client').io;
    // A fresh socket per attempt: nothing of a failed one carries over.
    const socket = io(this.target.origin, {
      path: `${this.target.base}/socket.io`,
      // WebSocket first, as the lightshow's own pages connect; long-polling
      // if something on the way will not upgrade the connection.
      transports: ['websocket', 'polling'],
      tryAllTransports: true,
      reconnection: false,
      autoConnect: false,
      forceNew: true,
      timeout: this.requestTimeoutMs,
      auth: { token: this.#token, protocol: PROTOCOL },
    });
    this.socket = socket;

    socket.on('connect', () => {
      this.socketUp = true;
      this.httpUp = false;
      this.refused = this.socketError = this.httpError = null;
      this.backoff.reset();
      clearTimeout(this.pollTimer);
      this.pollTimer = null;
      this._update();
    });
    socket.on('snapshot', (snapshot) => {
      if (this.mirror.applySnapshot(snapshot)) this._received();
    });
    socket.on('patch', (patch) => this._onPatch(patch));
    // The original protocol, from a lightshow that does not speak protocol 2.
    socket.on('state', (state) => {
      if (this.mirror.applyFullState(state)) this._received();
    });
    socket.on('connect_error', (err) => {
      this.socketUp = false;
      this.attempted = true;
      if (err && err.data && err.data.code === 'unauthorized') {
        // Polling with the same token would be refused too; retrying the
        // socket is enough to notice when the token is accepted again.
        this.refused = `the lightshow refused the token: ${this._clean(err.message)}`;
      } else {
        // A transport failure, or the handshake refused for the host name:
        // one poll tells which, and the poll stops again if it is refused.
        this.refused = null;
        this.socketError = `cannot reach the lightshow's socket: ${this._clean(err && err.message)}`;
        this._startPolling();
      }
      this._retryLater();
    });
    socket.on('disconnect', (reason) => {
      this.socketUp = false;
      this.attempted = true;
      this.socketError = `the lightshow's socket closed (${this._clean(reason)})`;
      this._startPolling();
      this._retryLater();
    });
    socket.connect();
  }

  _disposeSocket() {
    const socket = this.socket;
    this.socket = null;
    this.resyncing = false;
    if (!socket) return;
    socket.removeAllListeners();
    socket.disconnect();
  }

  /** One retry at a time, however many errors the failed attempt raised. */
  _retryLater() {
    if (this.stopped || this.retryTimer) {
      this._update();
      return;
    }
    this._disposeSocket();
    this.retryInMs = this.backoff.next();
    this.retryTimer = setTimeout(() => {
      this.retryTimer = null;
      this.retryInMs = null;
      if (this.stopped) return;
      this._update();
      this._openSocket();
    }, this.retryInMs);
    this._update();
  }

  _onPatch(patch) {
    const result = this.mirror.applyPatch(patch);
    if (result === 'ok') {
      this.lastUpdate = this.now();
      if (this.mirror.touchedLook()) this._emitLook();
      this._update();
    } else if (result === 'gap' && !this.resyncing && this.socket) {
      // A patch went missing: ask for the whole state rather than drift.
      this.resyncing = true;
      const socket = this.socket;
      socket.timeout(this.requestTimeoutMs).emit('sync', (err, snapshot) => {
        if (socket !== this.socket) return;
        this.resyncing = false;
        if (!err && this.mirror.applySnapshot(snapshot)) this._received();
      });
    }
  }

  // ── GET /api/state ───────────────────────────────────────────────────────

  _startPolling() {
    if (this.stopped || this.pollTimer || this.pollAbort) return;
    this._poll();
  }

  async _poll() {
    if (this.stopped || this.socketUp) return;
    const abort = new AbortController();
    this.pollAbort = abort;
    const timer = setTimeout(() => abort.abort(), this.requestTimeoutMs);
    try {
      const headers = { accept: 'application/json' };
      if (this.#token) headers['x-lightshow-token'] = this.#token;
      const res = await this.fetch(`${this.target.display}/api/state`, { headers, signal: abort.signal });
      if (res.status === 401) {
        // An unread body holds the connection open until it is collected.
        await res.body?.cancel();
        this.httpUp = false;
        this.refused = 'the lightshow refused the token (HTTP 401)';
      } else if (res.status === 403) {
        // The lightshow answers 403 before it looks at the token: to a host
        // name it is not known by, or to a browser's cross-origin request.
        await res.body?.cancel();
        this.httpUp = false;
        this.refused = 'the lightshow refused the request (HTTP 403): it does not answer to this host name; set it as its Public URL';
      } else if (!res.ok) {
        await res.body?.cancel();
        this.httpUp = false;
        this.httpError = `GET /api/state answered HTTP ${res.status}`;
      } else {
        const state = await res.json();
        // The socket may have come back while this request was out; it wins.
        if (!this.stopped && !this.socketUp && this.mirror.applyFullState(state)) {
          this.httpUp = true;
          this.refused = this.httpError = null;
          this._received();
        }
      }
    } catch (err) {
      this.httpUp = false;
      this.httpError = abort.signal.aborted && !this.stopped
        ? `GET /api/state had no answer within ${this.requestTimeoutMs} ms`
        : `GET /api/state failed: ${this._clean((err && err.cause && err.cause.code) || (err && err.message))}`;
    } finally {
      clearTimeout(timer);
      if (this.pollAbort === abort) this.pollAbort = null;
    }
    if (this.stopped || this.socketUp) return;
    this._update();
    // A refusal will not change by asking again every second; the socket's
    // next attempt, at its backoff, polls once more if it fails.
    if (this.refused) return;
    this.pollTimer = setTimeout(() => {
      this.pollTimer = null;
      this._poll();
    }, this.pollMs);
  }

  // ── State and status ─────────────────────────────────────────────────────

  _received() {
    this.lastUpdate = this.now();
    this._emitLook();
    this._update();
  }

  _emitLook() {
    this.emit('look', { ...readLook(this.mirror.state), changed: [...this.mirror.lastChanged] });
  }

  /**
   * The status from what is known: a working socket or poll is connected
   * whatever the other path says; a refusal is an error, since it will not
   * fix itself; anything else that failed is a retry in progress.
   */
  _compute() {
    const connected = !this.stopped && (this.socketUp || this.httpUp);
    let status;
    if (this.stopped) status = 'stopped';
    else if (this.target.error) status = 'error';
    else if (connected) status = 'connected';
    else if (this.refused) status = 'error';
    else if (this.attempted) status = 'reconnecting';
    else status = 'connecting';
    if (!connected) this.since = null;
    else if (this.since === null) this.since = this.now();
    let error = null;
    if (this.target.error) error = this.target.error;
    else if (!connected && !this.stopped) error = this.refused || this.socketError || this.httpError;
    return {
      status,
      via: connected ? (this.socketUp ? 'socket' : 'http') : null,
      since: this.since,
      lastUpdate: this.lastUpdate,
      error,
      retryInMs: this.retryInMs,
      target: this.target.display || null,
    };
  }

  _update() {
    const next = this._compute();
    const prev = this._status;
    if (JSON.stringify(next) === JSON.stringify(prev)) return;
    // A busy lightshow patches many times a second; the time of the last
    // update is worth a status of its own only once a second.
    if (prev && prev.lastUpdate !== null && next.lastUpdate - prev.lastUpdate < 1000
      && JSON.stringify({ ...next, lastUpdate: 0 }) === JSON.stringify({ ...prev, lastUpdate: 0 })) return;
    this._status = next;
    this.emit('status', this.status);
  }

  /** A message for the status line: short, and never carrying the token. */
  _clean(text) {
    let s = String(text || 'unknown error');
    if (this.#token) s = s.split(this.#token).join('***');
    return s.length > 160 ? `${s.slice(0, 157)}...` : s;
  }
}

module.exports = { LightshowClient, parseTarget, PROTOCOL };

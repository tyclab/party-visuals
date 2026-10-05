'use strict';
// A stand-in for the lightshow server: the parts of its protocol 2 this
// bundle reads (snapshot, patch, sync), the original `state` event for a
// client that does not ask for protocol 2, the token check on the handshake
// and GET /api/state. Loopback only, on a port the system picks.
const http = require('node:http');
const { Server } = require('socket.io');

// The lightshow's colour presets, as its catalogue sends them.
const PRESETS = [
  { name: 'Red', r: 255, g: 0, b: 0, w: 0, a: 0, uv: 0 },
  { name: 'Amber', r: 200, g: 150, b: 0, w: 0, a: 255, uv: 0 },
  { name: 'Lime', r: 150, g: 255, b: 0, w: 0, a: 0, uv: 0 },
  { name: 'Green', r: 0, g: 255, b: 85, w: 0, a: 0, uv: 0 },
  { name: 'Cyan', r: 0, g: 225, b: 255, w: 0, a: 0, uv: 0 },
  { name: 'Blue', r: 0, g: 85, b: 255, w: 0, a: 0, uv: 0 },
  { name: 'Congo Blue', r: 75, g: 0, b: 255, w: 0, a: 0, uv: 0 },
  { name: 'Violet', r: 205, g: 0, b: 255, w: 0, a: 0, uv: 0 },
  { name: 'Magenta', r: 255, g: 0, b: 150, w: 0, a: 0, uv: 0 },
  { name: 'Warm White', r: 90, g: 30, b: 0, w: 255, a: 200, uv: 0 },
  { name: 'Cool White', r: 0, g: 30, b: 80, w: 255, a: 0, uv: 0 },
  { name: 'Lavender', r: 130, g: 45, b: 200, w: 200, a: 0, uv: 0 },
  { name: 'Moonlight', r: 0, g: 70, b: 190, w: 190, a: 0, uv: 0 },
  { name: 'UV', r: 0, g: 0, b: 0, w: 0, a: 0, uv: 255 },
  { name: 'Blackout', r: 0, g: 0, b: 0, w: 0, a: 0, uv: 0 },
];

const DOMAIN_OF = {
  bpm: 'look', clock: 'look', colorA: 'look', colorB: 'look', colorC: 'look', colorD: 'look',
  palette: 'look', paletteOverride: 'look', masterDimmer: 'look',
  fixtures: 'rig', nowPlaying: 'sources', colorPresets: 'catalogs',
};
const domainOf = (key) => DOMAIN_OF[key] || 'system';

function baseState() {
  return {
    bpm: 120,
    clock: { source: 'tap', bpm: 120 },
    colorA: 8, colorB: 4, colorC: 6, colorD: 12,
    palette: 'synthwave',
    masterDimmer: 255,
    fixtures: [],
    colorPresets: PRESETS,
  };
}

function listen(server, port) {
  return new Promise((resolve, reject) => {
    server.once('error', reject);
    server.listen(port, '127.0.0.1', () => {
      server.off('error', reject);
      resolve();
    });
  });
}

/**
 * Start a mock lightshow. `socket: false` serves only HTTP, so a Socket.IO
 * client fails its handshake and has to fall back to polling.
 */
async function startMockLightshow({ token = 'mock-token', port = 0, socket = true, state = {} } = {}) {
  const live = { ...baseState(), ...state };
  const versions = { look: 0, rig: 0, show: 0, sources: 0, catalogs: 0, system: 0 };
  const seen = { stateRequests: 0, stateTokens: [], handshakes: [], syncs: 0 };

  const server = http.createServer((req, res) => {
    if (req.url.split('?')[0] === '/api/state') {
      seen.stateRequests++;
      seen.stateTokens.push(req.headers['x-lightshow-token'] || '');
      if (token && req.headers['x-lightshow-token'] !== token) {
        res.writeHead(401, { 'content-type': 'application/json' });
        res.end(JSON.stringify({ ok: false, error: 'Missing or invalid token.' }));
        return;
      }
      res.writeHead(200, { 'content-type': 'application/json' });
      res.end(JSON.stringify({ ...live, dmxSnapshot: {} }));
      return;
    }
    res.writeHead(404);
    res.end();
  });

  let io = null;
  if (socket) {
    io = new Server(server);
    io.use((s, next) => {
      const auth = s.handshake.auth || {};
      seen.handshakes.push({ protocol: auth.protocol, token: auth.token || '' });
      const presented = auth.token || '';
      if (!token || presented === token) return next();
      // The lightshow's refusal, word for word in shape: a message and a code.
      const err = new Error(presented ? 'Access token refused' : 'This server requires an access token');
      err.data = { code: 'unauthorized', presented: !!presented };
      return next(err);
    });
    io.on('connection', (s) => {
      const snapshot = () => ({ protocol: 2, versions: { ...versions }, state: { ...live } });
      if (s.handshake.auth && s.handshake.auth.protocol === 2) {
        s.join('v2');
        s.emit('snapshot', snapshot());
        s.on('sync', (ack) => {
          seen.syncs++;
          if (typeof ack === 'function') ack(snapshot());
          else s.emit('snapshot', snapshot());
        });
      } else {
        s.join('v1');
        s.emit('state', live);
      }
    });
  }

  await listen(server, port);
  const actualPort = server.address().port;

  return {
    url: `http://127.0.0.1:${actualPort}`,
    port: actualPort,
    state: live,
    versions,
    seen,

    /** Change keys and send the patches; `drop` loses them on the way, as a missed message would be. */
    publish(changes, { drop = false } = {}) {
      Object.assign(live, changes);
      const groups = new Map();
      for (const [key, value] of Object.entries(changes)) {
        const d = domainOf(key);
        if (!groups.has(d)) groups.set(d, {});
        groups.get(d)[key] = value;
      }
      for (const [d, set] of groups) {
        versions[d]++;
        if (!drop && io) io.to('v2').emit('patch', { d, v: versions[d], set });
      }
      if (io) io.to('v1').emit('state', live);
    },

    clients() {
      return io ? io.of('/').sockets.size : 0;
    },

    async close() {
      if (io) io.disconnectSockets(true);
      server.closeAllConnections();
      await new Promise((resolve) => {
        if (io) io.close(() => resolve());
        else server.close(() => resolve());
      });
    },
  };
}

module.exports = { startMockLightshow, baseState, PRESETS };

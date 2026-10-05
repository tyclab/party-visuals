'use strict';
/**
 * The bundle's HTTP addresses, under /bundles/party-visuals/ (NodeCG's login
 * applies when it is on):
 *
 *   GET  api/cmd/<target>/<cmd>   a command, for Companion's Generic HTTP
 *   POST api/cmd                  { target, cmd }
 *   GET  api/state                switches, connection, tempo and palette
 *
 * A command changes what is on air, so it is refused when a browser says it
 * comes from another site, and every api/ address answers only to IP
 * addresses, localhost, *.local and the configured `allowHosts` names, which
 * keeps a page that re-points its own name at this machine (DNS rebinding)
 * out. Companion and curl send no Origin and are not affected.
 */
const net = require('node:net');

const MAX_BODY = 16 * 1024;

/** The host name in a Host header: lower-case, no port, no brackets. */
function hostnameOf(header) {
  let raw = String(header || '').trim().toLowerCase();
  if (raw.startsWith('[')) return raw.slice(1, raw.indexOf(']') > 0 ? raw.indexOf(']') : undefined);
  const colon = raw.indexOf(':');
  if (colon >= 0 && colon === raw.lastIndexOf(':')) raw = raw.slice(0, colon);
  return raw.endsWith('.') ? raw.slice(0, -1) : raw;
}

function hostAllowed(header, allowHosts = []) {
  if (header === undefined || header === '') return true;
  const name = hostnameOf(header);
  if (!name) return false;
  if (net.isIP(name)) return true;
  if (name === 'localhost' || name.endsWith('.localhost') || name.endsWith('.local')) return true;
  return allowHosts.some((h) => hostnameOf(h) === name);
}

/** Why a browser request must not run a command, or null. */
function crossSiteReason(req) {
  const site = req.headers['sec-fetch-site'];
  if (site === 'cross-site' || site === 'same-site') return 'refused: the request comes from another site';
  const origin = req.headers.origin;
  if (origin === undefined) return null;
  try {
    if (new URL(origin).host === String(req.headers.host || '').toLowerCase()) return null;
  } catch {
    // an Origin of "null" or garbage: refused below
  }
  return 'refused: the request comes from another origin';
}

function send(res, status, body) {
  res.statusCode = status;
  res.setHeader('Content-Type', 'application/json; charset=utf-8');
  res.setHeader('Cache-Control', 'no-store');
  res.end(JSON.stringify(body));
}

function readJson(req) {
  // NodeCG parses JSON bodies before a bundle's routes see them.
  if (req._body && req.body && typeof req.body === 'object') return Promise.resolve(req.body);
  return new Promise((resolve, reject) => {
    let size = 0;
    const chunks = [];
    req.on('data', (chunk) => {
      size += chunk.length;
      if (size > MAX_BODY) {
        reject(new Error('body too large'));
        req.destroy();
      } else chunks.push(chunk);
    });
    req.on('end', () => {
      try {
        resolve(chunks.length ? JSON.parse(Buffer.concat(chunks).toString('utf8')) : {});
      } catch (err) {
        reject(err);
      }
    });
    req.on('error', reject);
  });
}

/**
 * Express-style middleware: `run(target, cmd)` applies a command and returns
 * `{ ok, ... }`; `state()` is what api/state answers. Anything that is not
 * api/ goes on to NodeCG.
 */
function createHttpHandler({ run, state, allowHosts = [] }) {
  return function partyVisualsHttp(req, res, next) {
    const url = new URL(req.url, 'http://bundle.invalid');
    const path = url.pathname;
    if (!path.startsWith('/api/')) return next();
    if (!hostAllowed(req.headers.host, allowHosts)) return send(res, 403, { ok: false, error: 'host not allowed (add it to allowHosts)' });

    if (path === '/api/state' && (req.method === 'GET' || req.method === 'HEAD')) return send(res, 200, state());

    const m = /^\/api\/cmd\/([^/]+)\/([^/]+)$/.exec(path);
    if (m && req.method === 'GET') {
      const why = crossSiteReason(req);
      if (why) return send(res, 403, { ok: false, error: why });
      let target, cmd;
      try {
        target = decodeURIComponent(m[1]);
        cmd = decodeURIComponent(m[2]);
      } catch {
        return send(res, 400, { ok: false, error: 'malformed address' });
      }
      const result = run(target, cmd);
      return send(res, result.ok ? 200 : 400, result);
    }
    if (path === '/api/cmd' && req.method === 'POST') {
      const why = crossSiteReason(req);
      if (why) return send(res, 403, { ok: false, error: why });
      readJson(req).then((body) => {
        const result = run(body && body.target, body && body.cmd);
        send(res, result.ok ? 200 : 400, result);
      }, () => send(res, 400, { ok: false, error: 'the body must be JSON: {"target": "…", "cmd": "…"}' }));
      return undefined;
    }
    return next();
  };
}

module.exports = { createHttpHandler, hostAllowed, hostnameOf, crossSiteReason };

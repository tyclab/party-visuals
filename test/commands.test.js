'use strict';
const test = require('node:test');
const assert = require('node:assert/strict');
const http = require('node:http');
const { runCommand, DEFAULT_CONTROLS } = require('../extension/commands.js');
const { createHttpHandler } = require('../extension/http.js');

test('air on, off and toggle switch one graphic or both', () => {
  let r = runCommand(DEFAULT_CONTROLS, 'wash', 'air.off');
  assert.equal(r.ok, true);
  assert.equal(r.controls.wash.on, false);
  assert.equal(r.controls.bar.on, true);
  r = runCommand(r.controls, 'wash', 'air.toggle');
  assert.equal(r.controls.wash.on, true);
  r = runCommand(r.controls, 'all', 'air.off');
  assert.deepEqual([r.controls.wash.on, r.controls.bar.on], [false, false]);
  assert.equal(DEFAULT_CONTROLS.wash.on, true, 'the defaults are never edited in place');
});

test('intensity is set in percent, or nudged, and held to 0..100', () => {
  let r = runCommand(DEFAULT_CONTROLS, 'bar', 'intensity.35');
  assert.equal(r.controls.bar.intensity, 35);
  r = runCommand(r.controls, 'bar', 'intensity.+10');
  assert.equal(r.controls.bar.intensity, 45);
  r = runCommand(r.controls, 'bar', 'intensity.-60');
  assert.equal(r.controls.bar.intensity, 0);
  r = runCommand(r.controls, 'all', 'intensity.100');
  assert.deepEqual([r.controls.wash.intensity, r.controls.bar.intensity], [100, 100]);
  r = runCommand(r.controls, 'wash', 'intensity.250');
  assert.equal(r.ok, false);
});

test('an unknown target or command is refused with the reason', () => {
  for (const [target, cmd] of [['strobe', 'air.on'], ['wash', 'air.maybe'], ['wash', 'intensity.loud'], ['wash', ''], [undefined, 'air.on']]) {
    const r = runCommand(DEFAULT_CONTROLS, target, cmd);
    assert.equal(r.ok, false, `${target} ${cmd}`);
    assert.equal(typeof r.error, 'string');
  }
});

async function serve(handler) {
  const server = http.createServer((req, res) => handler(req, res, () => {
    res.writeHead(404);
    res.end('next');
  }));
  await new Promise((r) => server.listen(0, '127.0.0.1', r));
  const base = `http://127.0.0.1:${server.address().port}`;
  return { base, close: () => new Promise((r) => { server.closeAllConnections(); server.close(r); }) };
}

function handlerWith(allowHosts = []) {
  let controls = structuredClone(DEFAULT_CONTROLS);
  const handler = createHttpHandler({
    run(target, cmd) {
      const r = runCommand(controls, target, cmd);
      if (r.ok) controls = r.controls;
      return r;
    },
    state: () => ({ controls, connection: { status: 'connected' } }),
    allowHosts,
  });
  return { handler, controls: () => controls };
}

test('Companion\'s Generic HTTP: GET api/cmd/<target>/<cmd> runs a command', async (t) => {
  const { handler, controls } = handlerWith();
  const srv = await serve(handler);
  t.after(srv.close);
  let res = await fetch(`${srv.base}/api/cmd/wash/air.off`);
  assert.equal(res.status, 200);
  assert.deepEqual((await res.json()).ok, true);
  assert.equal(controls().wash.on, false);

  res = await fetch(`${srv.base}/api/cmd/bar/intensity.20`);
  assert.equal(res.status, 200);
  assert.equal(controls().bar.intensity, 20);

  res = await fetch(`${srv.base}/api/cmd/bar/intensity.loud`);
  assert.equal(res.status, 400);
  assert.equal((await res.json()).ok, false);

  res = await fetch(`${srv.base}/api/cmd`, { method: 'POST', headers: { 'content-type': 'application/json' }, body: JSON.stringify({ target: 'all', cmd: 'air.on' }) });
  assert.equal(res.status, 200);
  assert.equal(controls().wash.on, true);

  res = await fetch(`${srv.base}/api/state`);
  assert.deepEqual(await res.json(), { controls: controls(), connection: { status: 'connected' } });

  res = await fetch(`${srv.base}/graphics/wash.html`);
  assert.equal(await res.text(), 'next', 'everything else is left to NodeCG');
});

test('a command from another site, or to a host name this machine is not known by, is refused', async (t) => {
  const { handler, controls } = handlerWith(['show-pc']);
  const srv = await serve(handler);
  t.after(srv.close);
  const port = new URL(srv.base).port;
  const get = (headers) => new Promise((resolve, reject) => {
    const req = http.get({ host: '127.0.0.1', port, path: '/api/cmd/wash/air.off', headers }, (res) => {
      res.resume();
      res.on('end', () => resolve(res.statusCode));
    });
    req.on('error', reject);
  });
  assert.equal(await get({ 'sec-fetch-site': 'cross-site' }), 403);
  assert.equal(await get({ origin: 'http://evil.example' }), 403);
  assert.equal(await get({ host: `evil.example:${port}` }), 403);
  assert.equal(controls().wash.on, true, 'none of those ran');
  assert.equal(await get({ host: `show-pc:${port}` }), 200, 'a name in allowHosts');
  assert.equal(await get({ host: `regie.local:${port}`, origin: `http://regie.local:${port}` }), 200, 'same origin, .local name');
  assert.equal(await get({ host: `localhost:${port}`, 'sec-fetch-site': 'same-origin' }), 200);
});

'use strict';
/**
 * party-visuals: the lightshow's beat and colours for OBS graphics.
 *
 * The extension is the only part that talks to the lightshow. It reads the
 * address and the token file from the bundle config, follows the lightshow
 * read-only, and publishes what the graphics need as Replicants:
 *
 *   bpm              the tempo, or null before the lightshow has sent one
 *   beatPos          { beat, bpm, at, epoch, locked }: the beat position at
 *                    `at` (ms since 1970); graphics extrapolate from it
 *   clockSource      what the lightshow's clock follows (auto, cdj, track, live, tap)
 *   palette          the look's colours as #RRGGBB, kept across restarts
 *   paletteOverride  the lightshow's palette override as #RRGGBB, or null
 *   connection       { status, via, since, lastUpdate, error, retryInMs, target }
 *   controls         { wash: { on, intensity }, bar: { on, intensity } },
 *                    kept across restarts
 *
 * The token never reaches a Replicant, a page or the log: NodeCG hands the
 * bundle config to every page of the bundle, so the config names a file and
 * only this process reads it.
 */
const fs = require('node:fs');
const path = require('node:path');
const { LightshowClient } = require('./lightshow-client.js');
const { runCommand, normaliseControls, DEFAULT_CONTROLS } = require('./commands.js');
const { createHttpHandler } = require('./http.js');
const { BeatClock, clampBpm } = require('../shared/beat-clock.js');

const BUNDLE = 'party-visuals';
// How often the beat position is re-published while connected: graphics run
// their own clocks, this only keeps a late-opened page and the dashboard close.
const BEAT_REFRESH_MS = 500;

/** The token from the configured file, trimmed; relative paths start at NodeCG's folder. */
function readToken(tokenFile) {
  if (!tokenFile) return { token: '' };
  try {
    const token = fs.readFileSync(path.resolve(process.cwd(), tokenFile), 'utf8').trim();
    return token ? { token } : { error: 'the token file (lightshow.tokenFile) is empty' };
  } catch (err) {
    return { error: `the token file (lightshow.tokenFile) cannot be read (${err.code || 'error'})` };
  }
}

const same = (a, b) => JSON.stringify(a) === JSON.stringify(b);

module.exports = function partyVisuals(nodecg) {
  const config = nodecg.bundleConfig || {};
  const lightshow = config.lightshow || {};
  const log = nodecg.log;

  const replicant = (name, defaultValue, persistent = false) => nodecg.Replicant(name, { defaultValue, persistent });
  const R = {
    bpm: replicant('bpm', null),
    beatPos: replicant('beatPos', { beat: 0, bpm: 120, at: 0, epoch: 0, locked: false }),
    clockSource: replicant('clockSource', null),
    palette: replicant('palette', [], true),
    paletteOverride: replicant('paletteOverride', null),
    connection: replicant('connection', {
      status: 'connecting', via: null, since: null, lastUpdate: null, error: null, retryInMs: null, target: null,
    }),
    controls: replicant('controls', structuredClone(DEFAULT_CONTROLS), true),
  };
  const set = (rep, value) => {
    if (!same(rep.value, value)) rep.value = value;
  };
  set(R.controls, normaliseControls(R.controls.value));

  // ── The lightshow ─────────────────────────────────────────────────────────
  const clock = new BeatClock();
  const publishBeat = () => {
    if (!clock.started) return;
    const now = Date.now();
    set(R.beatPos, { beat: Math.round(clock.at(now) * 1e4) / 1e4, bpm: clock.bpm, at: now, epoch: clock.epoch, locked: clock.locked });
  };

  const { token, error: tokenError } = readToken(lightshow.tokenFile);
  const client = new LightshowClient({
    url: lightshow.url,
    token,
    pollMs: lightshow.pollMs,
    backoff: lightshow.reconnect,
  });

  client.on('look', (look) => {
    // Only a reading that arrived now moves the clock: a palette patch
    // carries no news about the beat, and the mirror's beat is old by then.
    // The beat position rides on `clock`; a patch to the typed bpm alone is
    // a tempo change only while no clock tempo is in force (an old
    // lightshow), and never a fresh position.
    const fresh = look.changed.includes('clock');
    const tempo = look.bpm !== null && (fresh || clampBpm(look.bpm) !== clock.bpm);
    if (tempo) {
      clock.update({
        bpm: look.bpm,
        beat: fresh ? look.beat : null,
        epoch: fresh ? look.epoch : null,
        atMs: Date.now(),
      });
    }
    set(R.bpm, look.bpm);
    set(R.clockSource, look.clockSource);
    // No colours sent (an old lightshow, a catalogue missing): keep the last.
    if (look.palette) set(R.palette, look.palette);
    set(R.paletteOverride, look.paletteOverride);
    if (tempo) publishBeat();
  });

  let lastLogged = '';
  client.on('status', (status) => {
    set(R.connection, status);
    const line = `${status.status}${status.via ? ` via ${status.via}` : ''}${status.error ? `: ${status.error}` : ''}`;
    if (line === lastLogged) return;
    lastLogged = line;
    if (status.status === 'error') log.warn(`lightshow ${line}`);
    else log.info(`lightshow ${line}`);
  });

  const refresh = setInterval(() => {
    if (client.status.status === 'connected') publishBeat();
  }, BEAT_REFRESH_MS);

  if (tokenError) {
    set(R.connection, { ...client.status, status: 'error', error: tokenError });
    log.warn(`lightshow not started: ${tokenError}`);
  } else {
    client.start();
  }

  // ── Commands: dashboard, Companion, other bundles ─────────────────────────
  function command(target, cmd) {
    const result = runCommand(R.controls.value, target, cmd);
    if (!result.ok) return result;
    set(R.controls, result.controls);
    return { ok: true, controls: result.controls };
  }

  nodecg.listenFor('cmd', (message, ack) => {
    const result = command(message && message.target, message && message.cmd);
    if (typeof ack === 'function' && !ack.handled) ack(null, result);
  });

  const publicState = () => ({
    controls: R.controls.value,
    connection: R.connection.value,
    bpm: R.bpm.value,
    clockSource: R.clockSource.value,
    palette: R.palette.value,
    paletteOverride: R.paletteOverride.value,
  });

  const router = nodecg.Router();
  if (nodecg.util && typeof nodecg.util.authCheck === 'function') router.use(nodecg.util.authCheck);
  router.use(createHttpHandler({ run: command, state: publicState, allowHosts: config.allowHosts || [] }));
  nodecg.mount(`/bundles/${BUNDLE}`, router);

  nodecg.on('serverStopping', () => {
    clearInterval(refresh);
    client.stop();
  });

  // For other bundles' extensions: nodecg.extensions['party-visuals'].
  return { command, state: publicState };
};

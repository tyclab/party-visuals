/**
 * The dashboard panel: the link to the lightshow, its tempo and palette, and
 * each graphic's switch and intensity. Switches go to the extension as `cmd`
 * messages, the same commands Companion sends, and come back through the
 * `controls` Replicant, so every open dashboard and every graphic agree.
 */
(function () {
  'use strict';
  const PV = window.PartyVisuals;
  const $ = (id) => document.getElementById(id);
  const reps = {};
  for (const name of ['connection', 'bpm', 'clockSource', 'beatPos', 'palette', 'paletteOverride', 'controls', 'audio', 'audioMode']) {
    reps[name] = nodecg.Replicant(name);
  }

  const SOURCES = { auto: 'Auto show', cdj: 'CDJ', track: 'Track', live: 'Live input', tap: 'Tap' };

  // "Last update" counts up between updates, so it is redrawn every second.
  function ago(ms) {
    if (!ms) return 'never';
    const s = Math.max(0, Math.round((Date.now() - ms) / 1000));
    if (s < 60) return `${s} s ago`;
    const m = Math.round(s / 60);
    return m < 60 ? `${m} min ago` : `${Math.round(m / 60)} h ago`;
  }

  function showConnection() {
    const c = reps.connection.value || {};
    $('dot').dataset.status = c.status || 'connecting';
    const words = {
      connected: `Connected${c.via === 'http' ? ' (polling GET /api/state)' : ''}`,
      reconnecting: `Reconnecting${c.retryInMs ? ` in ${(c.retryInMs / 1000).toFixed(1)} s` : '…'}`,
      connecting: 'Connecting…',
      error: 'Error',
      stopped: 'Stopped',
    };
    $('status').textContent = words[c.status] || c.status || '…';
    const parts = [];
    if (c.target) parts.push(c.target);
    parts.push(`last update ${ago(c.lastUpdate)}`);
    if (c.status !== 'connected') parts.push('graphics calm');
    if (c.error) parts.push(c.error);
    $('detail').textContent = parts.join(' · ');
    const audio = reps.audio.value;
    const audioAge = audio ? Date.now() - audio.at : Infinity;
    $('audioResponse').textContent = c.status === 'connected' && reps.audioMode.value === 'reactive' && audioAge >= 0 && audioAge < 600
      ? 'Shared audio: following the lights’ hits and energy'
      : 'Tempo and colours only · enable Reactive audio in ArtNet for shared music response';
  }

  function showTempo() {
    const bpm = reps.bpm.value;
    $('bpm').textContent = Number.isFinite(bpm) ? (Math.round(bpm * 10) / 10).toString() : '—';
    const src = reps.clockSource.value;
    $('source').textContent = src ? (SOURCES[src] || src) : '';
    const beat = reps.beatPos.value;
    $('phase').textContent = beat && beat.at > 0
      ? (beat.locked ? 'Beat position from the lightshow' : 'Free-running at the lightshow\'s tempo (it sends no beat position)')
      : '';
  }

  function swatches(el, colours) {
    el.replaceChildren(...colours.map((hex) => {
      const s = document.createElement('span');
      s.className = 'swatch';
      s.style.background = hex;
      s.title = hex;
      return s;
    }));
  }

  function showPalette() {
    const { colours, source } = PV.effectivePalette({ override: reps.paletteOverride.value, palette: reps.palette.value });
    swatches($('swatches'), colours);
    $('paletteSource').textContent = {
      override: 'The lightshow\'s palette override',
      look: 'The lightshow\'s look',
      default: 'Default colours (nothing from the lightshow yet)',
    }[source];
    const look = PV.paletteFrom(reps.palette.value);
    $('lookSwatches').hidden = !(source === 'override' && look);
    if (source === 'override' && look) swatches($('lookSwatches'), look);
  }

  // A slider being dragged is not moved by the Replicant under the pointer.
  const dragging = new Set();

  function showControls() {
    const controls = reps.controls.value;
    if (!controls) return;
    for (const section of document.querySelectorAll('.graphic')) {
      const target = section.dataset.target;
      const g = controls[target];
      const button = section.querySelector('.air');
      button.textContent = g.on ? 'On air' : 'Off';
      button.classList.toggle('on', g.on);
      button.setAttribute('aria-pressed', String(g.on));
      const range = section.querySelector('input');
      if (!dragging.has(target)) range.value = g.intensity;
      section.querySelector('output').textContent = `${g.intensity} %`;
    }
  }

  // The answer comes back through the controls Replicant; only a refusal
  // is worth a line in NodeCG's log.
  function send(target, cmd) {
    nodecg.sendMessage('cmd', { target, cmd }).then((result) => {
      if (result && !result.ok) nodecg.log.warn(result.error);
    }, (err) => nodecg.log.warn(err && err.message));
  }

  for (const section of document.querySelectorAll('.graphic')) {
    const target = section.dataset.target;
    section.querySelector('.air').addEventListener('click', () => send(target, 'air.toggle'));
    const range = section.querySelector('input');
    let pending = null;
    range.addEventListener('pointerdown', () => dragging.add(target));
    range.addEventListener('pointerup', () => dragging.delete(target));
    // At most ten sends a second while dragging; the last value always goes.
    range.addEventListener('input', () => {
      if (pending) return;
      pending = setTimeout(() => {
        pending = null;
        send(target, `intensity.${range.value}`);
      }, 100);
    });
    range.addEventListener('change', () => {
      dragging.delete(target);
      send(target, `intensity.${range.value}`);
    });
  }
  $('allOff').addEventListener('click', () => send('all', 'air.off'));

  // Built from the address this dashboard was opened at, which is the one
  // OBS and Companion on the same machine use too.
  function showUrls() {
    const base = `${location.origin}/bundles/${nodecg.bundleName}`;
    const lines = [
      ['OBS, colour wash (under the cards)', `${base}/graphics/wash.html`],
      ['OBS, beat bar', `${base}/graphics/bar.html`],
      ['Companion, wash on / off / toggle', `${base}/api/cmd/wash/air.on · air.off · air.toggle`],
      ['Companion, bar on / off / toggle', `${base}/api/cmd/bar/air.on · air.off · air.toggle`],
      ['Companion, intensity', `${base}/api/cmd/wash/intensity.60 · intensity.+10 · intensity.-10`],
      ['Companion, everything off', `${base}/api/cmd/all/air.off`],
    ];
    $('urls').replaceChildren(...lines.map(([label, url]) => {
      const li = document.createElement('li');
      const b = document.createElement('b');
      b.textContent = label;
      const code = document.createElement('code');
      code.textContent = url;
      li.append(b, code);
      return li;
    }));
  }

  showUrls();
  // Each view reads several Replicants, so none is drawn before all have arrived.
  NodeCG.waitForReplicants(...Object.values(reps)).then(() => {
    reps.connection.on('change', showConnection);
    reps.bpm.on('change', showTempo);
    reps.clockSource.on('change', showTempo);
    reps.beatPos.on('change', showTempo);
    reps.palette.on('change', showPalette);
    reps.paletteOverride.on('change', showPalette);
    reps.controls.on('change', showControls);
    setInterval(showConnection, 1000);
  });
})();

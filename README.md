# party-visuals

Party graphics for OBS that keep time with the lightshow
([artnet-lightshow](https://github.com/tyclab/artnet-lightshow)). A
[NodeCG](https://www.nodecg.dev) 2 bundle that runs in the same NodeCG as
[EclipseGraphics](https://github.com/LightD31/EclipseGraphics): EclipseGraphics draws the
cards, lower thirds and tickers, this bundle draws what moves with the music behind and
around them.

- **Colour wash** (`graphics/wash.html`): the whole frame in the lightshow's palette, to
  sit under EclipseGraphics' cards. The colours turn and flow with the beat; each beat
  lifts the wash a little and swells a glow in the middle.
- **Beat bar** (`graphics/bar.html`): a band of the palette's colours across the bottom of
  the frame (`?position=top` for the top), scrolling half a block a beat and swelling on
  each beat.

Both are 1920 × 1080 with a transparent background, take the lightshow's palette (its
palette override when it has one, else the look's colours), and:

- **never pulse faster than 5 Hz**, the photosensitivity limit the lightshow holds its own
  strobe to: up to 300 bpm a pulse a beat, above that every second or fourth beat, and at
  least 200 ms between two pulses whatever the beat position does;
- **draw at most `graphics.maxFps` frames a second** (30 by default);
- **go calm when the lightshow is gone**: no new pulse, the glow fades within a second,
  the motion stops where it was and the last colours stay. Nothing strobes while the link
  is down, and the beat eases back in when it returns.

The beat comes from the lightshow, not from a tap here. Between the lightshow's readings
the extension and every graphic extrapolate the beat at the last tempo, so the pictures
move smoothly at the graphics' own frame rate.

## Install beside EclipseGraphics

NodeCG 2 (tested with 2.8.0) and Node 22 or later, as for EclipseGraphics. The bundle
has one dependency of its own, `socket.io-client`.

**With EclipseGraphics' own `npm start`** (NodeCG installed as EclipseGraphics'
dependency, which loads the bundles in its `bundles/` folder too):

```
cd EclipseGraphics
git clone https://github.com/tyclab/party-visuals bundles/party-visuals
cd bundles/party-visuals && npm install --omit=dev && cd ../..
echo bundles/ >> .git/info/exclude
```

Then write the configuration below into EclipseGraphics' `cfg/` folder and `npm start` as
before.

**In a NodeCG installation with a `bundles/` folder**: clone into
`bundles/party-visuals`, run `npm install --omit=dev` there, and put the configuration
in that installation's `cfg/` folder.

The dashboard (<http://127.0.0.1:9090/>) gets a **Party visuals** panel next to
EclipseGraphics' panels.

## Configuration

`cfg/party-visuals.json` (all optional; [`configschema.json`](configschema.json)
describes it):

```json
{
  "lightshow": {
    "url": "http://tychome:3000",
    "tokenFile": "cfg/party-visuals.token"
  }
}
```

and `cfg/party-visuals.token` holding the lightshow's access token (its
**Settings → Server & access**) and nothing else.

**The token goes in a file, never in `cfg/party-visuals.json`**: NodeCG hands the bundle's
configuration to every page of the bundle, the OBS sources included, so anything in it can
be read by whatever can open a graphic. Only the extension reads the token file. A
`token` key in the configuration stops NodeCG at startup with a schema error; an address
with a token or a password in it is refused too (the connection shows the error and
nothing is sent).

| Option | |
|---|---|
| `lightshow.url` | The lightshow server, `http(s)://host:port` (default `http://127.0.0.1:3000`). |
| `lightshow.tokenFile` | The token file; a relative path starts at the folder NodeCG runs from. Empty: a lightshow with no token (bound to loopback). |
| `lightshow.pollMs` | How often `GET /api/state` is polled while the socket is down (default 1000). |
| `lightshow.reconnect.minMs` · `maxMs` | The wait before reconnecting: doubling from 500 ms up to 15 s, a little shorter at random. |
| `graphics.maxFps` | The most frames a second a graphic draws (default 30). |
| `graphics.offsetMs` | Shift the graphics' beat, −1000 to 1000 ms: positive draws it earlier, to make up for a projector's or OBS's delay. Judge it on the real screen. |
| `allowHosts` | Host names the `api/` addresses answer to besides IP addresses, `localhost` and `*.local` (Companion calling this machine by name). |

### The network

On the show PC, NodeCG reaches the lightshow over the network (`tychome:3000` in the
example above). The lightshow host keeps port 3000 to loopback and its single-sign-on
front door, so this connection needs a route: either the port opened to the show PC's
address, or a path through the front door that accepts the lightshow token on its own
(an extension cannot answer a browser login). Which one is a deployment decision, made and
recorded with the deployment, not in this repository. The lightshow also refuses host
names it does not know: reaching it by a name other than its machine name needs that name
as its **Public URL** (Settings → Server & access).

## OBS

One browser source per graphic, 1920 × 1080:

| Source | URL |
|---|---|
| Colour wash, under EclipseGraphics' overlay | `http://127.0.0.1:9090/bundles/party-visuals/graphics/wash.html` |
| Beat bar, over it | `http://127.0.0.1:9090/bundles/party-visuals/graphics/bar.html` (`?position=top` for the top) |

The state lives in the extension, so a source refreshed or opened mid-show comes back as
it was. With NodeCG's login on, add the user's `?key=…` (NodeCG's Graphics tab gives the
address with it).

## Companion

Bitfocus Companion's **Generic HTTP** module, a GET request, as for EclipseGraphics:
`http://127.0.0.1:9090/bundles/party-visuals/api/cmd/<target>/<command>`.

| Target | Command | |
|---|---|---|
| `wash` · `bar` · `all` | `air.on` · `air.off` · `air.toggle` | On, off (both fade over 0.4 s). `all/air.off` takes both off. |
| | `intensity.<0-100>` · `intensity.+<n>` · `intensity.-<n>` | Brightness in percent, or a nudge. |

For example `…/api/cmd/wash/air.toggle`, `…/api/cmd/bar/intensity.60`,
`…/api/cmd/all/air.off`. A command answers `{"ok": true, "controls": …}`, or HTTP 400 with
`{"ok": false, "error": …}`. Also:

- `POST …/api/cmd` with `{"target": "…", "cmd": "…"}`;
- `GET …/api/state`: the switches, the connection, the tempo and the palette;
- from another bundle: `nodecg.sendMessageToBundle('cmd', 'party-visuals', {target, cmd})`,
  or from an extension `nodecg.extensions['party-visuals'].command(target, cmd)`.

Commands are refused (HTTP 403) when a browser says they come from another site, and
`api/` answers only to IP addresses, `localhost`, `*.local` and the `allowHosts` names.
With NodeCG's login on, add the user's `?key=…`.

## Replicants

Written by the extension; graphics and panels read them.

| Replicant | |
|---|---|
| `bpm` | The lightshow's tempo (its clock's, else the typed one); null before it has sent one. |
| `beatPos` | `{beat, bpm, at, epoch, locked}`: the beat position at time `at` (ms since 1970), re-published twice a second while connected; graphics extrapolate from it. `locked` says whether the phase came from the lightshow or is the extension's own. |
| `clockSource` | What the lightshow's clock follows: `auto`, `cdj`, `track`, `live` or `tap`. |
| `palette` | The look's colours as `#RRGGBB`, white, amber and UV mixed in as the lightshow's own swatches show them. Kept across restarts. |
| `paletteOverride` | The lightshow's palette override as `#RRGGBB`, or null (also when the lightshow does not send one). |
| `connection` | `{status, via, since, lastUpdate, error, retryInMs, target}`; `status` is `connecting`, `connected`, `reconnecting`, `error` or `stopped` (NodeCG shutting down), `via` is `socket` or `http`. Never the token. |
| `controls` | `{wash: {on, intensity}, bar: {on, intensity}}`. Kept across restarts. |

Each has its JSON schema in `schemas/`.

## How it follows the lightshow

The extension is a read-only client of the lightshow server. It connects over Socket.IO
asking for protocol 2 (`auth: {token, protocol: 2}`), takes the snapshot, then applies
the patches, each domain's version in order; a missed patch makes it ask for the whole
state again (`sync`). It reads `bpm`, `clock` (`source`, `bpm`, and `beatPos` and `epoch`
when the lightshow sends them), the look's four colour slots through the colour-preset
catalogue, and `paletteOverride` when there is one. It sends nothing else.

While the socket is down it polls `GET /api/state` (the token in the `X-Lightshow-Token`
header), so the tempo and colours get through a proxy that will not carry the socket.
Reconnecting is its own, with the backoff above, and goes on after a refused token, so a
token fixed on the lightshow's side is picked up without restarting NodeCG. A refused
poll (401: the token; 403: a host name the lightshow does not answer to) is not repeated
every second: the next socket attempt polls once more if it fails.

A lightshow that sends no beat position (the clock's `{source, bpm}` alone) still drives
the graphics at its tempo; the phase is then the extension's own, shared by every graphic.

## Development

```
npm install
npm test
```

The tests run against a local mock of the lightshow's Socket.IO server; nothing leaves the
machine. `shared/` is loaded both by the extension and, as plain scripts, by the graphics.

## Licence

MIT, see [LICENSE](LICENSE). NodeCG and Socket.IO are MIT-licensed.

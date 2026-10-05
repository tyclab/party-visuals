# Companion page: lights and visuals

One Bitfocus Companion page with the lightshow's buttons on the left and the party
visuals' on the right, for one Stream Deck that runs both.

There is no import file. Companion's page export (`.companionconfig`) is an internal
format with no published specification: it carries a format version that changes
between releases and the ids of the connections on the machine that exported it. The
page below takes a few minutes to build by hand.

## Connections

Companion runs on the show PC: NodeCG listens on `127.0.0.1` only, so a Companion on
another machine cannot reach it.

| Connection | Module | Settings |
|---|---|---|
| `lightshow` | **ArtNet Lightshow** (the lightshow's own module, `companion-module/` in [artnet-lightshow](https://github.com/tyclab/artnet-lightshow)) | Host and port of the lightshow, its access token |
| `visuals` | **Generic HTTP Requests** (`generic-http`, ships with Companion) | Base URL: `http://127.0.0.1:9090/bundles/party-visuals/api/cmd/` |

With that base URL every visuals button is a **GET** action whose URI is
`<target>/<command>`, e.g. `wash/air.toggle`. A refused command (HTTP 400) turns the
`visuals` connection red; its log has only the response code, and the same address
opened in a browser shows the reason (`{"ok": false, "error": …}`). The buttons send
and do not light up with the state; the **Party visuals** panel in NodeCG's dashboard
(<http://127.0.0.1:9090/>) shows what is on.

## Page

8 × 4, Companion's default page size (a Stream Deck XL). Columns 1–4 are the
lightshow, columns 5–8 the visuals. The lightshow's buttons are its module's presets,
dragged from Presets → ArtNet Lightshow → **Busk**.

| | 1 | 2 | 3 | 4 | 5 | 6 | 7 | 8 |
|---|---|---|---|---|---|---|---|---|
| **1** | Tap tempo | BPM (display) | BPM −5 | BPM +5 | Wash on/off | Wash −10 | Wash +10 | Wash 50 % |
| **2** | Palette | Palette | Palette | Palette | Bar on/off | Bar −10 | Bar +10 | Bar 80 % |
| **3** | Hold: white strobe | Hold: blinder | Hold: UV wash | Hold: kill | Wash on | Wash off | Bar on | Bar off |
| **4** | Auto show | Master −10 % | Master +10 % | Blackout | Visuals off | Visuals on | All dark | All back |

### Lightshow (columns 1–4)

Presets of the ArtNet Lightshow module, from its **Busk** category:

| Button | Preset (Busk group) | What it does |
|---|---|---|
| Tap tempo | Tap Tempo (Tempo) | A tap. |
| BPM (display) | BPM Display (Tempo) | The tempo. |
| BPM −5 · +5 | BPM -5 · BPM +5 (Tempo) | Adjust BPM by 5. |
| Palette ×4 | one palette each (Palettes) | The look's palette; lit while it is the look's. |
| Hold: … | the Hold presets for white strobe, blinder, UV wash and kill (Hold, on while pressed) | Energy Hold: on at the button's down, off at its up. The lightshow lets go a second after it last heard from Companion. |
| Auto show | Auto Show (Auto show) | Start or stop the auto show. |
| Master −10 % · +10 % | Master −10% · Master +10% (Master) | Adjust Master Dimmer. |
| Blackout | Master Blackout (Master) | Master Blackout, toggle. |

### Visuals (columns 5–8)

GET actions on the `visuals` connection. The full address is the base URL followed by
the URI.

| Button | URI | |
|---|---|---|
| Wash on/off | `wash/air.toggle` | The colour wash on or off; it fades over 0.4 s. |
| Wash −10 · +10 | `wash/intensity.-10` · `wash/intensity.+10` | Brightness, in steps of 10 %. |
| Wash 50 % | `wash/intensity.50` | Back to the default brightness. |
| Bar on/off | `bar/air.toggle` | The beat bar on or off. |
| Bar −10 · +10 | `bar/intensity.-10` · `bar/intensity.+10` | |
| Bar 80 % | `bar/intensity.80` | Back to the default brightness. |
| Wash on · off | `wash/air.on` · `wash/air.off` | Explicit, for a cue that must land the same way every time. |
| Bar on · off | `bar/air.on` · `bar/air.off` | |
| Visuals off | `all/air.off` | Both graphics off. |
| Visuals on | `all/air.on` | Both graphics on. |

### Lights and visuals together (row 4, columns 7 and 8)

A button can carry actions of both connections:

| Button | Actions |
|---|---|
| All dark | ArtNet Lightshow: Master Blackout, on · `visuals`: GET `all/air.off` |
| All back | ArtNet Lightshow: Master Blackout, off · `visuals`: GET `all/air.on` |

## The commands

The bundle's whole Companion vocabulary (see the README's **Companion** section):

| Target | Command |
|---|---|
| `wash` · `bar` · `all` | `air.on` · `air.off` · `air.toggle` |
| | `intensity.<0-100>` · `intensity.+<n>` · `intensity.-<n>` |

EclipseGraphics' commands go on the same kind of button with its own base,
`http://127.0.0.1:9090/bundles/EclipseGraphics/api/cmd/` (its README lists them). With
NodeCG's login on, every address needs the user's `?key=…`.

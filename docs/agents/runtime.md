# NodeCG and visualizer troubleshooting

Read for this topic; paths below are relative to the repository root unless stated otherwise.

- After updating a bundle, stop/start PartyVisuals NodeCG so it loads the new code.
  If the engine token changed, update the installer token and related controller
  connection. Tokens stay in the extension's private storage.
- Music Assistant 2.10.7 drives its visualizer from queue buffers. Connect traffic
  and audio that bypass those queues can play while the visualization stays black.
  Verify the current version and playback path before diagnosing rendering.
- Updating a show PC to the live-audio page goes engine, capture helper, bundle,
  configuration, in that order (README, Updating a show PC): an older helper refuses
  the page's address and an older engine sends no samples. Lightshow has no releases;
  name the commit (`91d0b405` or later).
- The live-audio page (`graphics/milkdrop.html`) draws from the engine's live input
  instead. A calm page names its wait on screen and in `data-party-wait` (also the
  helper's log): check that before the capture path. Its timing bound, idle rule and
  dependency pins are in the README; keep `shared/samples.js`, `test/helpers/mock-relay.js`
  and the engine's `samples` topic (lightshow `src/server/sample-relay.ts`, the commit the
  mock names) in step.
- A covered Chrome window can stop painting; bring the capture window to the
  foreground. The helper intentionally covers the visualizer while NodeCG is
  unreachable, so check NodeCG first when music plays behind a black screen.
- The protocol client is read-only. Preserve version-gap recovery, HTTP fallback,
  reconnect backoff and clock continuity; tests use a local mock engine.
- Installer changes must pass the Windows PowerShell 5.1 path:
  `powershell -NoProfile -ExecutionPolicy Bypass -File test\install.test.ps1`.
- A controller that is not running: read the end of `controller.log` first. It
  holds `controller did not start: <error>`, `controller stopped: <error>` or the Go runtime's crash report
  ([Keeping the controller running](../../controller/README.md#keeping-the-controller-running)).
- `test/controller-install.test.ps1` loads the ScheduledTasks module first and
  replaces each of its commands that changes a task with one that throws, so a
  run on a show PC cannot reach the installed task. Run with `-File`, a script's
  functions are global, and a module that loads later replaces them: keep that
  order when adding a stub.
- The native controller uses `controller/` and the run-once session worker;
  never wrap the legacy restart loops with a second supervisor. Run its Go
  checks, `test/controller-install.test.ps1` and `test/release-install.test.ps1`. Native dummy-process tests
  do not replace actual Chrome/power acceptance. Installation stays staged
  until the deployment interlock permits the owned launcher handoff.

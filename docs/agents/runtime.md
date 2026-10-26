# NodeCG and visualizer troubleshooting

Read for this topic; paths below are relative to the repository root unless stated otherwise.

- After updating a bundle, stop/start PartyVisuals NodeCG so it loads the new code.
  If the engine token changed, update the installer token and related controller
  connection. Tokens stay in the extension's private storage.
- Music Assistant 2.10.7 drives its visualizer from queue buffers. Connect traffic
  and audio that bypass those queues can play while the visualization stays black.
  Verify the current version and playback path before diagnosing rendering.
- A covered Chrome window can stop painting; bring the capture window to the
  foreground. The helper intentionally covers the visualizer while NodeCG is
  unreachable, so check NodeCG first when music plays behind a black screen.
- The protocol client is read-only. Preserve version-gap recovery, HTTP fallback,
  reconnect backoff and clock continuity; tests use a local mock engine.
- Installer changes must pass the Windows PowerShell 5.1 path:
  `powershell -NoProfile -ExecutionPolicy Bypass -File test\install.test.ps1`.

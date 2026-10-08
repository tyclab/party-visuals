# Repository instructions

This NodeCG 2 bundle follows ArtNet Lightshow and renders beat/colour graphics
for OBS. README.md documents installation, configuration and integration.

- README files and agent instruction files are exempt from the comment limit
  by design. The limit applies to code and configuration files.
- Outside the limit (operator, 2026-10-08): vendored third-party code and
  third-party build output (kept byte-identical to upstream), Hugo site
  functional files, translation files, and approved runtime text such as MCP
  tool docstrings. Output of our own generators is inside it: fix the generator.
- Keep comment-only lines at or below 20% of nonblank lines per code/config file.
  Exceptions require explicit operator approval recording path, reason and scope;
  none are granted here. Preserve legal notices, tool directives and runtime help
  while requesting an exception. Never mistake embedded strings for comments.
- Keep concise constraints and rationale; remove redundant prose and unused files
  only when callers, packaging and tests prove they are unnecessary. Add or remove
  features only within the requested scope and with regression evidence.
- Keep repository instructions only in AGENTS.md. Update READMEs and their process
  diagram when behavior changes. Inspect fetched incoming branches before edits
  and preserve other agents' work.
- The extension is a read-only lightshow client. Keep protocol version-gap recovery,
  HTTP fallback, reconnect backoff and clock continuity intact.
- Read the access token in the extension only. Never put it in bundle configuration,
  Replicants, graphics, logs or command lines. Preserve HTTP origin/host checks.
- Preserve the five-pulse-per-second cap, minimum pulse spacing and calm disconnected
  state. `shared/` must work in both CommonJS and plain browser scripts.
- Run `npm ci` and `npm test`; tests use a local mock lightshow. On Windows also run
  `powershell -NoProfile -ExecutionPolicy Bypass -File test\install.test.ps1` for
  installer changes. Keep the installer compatible with Windows PowerShell 5.1.

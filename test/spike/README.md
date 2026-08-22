# Spike 1 harness — native messaging

The experiments behind [SPIKE.md](../../SPIKE.md) §1. **Deliberately not part of `npm test`**:
each run launches a real Chrome and the lifetime experiment takes five and a half minutes by
design. It exists so the findings can be re-derived rather than trusted, when Chrome changes
or the project moves to another OS.

```bash
node test/spike/run.mjs lifetime    # does an open native port keep the MV3 worker alive?
node test/spike/run.mjs control     # the same extension without a port — the control
node test/spike/run.mjs offscreen   # is connectNative reachable from an offscreen document?
node test/spike/run.mjs bench       # native messaging vs the current WebSocket path
node test/spike/run.mjs dock        # a shebang host against a Chrome with the GUI environment
node test/spike/run.mjs dock-fixed  # the same, with the interpreter path baked into a launcher
node test/spike/run.mjs all         # all six, ~8 minutes
```

Everything runs against a throwaway `--user-data-dir` under this directory, so the real Chrome
profile — including its `NativeMessagingHosts` — is never touched. Extensions, keys, profiles
and logs are all generated on first run and git-ignored.

## What each run proves

| Run | Reads as a pass when |
| --- | --- |
| `lifetime` | the worker is never evicted, the host keeps one pid, and `STDIN CLOSED` appears only after Chrome is killed |
| `control` | the worker **is** evicted at ~31 s — otherwise the harness itself is keeping workers alive and the lifetime result means nothing |
| `offscreen` | the host log states plainly whether `connectNative` exists in an offscreen document |
| `bench` | both transports report MB/s for the same 300 × 300 KB payload, plus the sender-side cost inside the service worker |
| `dock` | the host is **never spawned** — that is the finding, not a broken harness |
| `dock-fixed` | the host spawns and reports `PATH=/usr/bin:/bin:/usr/sbin:/sbin` |

`control` is not optional. A debugger attached to a service worker keeps it alive, so a
lifetime result without a control cannot distinguish "the port held the worker" from "the
measurement held the worker".

## Two things that will bite a future reader

- **`--load-extension` is ignored as of Chrome 151**, silently, with nothing in the log, and
  `--disable-features=DisableLoadExtensionCommandLineSwitch` no longer revives it. The harness
  loads extensions over CDP with `Extensions.loadUnpacked`.
- **stdout is the protocol.** A native host that prints anything to stdout corrupts the frame
  stream, and the failure looks like a parse error far away. The hosts here write only framed
  messages; everything else goes to a log file.
- **A Chrome started from a shell lies about the host's environment.** It hands the host the
  shell's PATH, so an nvm or homebrew interpreter resolves and the host starts — while the
  same host, registered the same way, never starts under the Dock. Any question about
  spawning, interpreters or FFmpeg lookup must be asked through `dock`, never through the
  other runs.

# Harness verification

intern has two genuinely different kinds of harness-facing surface, and they
carry different verification claims. Read both halves before trusting either.

## The CLI (register, send, inbox, wait, ls, claim, ...)

This is one plain subprocess call per command: stdin/stdout/exit code, no
harness-specific integration point. It behaves identically no matter which
agent or harness invokes it, because from intern's side there is no way to
tell them apart — it is just a process with a pid talking to a unix socket.
Nothing here needs, or gets, per-harness verification: verifying it once
(the test suite, `intern demo`) verifies it for every harness.

## Hooks (`intern hooks install`, Claude Code only)

Hooks are different: they mean writing into a specific harness's own
configuration format and trusting that harness to invoke intern at the
right lifecycle point. That integration is real, harness-specific surface,
and each harness's status below is reported honestly rather than assumed.

| Harness | Mechanism | Status | Verified as of |
| --- | --- | --- | --- |
| Claude Code | Stop / SessionStart hooks in `settings.json` | Verified at the design/contract level against Claude Code's own published hooks documentation (hook shapes, exit-code semantics, `settings.json` merge behavior). **Not** verified against a live Claude Code session — no such session was available in this environment. | 2026-07-31 |
| Other harnesses | (none implemented) | Unverified. No hook mechanism has been investigated or built for other harnesses. | — |
| OpenCode | (none implemented) | Unverified. No hook mechanism has been investigated or built for OpenCode. | — |
| Aider, Gemini CLI, Cline, and everything else | (none implemented) | Not attempted. These harnesses use the CLI-only path (`intern wait`, polling or blocking), which needs no hook and no per-harness verification — see above. | — |

If you install Claude Code hooks and hit a mismatch with this table, that is
useful signal: please open an issue with what you saw instead of assuming
the design-level verification above extended further than it did.

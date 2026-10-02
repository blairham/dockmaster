<!--
Explain the why. The diff already says what.
A new action that changes daemon state goes through App.handleAction and the
`mutating` map, never straight from a view — that is what keeps --readonly
honest (AGENTS.md, "Views never mutate").
-->

## What this changes



## Why



## How it was verified

<!--
Tests drive the app headlessly (internal/tui/app_test.go). A new key needs a
test that presses it and checks the frame or the action; a new mutation needs
a readonly test proving it is refused. Never run a test that starts, stops or
deletes a real container or VM.
-->



---

- [ ] I have signed the CLA (see CLA.md)

Closes #

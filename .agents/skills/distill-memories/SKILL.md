---
name: distill-memories
description: Promote durable journal and LTM knowledge into canonical project documentation.
user-invocable: true
allowed-tools: Bash, Read, Write, Edit, Grep, Glob
---

# Distill Memories

Read `.agents/docs/JOURNAL.md`, `.agents/docs/LTM/INDEX.md`, and only the relevant LTM documents. Promote stable knowledge into the appropriate canonical destination:

- `.agents/docs/OVERVIEW.md` for concise coding-agent orientation and subsystem boundaries.
- `.agents/docs/TESTING.md` for how to build, harvest, and grade against the engine.
- `README.md` or `docs/` for user-visible capabilities and workflows, when either exists.

Verify implementation facts against the current code before documenting them. Synthesize rather than copying historical prose. Prefer current invariants and behavior over incident timelines, omit narrow patch history, and merge into existing sections when possible.

Re-read every edited section and run the appropriate documentation or code checks. If sources disagree with each other or with the code, expose the ambiguity rather than silently choosing one version.

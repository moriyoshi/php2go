---
name: tackle-todos
description: Audit TODO.md and source TODO/FIXME comments, prioritize verified items, and resolve approved independent work units.
user-invocable: true
allowed-tools: Bash, Read, Write, Edit, Grep, Glob, Agent
---

# Tackle TODOs

Read unchecked items in `.agents/docs/TODO.md` and scan source files for TODO/FIXME comments. Deduplicate and classify them as systematic, behavioral, validation, serialization, test-only, or design work.

Re-verify every historical item against the current tree before acting. Store the working inventory in `.agents-workspace/tmp/consolidated-todos.md`. Apply any user-supplied package or keyword filter.

Group applicable items into independent work units with non-overlapping files. Present the plan and obtain confirmation before dispatching parallel agents. Do not attempt design-category work without explicit approval.

Each work unit must add appropriate regression coverage and run focused tests while iterating. Before completion run the repository quality gate from `.agents/docs/QUALITY_GATE.md`. Mark successfully resolved TODO entries, remove obsolete source comments, annotate blocked attempts, and append a concise outcome to the journal.

---
name: good-sleep
description: Reorganize the append-only development journal into topic-oriented long-term memory and extract unresolved work into TODO.md.
user-invocable: true
allowed-tools: Bash, Read, Write, Edit, Grep, Glob
---

# Good Sleep

Read all of `.agents/docs/JOURNAL.md`, identify durable knowledge by topic, and consolidate it under `.agents/docs/LTM/`. Group by subsystem or cross-cutting concern, never by date.

Before editing, present the proposed LTM filenames, their purpose, and the journal sections feeding each file, and wait for confirmation.

Extract pending tasks, follow-ups, TODOs, and FIXMEs into `.agents/docs/TODO.md`. Deduplicate them and include their journal source. Do not put unfinished work into LTM.

Each LTM document should contain, where applicable: a summary, key facts, details, relevant files, test coverage, and pitfalls. Synthesize overlapping entries instead of concatenating them. Preserve useful paths, commands, symbol names, invariants, and tables.

Refresh `.agents/docs/LTM/INDEX.md` with every LTM document and a one-line description. Do not delete or rewrite existing journal entries. Append one `## LTM Consolidation Record` mapping consolidated journal sections to LTM files. Subsequent runs should process only uncovered material and avoid duplicate records.

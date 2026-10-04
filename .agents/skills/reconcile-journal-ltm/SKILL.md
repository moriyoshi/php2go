---
name: reconcile-journal-ltm
description: Audit journal coverage in LTM and TODO, fill gaps through good-sleep, and normalize consolidation history.
user-invocable: true
allowed-tools: Bash, Read, Write, Edit, Grep, Glob
---

# Reconcile Journal and LTM

Read the journal, LTM index, TODO file, and the `good-sleep` skill. For each substantive journal entry, identify durable decisions, invariants, patterns, pitfalls, paths, test guidance, and unresolved work. Treat an entry as covered only when that information actually exists in LTM or TODO; record tables alone are not evidence.

For gaps, follow `good-sleep`: extend an existing topic when possible, propose meaningful new topics before writing, extract open work to TODO, and refresh the index. Repeat the audit until coverage is complete.

Then rewrite consolidation metadata into one canonical `## LTM Consolidation Record`. Deduplicate its section-to-LTM mappings and synthesis mappings. Remove superseded consolidation-record sections and substantive journal entries only when their durable content is demonstrably represented in the canonical mapping. This skill is the sole exception to the journal's append-only rule.

Validate that referenced files exist, only one consolidation record remains, and every removed entry is covered.

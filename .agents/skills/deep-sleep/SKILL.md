---
name: deep-sleep
description: Consolidate overlapping long-term memory documents into broader synthesis documents while preserving their sources.
user-invocable: true
allowed-tools: Bash, Read, Write, Edit, Grep, Glob
---

# Deep Sleep

Read `.agents/docs/LTM/INDEX.md`, then selectively inspect candidate LTM documents. Find overlapping subsystem notes, repeated pitfalls, or related implementation documents that would benefit from a durable synthesis.

Before editing, present proposed synthesis files, their source documents, and cohesive documents that should remain standalone. Wait for confirmation.

Write synthesis documents under `.agents/docs/LTM/` with: summary, included-document table, stable knowledge, operational guidance, files, tests, and pitfalls. Deduplicate and reconcile the sources; make contradictions explicit. Keep all source documents unless the user explicitly requests deletion.

Update the index so synthesis and source documents are distinguishable, and append a short consolidation note to the journal. Avoid consolidation when the existing set is already small and non-overlapping.

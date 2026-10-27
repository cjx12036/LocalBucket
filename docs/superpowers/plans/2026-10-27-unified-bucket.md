# Unified bucket Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox syntax for tracking.

**Goal:** Ship a unified online-file space with refined native layouts and concise bilingual docs.
**Architecture:** Extend loopback admin API with a fault-isolated root aggregation and mixed-owner prepare/save requests. Keep LAN protocol and existing dedicated storage intact. Native views hold owner ID with every entry, root imports target self, folders target their owner.
**Tech Stack:** Go, Swift/AppKit, Go/Win32/OLE.
**Spec:** docs/superpowers/specs/2026-10-27-unified-bucket-design.md (approved in chat).

## Global Constraints
- No replication, no source deletion on export, no remote deletion.
- Preserve existing data and discovery; no certificates or protection bypass.
- Root imports go local; owner folders stay distinct; stable peer+path identity.
- User authorized implementation, GitHub release, docs; continue without further workflow approvals.

## Review Focus
- Same basename from multiple owners must survive list and export.
- A timed-out peer must not hide healthy peers' files.
- Mixed-owner selection must route every action to its actual source.
- Refresh/navigation races must not change selection or drag content.
- Root import/internal drag and local-only deletion must preserve originals.

### Task 1: Shared API and regressions
Files: app/internal/node/space.go, space_test.go, http.go, service.go; app/API.md.
Interfaces: GET /app/space -> {files:[{name,path,is_dir,size,modified,peer,owner,local}],peers:[Peer],unavailable:[{peer,owner,error}]}. Root only. Existing /app/list unchanged.
POST /app/prepare and /app/save additionally accept items:[{peer,path}] (nonempty overrides legacy peer/paths); return one existing job. Mixed-owner prepare exports unique basenames in a common staging directory (auto number conflicts), does not merge distinct same-name directories. Save uses existing collision-safe export behavior. Both preserve originals and cancellation.
- [x] Add integration tests for aggregation owner collisions, failed peer, mixed-owner export, invalid/empty selection and cancellation; run red.
- [x] Implement bounded concurrent aggregation and mixed-owner jobs; run targeted then race suite green.

### Task 2: Mac unified layout
Files: app/macos/LocalBucket.swift only. Consume /app/space at root; enrich folder entries with selected owner; use items for multi-owner save. Resolve drag URLs/promises by each entry, preserve identities. Root imports local and internal root drag no-op; local-only delete. Main actions Add/Save, compact More menu and footer; location column and online count. Compile Swift and inspect actual Mac app.
- [x] Implement and compile; review selection/drag ownership and offline refresh.

### Task 3: Windows unified layout
Files: app/cmd/localbucket/*.go only. Same contracts/behaviors as Mac; one prepare job for mixed-owner drag with coherent cache fingerprint. Compact toolbar and more menu, legible file rows and ownership; DPI-aware resize, keyboard and existing OLE semantics. Crosscompile and native Windows CI startup.
- [x] Implement and crosscompile; review mixed-owner cache invalidation and root drop.

### Task 4: Documentation, package and release
Files: README.md, README.en.md, app/README.md, app/build.py, app/resources/winres.json, workflow, docs/validation-v0.2.md.
- [x] Concise Chinese default and English linked README; motivation, download/use, no sync, local delete, LAN permissions, SmartScreen and Mac Gatekeeper limitations.
- [x] Version 0.2.0; build both apps, tests/race/vet, packaging/signature checks; fresh review and fix important findings.
- [ ] Commit/push feature branch, update PR, run CI; publish v0.2.0-preview.1 assets and verify GitHub digests.

## Ledger
Ruling: Reuse current codex/localbucket-v0.1 feature branch (not main), with disjoint native UI file ownership. User requests GitHub publication and README update; after CI, promote reviewed PR to main so the bilingual README appears on the repository homepage.
Ruling: Implementation runs in this session; dispatch independent native platform tasks under dispatching-parallel-agents, root handles shared API/docs/integration.

Review: mixed-save temporary cache leak fixed with red/green regression. Mac folder online status now refreshes independently. Full 23-test race suite plus real Dufs/UDP discovery passes. Mac packaged root Add and local Trash tested with one synthetic fixture; actual new physical cross-device drag remains a device check.

CI: Windows and macOS success at https://github.com/cjx12036/LocalBucket/actions/runs/40457342739 for a9388f2. Publishing authorized by user; plan/documentation recording commit does not alter executable code.

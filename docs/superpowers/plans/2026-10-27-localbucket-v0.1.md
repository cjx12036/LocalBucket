# LocalBucket v0.1 Implementation Plan

> **For agentic workers:** Use superpowers:executing-plans for core/integration and dispatching-parallel-agents for the two independent native UI implementations. Steps use checkbox tracking.

**Goal:** Ship native Mac and Win10 apps with computer-first discovery, reliable copying, clear Chinese guidance and generated icons.
**Architecture:** Shared Go node owns persistent identity, safe file operations, discovery and Dufs. AppKit and Win32/OLE provide native UI and drag/drop. Admin API is loopback plus a secret; LAN API never exposes delete.
**Tech Stack:** Go standard library, zeroconf mDNS, Swift AppKit, Win32/OLE, Dufs 0.47.0, Python build tooling.
**Spec:** `docs/superpowers/specs/2026-10-27-localbucket-v0.1-design.md` (approved by user 2026-10-27).

## Global Constraints
- Mac M4/macOS 15.7.7, Win10 x64; no browser UI.
- TCP 47931, automatic mDNS and UDP discovery; computer names at first level.
- Dedicated empty bucket; remote browse/upload/download without password; delete only local.
- Copy-only export; identical import skips; changed names auto-number without prompting.
- Close window stops sharing and exits; no tray/autostart.
- Preserve prototype and user data. No paid signing or bypass of SmartScreen.

## Review Focus
- Path escape, symlinks/reparse points and remote admin access must be rejected.
- Reimport numbered duplicates, case collisions and simultaneous uploads must not lose data.
- Directory self-import must not recurse; canceled transfers must not appear complete.
- Discovery/port errors and shutdown must not leave an invisible sharing process.
- Slow remote preparation must not block native drag callbacks or UI.

### Task 1: Safe bucket engine
Files: `app/go.mod`, `app/internal/node/store.go`, `store_test.go`, platform trash helpers.
- [ ] Write failing tests for duplicate conflict groups, directory merge, self-import, traversal and concurrency.
- [ ] Observe failure, implement streaming hash/stage/commit and safe path resolution.
- [ ] Run `cd app && go test ./internal/node -run TestStore -race`; commit after green.
Interfaces: `Store.Import(ctx, dest, sources) (Result,error)`, `Store.Receive(ctx,path,reader) (Result,error)`, `Store.List(path) ([]File,error)`.

### Task 2: Node and discovery
Files: `app/internal/node/{service,http,discovery,jobs}.go`, `service_test.go`, `app/cmd/node/main.go`.
- [ ] Write failing HTTP tests for LAN write/read, remote deletion forbidden, token-gated local management, duplicate upload and interrupted upload cleanup.
- [ ] Implement shared contracts in `app/API.md`, managed Dufs, persistent identity, bounded jobs, mDNS/UDP peer expiry.
- [ ] Run Go tests/race tests and a real Dufs integration on isolated temporary ports; commit after green.

### Task 3: Native Mac UI
Files: `app/macos/LocalBucket.swift` only. Consumes `app/API.md`; no core edits.
- [ ] Build computer-first NSTableView UI, folder navigation, clear drop highlight, picker/save/delete, asynchronous jobs, source tracking, file promises, graceful close and first-run guide.
- [ ] Compile Swift and exercise Mac UI; commit after integration review.

### Task 4: Native Windows UI
Files: `app/cmd/localbucket/*_windows.go` only. Consumes `app/API.md` and `node.Start`.
- [ ] Reuse verified OLE copy drag, add native computer/file lists, async cache preparation, upload jobs, save/delete/help, resize and close handling.
- [ ] Cross-build Win64 with embedded Dufs/runtime/icon; runtime validation remains user's Win10 test if no Windows runner.

### Task 5: Build, review and delivery
Files: `app/build.py`, root README, app docs and release scripts.
- [ ] Produce PNG/ICO/ICNS from generated icon without changing its design. Build Mac App and Win EXE; optional explicit signing hook, checksums, license notices.
- [ ] Run full suite, platform compilation, Mac smoke and native UI checks, shared-service two-node integration and shutdown checks.
- [ ] Fresh whole-change review; fix material findings and rerun affected checks.
- [ ] Preserve clear SmartScreen/Windows-runtime limitations; publish an appropriately labeled prerelease through the existing authorized GitHub flow.

## Execution ledger
Ruling: User explicitly said start development after design approval; execute continuously without another plan approval prompt. Use current clean checkout on `codex/localbucket-v0.1` to preserve known build assets; no parallel checkout consumers. Native UIs own disjoint files and share only the fixed API contract; core integration remains with the primary agent.

# LocalBucket v0.1 validation

Date: 2026-10-27 (America/New_York).

## Executed

- Local M4/macOS 15.7.7: `LOCALBUCKET_TEST_DUFS=... LOCALBUCKET_TEST_DISCOVERY=1 go test -race ./internal/node -v` passed, including real Dufs and actual UDP sockets.
- Shared tests cover identical/numbered/case collisions, concurrent uploads, folder merge, traversal/link roots, delete-root protection, interrupted upload cleanup, filtered ZIP/empty folders, Dufs reserved paths, local management isolation, two-node transfer/preparation, port conflict, discovery and shutdown.
- `go test ./...` and `go vet ./internal/node ./cmd/node` passed locally.
- Native Swift UI and Mac trash helper compiled; Win64 GUI EXE compiled with generated icon/version/manifest resources.
- Packaged node startup handshake and stdin-close shutdown passed on synthetic data.
- Mac App exercised through UI: automatic computer-first home, opening own bucket, file list and guidance; synthetic same-content upload skipped; deleting only the synthetic fixture succeeded through native system Trash.
- Closing final Mac App shut TCP47931 and removed the owned node process.
- ZIP integrity, release SHA256 values, embedded Windows Dufs/VC runtime and Mac helpers/icon verified. Mac bundle passes `codesign --verify --deep --strict` (local ad-hoc signature).
- Read-only core and UI reviews completed; material findings fixed and scoped re-reviews completed.

## Native Windows CI

[Successful macOS and Windows workflow](https://github.com/cjx12036/LocalBucket/actions/runs/40454265788), code commit `8be5812594c1732d2228c3e7667e14b245237510`.

Windows Server 2022 runner ran the core tests, built the GUI EXE, launched it and verified its embedded node's `/v1/info` response. This is an actual Windows startup check, not only cross-compilation.

## Remaining device/release checks

- Win10 Explorer drag-in/out and two physical computers' automatic discovery/transfers need user regression on the new UI. The earlier native spike was user-validated, but that does not establish new-version runtime coverage.
- Computer-use coordinate dragging failed to locate the target window during this session; it is not counted as a successful new-UI drag test. Core dedup, native code review and the other UI/API checks are separate evidence.
- Remote Windows first drag prepares a cache; a subsequent drag exports it, or use save-to directly. Cached preparations are invalidated on metadata/list changes, consumed after a successful export, and have bounded expiry.
- Windows executable is unsigned because no trusted signing identity was supplied. SmartScreen clearance was not tested or achieved. The signing hook requires the user's own credentials/enrollment; no security protections were disabled.
- Mac public distribution notarization is not configured; local ad-hoc signing is not Developer ID signing.

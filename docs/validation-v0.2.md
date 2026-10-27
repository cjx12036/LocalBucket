# LocalBucket v0.2 validation

## Executed locally (Apple Silicon Mac)

- `LOCALBUCKET_TEST_DUFS=<vendor/dufs-macos> LOCALBUCKET_TEST_DISCOVERY=1 go test -race ./... -v -timeout 60s`: 23 tests passed (22 shared-node cases plus native selection helper case).
- Existing rules: hash/numbered-copy dedup, collision concurrency, path/link/root rejection, atomic canceled uploads, two-node folder transfer, archive link filtering/empty folders/reserved Dufs route, LAN/admin permission boundary, discovery, startup conflict and shutdown.
- New real HTTP integration cases: unified list keeps distinct owner IDs for identical names; a stalled peer does not hide two healthy peers; mixed-owner prepare and save keep same-name folders separate and preserve originals; invalid selections rejected; cancel removes partial cache; save success/failure discards temporary copies.
- `go vet ./internal/node ./cmd/node` and Windows x64 crosscompilation passed.
- Swift compiled; separate temporary harnesses exercised mixed-source promised-name collisions and folder online-status transitions. Source review checked selection identity, local-only deletion and root internal-drop guards on both platforms.
- Packaged Mac launched. Native root Add picker imported `LocalBucket-v02-check.txt` from `/tmp`; unified row showed its name, size and local source. Selecting it enabled Save and Trash. Only that synthetic fixture was moved to system Trash through the native confirmation, and UI confirmed success. Actual screen inspected for table/toolbar layout. Closing App stopped its listening service.
- Both distributions built via `python3 app/build.py`; final Mac bundle is ad-hoc signed. Archives and SHA-256 manifest are verified before release.

## Windows native CI

GitHub Actions runs the shared tests on Windows and macOS, compiles the Swift UI, launches the Windows GUI executable and checks `/v1/info` reports v0.2.0. The workflow is a launch smoke check; it does not validate Explorer gestures, layout rendering, or SmartScreen.

## Still requires device verification

The earlier drag spike was manually verified by the user on Mac and Windows. This new unified version still needs Mac ↔ Win10 discovery and real Finder/Explorer drag/drop regression on the user's two physical computers. Windows remote drag first prepares a cache and requires another drag; Save to works as a direct alternative. Mac remote-folder drag exports ZIP.

No trusted Windows publisher certificate, Developer ID or Apple notarization is configured. SmartScreen and Gatekeeper can still warn or block. The app does not disable system protection and is not tested under enterprise device policies. The shared space aggregates online storage; it does not replicate files.

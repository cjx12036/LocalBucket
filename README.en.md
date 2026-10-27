# LocalBucket

**Moving files between two computers shouldn’t be a chore. Just drop them into LocalBucket.**

**English** · [简体中文](README.md) · [Download previews](https://github.com/cjx12036/LocalBucket/releases)

A small LAN file-sharing app for Mac and Windows. Open it on both computers and see their files in one shared space. No account, address entry, or browser required.

**v0.2 is a feature preview.** Download the newest preview from [Releases](https://github.com/cjx12036/LocalBucket/releases).

## Get started

| System | Download | Run |
| --- | --- | --- |
| Windows 10 x64 | `LocalBucket-win-x64.exe` | Double-click; no administrator privileges needed |
| Apple Silicon Mac (M series, macOS 15+) | `LocalBucket-mac-arm64.zip` | Extract and open `LocalBucket.app` |

A Windows ZIP with instructions and `SHA256.json` checksums are also available. You do not need the Source code downloads to run the app.

1. Connect both computers to the same LAN, open LocalBucket, and allow the required local-network/firewall access.
2. **Drop files in** or use Add at the shared-space root to store copies in this computer’s dedicated bucket.
3. **Drag files out** to a desktop/folder or select Save to. Originals always remain in the bucket.

The Location column shows which computer holds each entry. Double-click a folder to open it; adding files inside it stores them on that folder’s computer. Entries with identical names stay separate across computers, including folders.

## Simple rules

- **One view, no automatic synchronization.** Files stay on their original computer and become unavailable when it goes offline. Each computer does not keep a full replica.
- Importing identical files again skips duplicates. Different content with the same name gets a numbered copy.
- Only local files can be deleted, and they go to the system Trash/Recycle Bin. Windows has a visible Delete button in the top toolbar.
- Closing the window stops sharing and exits. Stored files remain for the next launch.
- LAN access has no password. Devices that can reach the service can read and upload files; share only content you intend others on that network to access.

## OS security prompts and current limits

**The current builds cannot guarantee avoiding Windows SmartScreen or macOS Gatekeeper prompts/blocks. The app does not bypass or disable system protection.**

- **Windows:** The EXE does not yet have a trusted publisher signature. You may see “Windows protected your PC.” Only if you trust the download source, use the system’s More info → Run anyway option. Device policy may prevent this option. Even a signed new release may initially trigger reputation warnings.
- **Mac:** The app is currently ad-hoc signed, without Developer ID signing or Apple notarization. If Apple cannot verify LocalBucket, and you trust its source, use System Settings → Privacy & Security → Open Anyway. This is Gatekeeper, distinct from enterprise MDM management; managed devices may impose extra restrictions. See [Apple’s instructions](https://support.apple.com/en-us/102445).
- On Windows, dragging remote files out first prepares a download cache; drag again when ready, or use Save to directly. On Mac, remote folders dragged out are ZIP archives.
- Guest Wi-Fi isolation, VPNs, or firewalls may affect discovery. Both apps must be running. Network diagnostics are under More.

## Development

Native AppKit / Win32 + OLE interfaces share a Go node service, with Dufs as an internal file server. Discovery uses TCP/UDP `47931` and mDNS.

```sh
# Shared-service tests
go -C app test -race ./internal/node

# Package both platforms on a Mac, from the repository root
python3 app/build.py
```

Build requirements: Go 1.26+, Swift command-line tools, and Python 3. See [app instructions](app/README.md) for data paths/signing configuration and [v0.2 validation](docs/validation-v0.2.md) for test scope. User files are not committed. Third-party notices are in `spikes/drag-drop/vendor/`; the earlier prototype remains in `spikes/`.

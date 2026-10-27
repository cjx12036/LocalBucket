# Native UI contract (v0.2)

Shared Go module `github.com/cjx12036/LocalBucket/app`.

Windows starts `node.Start(node.Options{DataDir: dataDir, DufsPath: extractedDufsPath}) (*node.Service,error)`. Service fields: `AdminURL string`, `Token string`, `BucketDir string`, `DataDir string`, `Self node.Peer`. `Close() error` stops all listeners, transfers and owned processes. `Options` also has `Port int` (default47931), `Name string`, `NoDiscovery bool`.

Mac launches bundled `Resources/localbucket-node -dufs PATH [-data PATH]`. First stdout line JSON: `{"admin_url":"http://127.0.0.1:PORT","token":"...","bucket_dir":"...","data_dir":"..."}`. Startup errors go to stderr and exit nonzero. Closing stdin, SIGTERM or SIGINT exits the service. Native parent MUST close/terminate and wait for it on app exit.

Every `/app/*` request goes to AdminURL with `Authorization: Bearer TOKEN`; JSON UTF-8, errors `{ "error": "Chinese human-readable reason" }`. Local management uses this token only, never propagates it across LAN. No browser opening.

GET `/app/space` => `{"files":[{"name":"hello.txt","path":"hello.txt","is_dir":false,"size":42,"modified":1793075007,"peer":"uuid","owner":"Mac","local":true}],"peers":[Peer],"unavailable":[{"peer":"uuid","owner":"Windows","error":"暂时无法读取文件"}]}`. Unified root view. Entries retain owner ID and path, including name collisions. Concurrent listing has a two-second total deadline, up to eight active queries; partial results remain visible. Failed peers are marked online=false in this response. Refresh ~2s; preserve selection by peer+path.

GET `/app/peers` remains available for diagnostics/legacy compatibility. Self first; refresh selection by ID, never name.

GET `/app/list?peer=ID&path=REL` => `{"files":[{"name":"hello.txt","path":"sub/hello.txt","is_dir":false,"size":42,"modified":1793075007}],"local":true}`. REL is slash-separated bucket-relative path; root empty string. UI filtering can happen locally; directories open on double click.

POST `/app/import` JSON `{"peer":"ID","path":"REL_DEST_DIR","sources":["/absolute/local/file/or/folder"]}` => `{"job":"JOB_ID"}`. Asynchronous server job handles local and remote folder uploads. Do not submit internal drag back onto its current bucket; core also protects same-path input.

POST `/app/prepare` and `/app/save` additionally accept `{"items":[{"peer":"ID","path":"REL"}]}`; save also needs `destination`. Nonempty items supersede legacy peer/paths. Validate all selections before starting. One cancelable job handles mixed owners, retaining distinct same-name files/folders with numbered cache names, preserving source originals. `items:[]` is rejected. Cache files are copies even for local items; native direct all-local drags can use original file URLs instead.

POST `/app/prepare` JSON `{"peer":"ID","paths":["REL_FILE_OR_DIR"]}` => `{"job":"JOB_ID"}`. Local paths resolve to bucket originals; remote paths copied into private cache asynchronously. This supports native FileDrop drag-out and save fallback. Do not start Windows native drag until prepared.

GET `/app/jobs` => `{"jobs":[{"id":"...","kind":"import|prepare","status":"running|done|error|canceled","message":"Chinese status","error":"...","completed":0,"total":0,"paths":["/absolute/prepared/path"],"result":{"added":0,"skipped":0,"renamed":0}}]}`. UI polls ~0.5s during jobs. Error/message persists; do not overwrite with generic ready state. `paths` present on completed prepare.

POST `/app/cancel` JSON `{"job":"ID"}` => `{"ok":true}`.

POST `/app/delete` JSON `{"paths":["REL"]}` => `{"ok":true}`. Always affects ONLY local bucket, moves to OS Trash/Recycle Bin. Never called for selected remote peer.

GET `/app/download?peer=ID&path=REL` => byte stream (directory returns ZIP). Auth header required. Mac file promises can download individual files directly from here, with token only to loopback. HTTP error before writing final destination; keep original source.

POST `/app/save` JSON `{"peer":"ID","paths":["REL"],"destination":"/absolute/selected/folder"}` => job ID. Native folder chooser supplies destination; copied files use same safe collision rules, original source retained. Used for explicit 另存到….

GET `/app/diagnostics` => `{"message":"...","port":47931,"addresses":["192.168..."],"discovery":"..."}`. Show in help/network diagnostic only; no IP input UI.

POST `/app/migrate` => job ID; copies legacy prototype bucket into local product bucket using import rules. GET `/app/config` => `{"legacy_available":true,"guide_seen":false}`. POST `/app/config` `{"guide_seen":true}`. Native UI asks before legacy import; never move/delete source.

UI guidance: Chinese unified “共享空间” root with source location on every row. Root Add/import targets local self. Opening a folder chooses its owner; same-name folders stay separate. Inside-folder import targets that owner. Save/export supports mixed-owner selection. Native drag uses each entry's ownership; internal drag back at unified root is a no-op. Delete is enabled only for an all-local selection; no remote deletion. Primary Add/Save actions, secondary More menu for refresh, reveal, help and diagnostics; breadcrumb/search, online status and compact task footer. Close cancels jobs with confirmation if running, then stops node and App. No tray/autostart.

LAN server internal contract: `/v1/info`, `/v1/list?path=`, `/v1/file?path=` (GET only), `/v1/upload?path=` (POST raw file content; safe dedup), `/v1/mkdir?path=` (POST). No delete/admin routes. mDNS `_localbucket._tcp`; UDP discovery same numeric47931. Dufs binds loopback only.

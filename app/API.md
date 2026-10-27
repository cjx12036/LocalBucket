# Native UI contract (v0.1)

Shared Go module `github.com/cjx12036/LocalBucket/app`.

Windows starts `node.Start(node.Options{DataDir: dataDir, DufsPath: extractedDufsPath}) (*node.Service,error)`. Service fields: `AdminURL string`, `Token string`, `BucketDir string`, `DataDir string`, `Self node.Peer`. `Close() error` stops all listeners, transfers and owned processes. `Options` also has `Port int` (default47931), `Name string`, `NoDiscovery bool`.

Mac launches bundled `Resources/localbucket-node -dufs PATH [-data PATH]`. First stdout line JSON: `{"admin_url":"http://127.0.0.1:PORT","token":"...","bucket_dir":"...","data_dir":"..."}`. Startup errors go to stderr and exit nonzero. Closing stdin, SIGTERM or SIGINT exits the service. Native parent MUST close/terminate and wait for it on app exit.

Every `/app/*` request goes to AdminURL with `Authorization: Bearer TOKEN`; JSON UTF-8, errors `{ "error": "Chinese human-readable reason" }`. Local management uses this token only, never propagates it across LAN. No browser opening.

GET `/app/peers` => `{"peers":[{"id":"uuid","name":"电脑名","address":"http://IP:47931","local":true,"online":true}]}`. First layer shows all peers; self first. Refresh every ~2s. Selection by id, never name.

GET `/app/list?peer=ID&path=REL` => `{"files":[{"name":"hello.txt","path":"sub/hello.txt","is_dir":false,"size":42,"modified":1793075007}],"local":true}`. REL is slash-separated bucket-relative path; root empty string. UI filtering can happen locally; directories open on double click.

POST `/app/import` JSON `{"peer":"ID","path":"REL_DEST_DIR","sources":["/absolute/local/file/or/folder"]}` => `{"job":"JOB_ID"}`. Asynchronous server job handles local and remote folder uploads. Do not submit internal drag back onto its current bucket; core also protects same-path input.

POST `/app/prepare` JSON `{"peer":"ID","paths":["REL_FILE_OR_DIR"]}` => `{"job":"JOB_ID"}`. Local paths resolve to bucket originals; remote paths copied into private cache asynchronously. This supports native FileDrop drag-out and save fallback. Do not start Windows native drag until prepared.

GET `/app/jobs` => `{"jobs":[{"id":"...","kind":"import|prepare","status":"running|done|error|canceled","message":"Chinese status","error":"...","completed":0,"total":0,"paths":["/absolute/prepared/path"],"result":{"added":0,"skipped":0,"renamed":0}}]}`. UI polls ~0.5s during jobs. Error/message persists; do not overwrite with generic ready state. `paths` present on completed prepare.

POST `/app/cancel` JSON `{"job":"ID"}` => `{"ok":true}`.

POST `/app/delete` JSON `{"paths":["REL"]}` => `{"ok":true}`. Always affects ONLY local bucket, moves to OS Trash/Recycle Bin. Never called for selected remote peer.

GET `/app/download?peer=ID&path=REL` => byte stream (directory returns ZIP). Auth header required. Mac file promises can download individual files directly from here, with token only to loopback. HTTP error before writing final destination; keep original source.

POST `/app/save` JSON `{"peer":"ID","paths":["REL"],"destination":"/absolute/selected/folder"}` => job ID. Native folder chooser supplies destination; copied files use same safe collision rules, original source retained. Used for explicit 另存到….

GET `/app/diagnostics` => `{"message":"...","port":47931,"addresses":["192.168..."],"discovery":"..."}`. Show in help/network diagnostic only; no IP input UI.

POST `/app/migrate` => job ID; copies legacy prototype bucket into local product bucket using import rules. GET `/app/config` => `{"legacy_available":true,"guide_seen":false}`. POST `/app/config` `{"guide_seen":true}`. Native UI asks before legacy import; never move/delete source.

UI guidance: Chinese, computer-name root, 我的电脑 label, breadcrumb 所有电脑 > 电脑名 > 文件夹; 添加文件…, 另存到…, 删除 (only local), 刷新, 帮助, 打开本机存储位置. Root disallows file imports until a computer is opened. Native folder list scrolls/multi-selects; resizing must preserve controls. Close cancels jobs with confirmation if running, then closes node and App. No tray/autostart.

LAN server internal contract: `/v1/info`, `/v1/list?path=`, `/v1/file?path=` (GET only), `/v1/upload?path=` (POST raw file content; safe dedup), `/v1/mkdir?path=` (POST). No delete/admin routes. mDNS `_localbucket._tcp`; UDP discovery same numeric47931. Dufs binds loopback only.

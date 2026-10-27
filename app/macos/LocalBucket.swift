import AppKit
import UniformTypeIdentifiers
import Darwin

private struct Peer {
    let id: String, name: String
    let local: Bool, online: Bool
    var title: String { local ? "我的电脑 · \(name)" : name }
}
private struct Entry {
    let name: String, path: String
    let directory: Bool
    let size: Int64, modified: Double
}
private struct ServiceInfo {
    let base: URL, token: String, bucket: URL
}
private func failure(_ message: String) -> NSError {
    NSError(domain: "LocalBucket", code: 1, userInfo: [NSLocalizedDescriptionKey: message])
}

// A promise downloads to a temporary file first. The final Finder destination is
// created only after HTTP success; its source always remains in the bucket.
private final class FileTable: NSTableView {
    var exited: (() -> Void)?
    var activateSelection: (() -> Void)?
    override func draggingExited(_ sender: NSDraggingInfo?) { super.draggingExited(sender); exited?() }
    override func keyDown(with event: NSEvent) {
        if event.keyCode == 36 || event.keyCode == 76 { activateSelection?(); return }
        super.keyDown(with: event)
    }
}

private final class DownloadPromise: NSObject, NSFilePromiseProviderDelegate {
    let name: String, request: URLRequest
    let report: (String) -> Void
    let activity: (UUID, URLSessionDownloadTask?) -> Void
    init(name: String, request: URLRequest, activity: @escaping (UUID, URLSessionDownloadTask?) -> Void, report: @escaping (String) -> Void) {
        self.name = name; self.request = request; self.report = report; self.activity = activity
    }
    func filePromiseProvider(_ filePromiseProvider: NSFilePromiseProvider, fileNameForType fileType: String) -> String { name }
    func filePromiseProvider(_ filePromiseProvider: NSFilePromiseProvider, writePromiseTo url: URL, completionHandler: @escaping (Error?) -> Void) {
        report("正在复制 \(name)…")
        let id = UUID()
        let task = URLSession.shared.downloadTask(with: request) { temp, response, error in
            defer { self.activity(id, nil) }
            do {
                if let error = error { throw error }
                guard let http = response as? HTTPURLResponse, http.statusCode == 200, let temp = temp else {
                    throw failure("下载失败，请确认目标电脑仍在线。")
                }
                try FileManager.default.moveItem(at: temp, to: url)
                self.report("已复制 \(self.name)，原文件保留。")
                completionHandler(nil)
            } catch {
                self.report("复制失败：\(error.localizedDescription)")
                completionHandler(error)
            }
        }
        activity(id, task); task.resume()
    }
}

private final class App: NSObject, NSApplicationDelegate, NSWindowDelegate, NSTableViewDataSource, NSTableViewDelegate, NSSearchFieldDelegate {
    private var window: NSWindow!
    private let table = FileTable()
    private let scroll = NSScrollView()
    private let breadcrumb = NSStackView()
    private let search = NSSearchField()
    private let state = NSTextField(wrappingLabelWithString: "正在启动本机共享…")
    private let empty = NSTextField(wrappingLabelWithString: "")
    private let guide = NSTextField(wrappingLabelWithString: "本机 bucket 是独立的存储文件夹。打开一台电脑后拖入文件即可存放；拖到桌面或文件夹即可复制，原文件保留。")
    private let progress = NSProgressIndicator()
    private var addButton: NSButton!, saveButton: NSButton!, deleteButton: NSButton!, cancelButton: NSButton!, retryButton: NSButton!
    private var service: ServiceInfo?
    private var process: Process?, input: Pipe?
    private var startGeneration = UUID()
    private var peers: [Peer] = [], files: [Entry] = [], filtered: [Entry] = []
    private var selectedPeer: Peer?, directory = ""
    private var listGeneration = 0
    private var peerBusy = false, jobBusy = false, listBusy = false
    private var peerTimer: Timer?, jobTimer: Timer?
    private var activeJobs: [String] = [], seenJobs: [String: String] = [:]
    private var retryAction: (() -> Void)?
    private var jobRetries: [String: () -> Void] = [:]
    private var promises: [DownloadPromise] = []
    private var dragOrigin: (String, String)?
    private var quitting = false
    private var downloads: [UUID: URLSessionDownloadTask] = [:]
    private var pendingSubmissions = 0

    func applicationDidFinishLaunching(_ notification: Notification) {
        buildMenu(); buildWindow(); startService()
    }
    private func button(_ title: String, _ action: Selector, key: String = "") -> NSButton {
        let b = NSButton(title: title, target: self, action: action)
        b.bezelStyle = .rounded; b.keyEquivalent = key; b.setAccessibilityLabel(title)
        return b
    }
    private func buildMenu() {
        let menu = NSMenu(), appItem = NSMenuItem(), appMenu = NSMenu()
        appMenu.addItem(withTitle: "关于 LocalBucket", action: #selector(about), keyEquivalent: "")
        appMenu.addItem(.separator())
        appMenu.addItem(withTitle: "退出 LocalBucket", action: #selector(NSApplication.terminate(_:)), keyEquivalent: "q")
        appItem.submenu = appMenu; menu.addItem(appItem)
        let editItem = NSMenuItem(), edit = NSMenu(title: "编辑")
        for (title, action, key) in [("剪切", "cut:", "x"), ("复制", "copy:", "c"), ("粘贴", "paste:", "v"), ("全选", "selectAll:", "a")] {
            edit.addItem(withTitle: title, action: Selector(action), keyEquivalent: key)
        }
        editItem.submenu = edit; menu.addItem(editItem); NSApp.mainMenu = menu
    }
    private func buildWindow() {
        window = NSWindow(contentRect: NSRect(x: 0, y: 0, width: 960, height: 650), styleMask: [.titled, .closable, .miniaturizable, .resizable], backing: .buffered, defer: false)
        for name in ["LocalBucket", "AppIcon", "icon"] {
            if let url = Bundle.main.url(forResource: name, withExtension: "icns"), let image = NSImage(contentsOf: url) { NSApp.applicationIconImage = image; break }
        }
        window.title = "LocalBucket"
        window.delegate = self; window.center(); window.isReleasedWhenClosed = false
        let content = window.contentView!
        let layout = NSStackView(); layout.orientation = .vertical; layout.alignment = .leading; layout.spacing = 14
        layout.translatesAutoresizingMaskIntoConstraints = false; content.addSubview(layout)
        NSLayoutConstraint.activate([layout.leadingAnchor.constraint(equalTo: content.leadingAnchor, constant: 24), layout.trailingAnchor.constraint(equalTo: content.trailingAnchor, constant: -24), layout.topAnchor.constraint(equalTo: content.topAnchor, constant: 20), layout.bottomAnchor.constraint(equalTo: content.bottomAnchor, constant: -18)])
        let heading = NSTextField(labelWithString: "LocalBucket"); heading.font = .systemFont(ofSize: 27, weight: .semibold)
        let subtitle = NSTextField(labelWithString: "让文件在附近的电脑之间轻松流动"); subtitle.textColor = .secondaryLabelColor
        layout.addArrangedSubview(heading); layout.addArrangedSubview(subtitle)
        guide.textColor = .secondaryLabelColor; guide.font = .systemFont(ofSize: 12)
        layout.addArrangedSubview(guide)
        guide.widthAnchor.constraint(equalTo: layout.widthAnchor).isActive = true
        let tools = NSStackView(); tools.spacing = 8
        addButton = button("添加文件…", #selector(addFiles), key: "o")
        saveButton = button("另存到…", #selector(saveFiles), key: "s")
        deleteButton = button("删除", #selector(deleteFiles))
        tools.addArrangedSubview(addButton); tools.addArrangedSubview(saveButton); tools.addArrangedSubview(deleteButton)
        tools.addArrangedSubview(button("刷新", #selector(refresh), key: "r"))
        tools.addArrangedSubview(button("帮助", #selector(help)))
        tools.addArrangedSubview(button("打开本机存储位置", #selector(revealBucket)))
        layout.addArrangedSubview(tools)
        // Reserve enough content space for every control, the expanded guide,
        // three status lines and a visible 160-point file list at minimum size.
        // Native button metrics determine the horizontal limit for this locale.
        window.contentMinSize = NSSize(width: max(760, tools.fittingSize.width + 48), height: 560)
        let navigation = NSStackView(); navigation.spacing = 12
        breadcrumb.spacing = 2; breadcrumb.orientation = .horizontal
        let crumbScroll = NSScrollView(); crumbScroll.drawsBackground = false; crumbScroll.hasHorizontalScroller = true
        crumbScroll.documentView = breadcrumb
        breadcrumb.translatesAutoresizingMaskIntoConstraints = false
        NSLayoutConstraint.activate([breadcrumb.leadingAnchor.constraint(equalTo: crumbScroll.contentView.leadingAnchor), breadcrumb.topAnchor.constraint(equalTo: crumbScroll.contentView.topAnchor), breadcrumb.heightAnchor.constraint(equalToConstant: 30)])
        navigation.addArrangedSubview(crumbScroll)
        search.placeholderString = "搜索文件"; search.delegate = self
        search.setAccessibilityLabel("搜索文件或电脑")
        search.widthAnchor.constraint(equalToConstant: 190).isActive = true
        navigation.addArrangedSubview(search); layout.addArrangedSubview(navigation)
        navigation.widthAnchor.constraint(equalTo: layout.widthAnchor).isActive = true
        crumbScroll.heightAnchor.constraint(equalToConstant: 34).isActive = true
        for (id, title, width) in [("name", "名称", 540.0), ("size", "大小", 110.0), ("date", "修改时间", 170.0)] {
            let col = NSTableColumn(identifier: NSUserInterfaceItemIdentifier(id)); col.title = title; col.width = width; col.minWidth = id == "name" ? 220 : 90
            table.addTableColumn(col)
        }
        table.delegate = self; table.dataSource = self; table.allowsMultipleSelection = true
        table.rowHeight = 42; table.intercellSpacing = NSSize(width: 12, height: 3)
        table.usesAlternatingRowBackgroundColors = true; table.columnAutoresizingStyle = .firstColumnOnlyAutoresizingStyle
        table.target = self; table.doubleAction = #selector(openSelection)
        table.exited = { [weak self] in self?.highlight(false) }
        table.activateSelection = { [weak self] in self?.openRow(self?.table.selectedRow ?? -1) }
        table.setAccessibilityLabel("附近电脑和共享文件")
        table.registerForDraggedTypes([.fileURL] + NSFilePromiseReceiver.readableDraggedTypes.map { NSPasteboard.PasteboardType($0) })
        table.setDraggingSourceOperationMask(.copy, forLocal: false)
        table.setDraggingSourceOperationMask(.copy, forLocal: true)
        scroll.documentView = table; scroll.hasVerticalScroller = true; scroll.borderType = .bezelBorder
        scroll.wantsLayer = true; layout.addArrangedSubview(scroll)
        scroll.setContentHuggingPriority(NSLayoutConstraint.Priority(1), for: .vertical)
        scroll.widthAnchor.constraint(equalTo: layout.widthAnchor).isActive = true
        scroll.heightAnchor.constraint(greaterThanOrEqualToConstant: 160).isActive = true
        empty.textColor = .secondaryLabelColor; empty.alignment = .center; empty.font = .systemFont(ofSize: 13)
        layout.addArrangedSubview(empty); empty.widthAnchor.constraint(equalTo: layout.widthAnchor).isActive = true
        progress.style = .bar; progress.minValue = 0; progress.maxValue = 1; progress.isHidden = true
        layout.addArrangedSubview(progress); progress.widthAnchor.constraint(equalTo: layout.widthAnchor).isActive = true
        let footer = NSStackView(); footer.spacing = 8
        state.font = .systemFont(ofSize: 12); state.setAccessibilityLabel("传输状态")
        state.lineBreakMode = .byWordWrapping; state.maximumNumberOfLines = 3
        footer.addArrangedSubview(state)
        retryButton = button("重试", #selector(retry)); retryButton.isHidden = true; footer.addArrangedSubview(retryButton)
        cancelButton = button("取消传输", #selector(cancelJobs)); cancelButton.isHidden = true; footer.addArrangedSubview(cancelButton)
        layout.addArrangedSubview(footer); footer.widthAnchor.constraint(equalTo: layout.widthAnchor).isActive = true
        updateNavigation(); updateButtons()
        window.makeKeyAndOrderFront(nil); NSApp.activate(ignoringOtherApps: true)
    }

    // The child writes exactly one handshake line. Limit its size and startup
    // duration, and drain stderr concurrently so neither pipe can deadlock.
    private func startService() {
        guard process == nil else { return }
        state.stringValue = "正在启动本机共享…"; retryButton.isHidden = true
        let generation = UUID(); startGeneration = generation
        guard let resources = Bundle.main.resourceURL else { startupError("找不到应用资源，请重新安装。"); return }
        let p = Process(), stdout = Pipe(), stderr = Pipe(), stdin = Pipe()
        p.executableURL = resources.appendingPathComponent("localbucket-node")
        p.arguments = ["-dufs", resources.appendingPathComponent("dufs").path]
        p.standardOutput = stdout; p.standardError = stderr; p.standardInput = stdin
        let lock = NSLock(); var errors = Data()
        stderr.fileHandleForReading.readabilityHandler = { handle in
            let chunk = handle.availableData
            lock.lock(); errors.append(chunk); if errors.count > 8192 { errors = Data(errors.suffix(8192)) }; lock.unlock()
        }
        p.terminationHandler = { [weak self] _ in
            stderr.fileHandleForReading.readabilityHandler = nil
            lock.lock(); let message = String(data: errors, encoding: .utf8) ?? ""; lock.unlock()
            DispatchQueue.main.async {
                guard let self = self, self.startGeneration == generation, !self.quitting else { return }
                self.process = nil; self.service = nil; self.peerTimer?.invalidate(); self.jobTimer?.invalidate()
                self.startupError(message.isEmpty ? "共享服务已停止，请重试。" : message)
            }
        }
        do { try p.run(); process = p; input = stdin }
        catch { stderr.fileHandleForReading.readabilityHandler = nil; startupError("无法启动共享服务：\(error.localizedDescription)"); return }
        DispatchQueue.global(qos: .userInitiated).async { [weak self] in
            var line = Data()
            while line.count < 16384 {
                let byte = stdout.fileHandleForReading.readData(ofLength: 1)
                if byte.isEmpty || byte.first == 10 { break }; line.append(byte)
            }
            let obj = (try? JSONSerialization.jsonObject(with: line)) as? [String: Any]
            DispatchQueue.main.async {
                guard let self = self, self.startGeneration == generation, !self.quitting else { return }
                guard p.isRunning, let obj = obj, let address = obj["admin_url"] as? String, let base = URL(string: address), base.scheme == "http", base.host == "127.0.0.1", let token = obj["token"] as? String, !token.isEmpty, let bucket = obj["bucket_dir"] as? String, bucket.hasPrefix("/") else {
                    if p.isRunning { p.terminate() }
                    self.startupError("共享服务启动失败，请查看应用资源和网络端口是否可用。"); return
                }
                self.service = ServiceInfo(base: base, token: token, bucket: URL(fileURLWithPath: bucket))
                self.state.stringValue = "打开一台电脑，开始存放文件。"
                self.refreshPeers(); self.pollJobs(); self.loadConfig()
                self.peerTimer = Timer.scheduledTimer(withTimeInterval: 2, repeats: true) { [weak self] _ in self?.refreshPeers(); self?.loadFiles() }
                self.jobTimer = Timer.scheduledTimer(withTimeInterval: 0.5, repeats: true) { [weak self] _ in self?.pollJobs() }
                self.updateButtons()
            }
            // Drain any subsequent stdout without retaining it.
            while !stdout.fileHandleForReading.readData(ofLength: 4096).isEmpty {}
        }
        DispatchQueue.main.asyncAfter(deadline: .now() + 12) { [weak self] in
            guard let self = self, self.startGeneration == generation, self.service == nil, p.isRunning else { return }
            p.terminate(); self.startupError("共享服务启动超时，请重试。")
            DispatchQueue.global().asyncAfter(deadline: .now() + 3) { if p.isRunning { kill(p.processIdentifier, SIGKILL) } }
        }
    }
    private func startupError(_ message: String) {
        state.stringValue = message; retryAction = { [weak self] in self?.startService() }; retryButton.isHidden = false; updateButtons()
    }
    private func request(_ route: String, query: [String: String] = [:], body: [String: Any]? = nil) -> URLRequest? {
        guard let service = service else { return nil }
        var c = URLComponents(url: service.base.appendingPathComponent("app/" + route), resolvingAgainstBaseURL: false)!
        c.queryItems = query.sorted { $0.key < $1.key }.map { URLQueryItem(name: $0.key, value: $0.value) }
        var req = URLRequest(url: c.url!); req.timeoutInterval = 30
        req.setValue("Bearer " + service.token, forHTTPHeaderField: "Authorization")
        if let body = body { req.httpMethod = "POST"; req.httpBody = try? JSONSerialization.data(withJSONObject: body); req.setValue("application/json", forHTTPHeaderField: "Content-Type") }
        return req
    }
    private func api(_ route: String, query: [String: String] = [:], body: [String: Any]? = nil, completion: @escaping (Result<[String: Any], Error>) -> Void) {
        guard let req = request(route, query: query, body: body) else { completion(.failure(failure("共享服务尚未就绪。"))); return }
        URLSession.shared.dataTask(with: req) { data, response, error in
            let result: Result<[String: Any], Error>
            do {
                if let error = error { throw error }
                guard let obj = (try? JSONSerialization.jsonObject(with: data ?? Data())) as? [String: Any] else { throw failure("共享服务返回了无法识别的数据。") }
                guard let http = response as? HTTPURLResponse, (200..<300).contains(http.statusCode) else { throw failure(obj["error"] as? String ?? "请求失败，请稍后重试。") }
                result = .success(obj)
            } catch { result = .failure(error) }
            DispatchQueue.main.async { completion(result) }
        }.resume()
    }
    private func refreshPeers() {
        guard service != nil, !peerBusy else { return }; peerBusy = true
        api("peers") { [weak self] result in
            guard let self = self else { return }; self.peerBusy = false
            switch result {
            case .success(let obj):
                // Capture identities against the rows currently on screen before
                // discovery replaces/reorders the underlying peer collection.
                let selectedIDs = self.selectedRootPeerIDs
                self.peers = (obj["peers"] as? [[String: Any]] ?? []).compactMap { p in
                    guard let id = p["id"] as? String, let name = p["name"] as? String else { return nil }
                    return Peer(id: id, name: name, local: p["local"] as? Bool ?? false, online: p["online"] as? Bool ?? true)
                }.sorted { a, b in a.local != b.local ? a.local : a.name.localizedStandardCompare(b.name) == .orderedAscending }
                if let current = self.selectedPeer, let updated = self.peers.first(where: { $0.id == current.id }) { self.selectedPeer = updated }
                if self.selectedPeer == nil { self.reloadTable(rootSelection: selectedIDs) }
            case .failure(let error): self.showError(error)
            }
        }
    }
    private func loadFiles() {
        guard let peer = selectedPeer, service != nil, !listBusy else { return }
        listBusy = true; let generation = listGeneration; let path = directory
        api("list", query: ["peer": peer.id, "path": path]) { [weak self] result in
            guard let self = self else { return }; self.listBusy = false
            guard self.listGeneration == generation, self.selectedPeer?.id == peer.id, self.directory == path else { self.loadFiles(); return }
            switch result {
            case .success(let obj):
                self.files = (obj["files"] as? [[String: Any]] ?? []).compactMap { f in
                    guard let name = f["name"] as? String, let path = f["path"] as? String else { return nil }
                    return Entry(name: name, path: path, directory: f["is_dir"] as? Bool ?? false, size: (f["size"] as? NSNumber)?.int64Value ?? 0, modified: (f["modified"] as? NSNumber)?.doubleValue ?? 0)
                }.sorted { a, b in a.directory != b.directory ? a.directory : a.name.localizedStandardCompare(b.name) == .orderedAscending }
                self.reloadTable()
            case .failure(let error): self.showError(error)
            }
        }
    }
    private var visiblePeers: [Peer] { peers.filter { search.stringValue.isEmpty || $0.title.localizedCaseInsensitiveContains(search.stringValue) } }
    private var selectedRootPeerIDs: Set<String> {
        guard selectedPeer == nil else { return [] }
        let displayedPeers = visiblePeers
        return Set(table.selectedRowIndexes.compactMap { $0 < displayedPeers.count ? displayedPeers[$0].id : nil })
    }
    private func reloadTable(rootSelection: Set<String>? = nil) {
        let selectedPaths = Set(selectedEntries.map { $0.path })
        let selectedIDs = rootSelection ?? selectedRootPeerIDs
        filtered = files.filter { search.stringValue.isEmpty || $0.name.localizedCaseInsensitiveContains(search.stringValue) }
        table.reloadData()
        let rows = selectedPeer == nil ? visiblePeers.enumerated().filter { selectedIDs.contains($0.element.id) }.map { $0.offset } : filtered.enumerated().filter { selectedPaths.contains($0.element.path) }.map { $0.offset }
        table.selectRowIndexes(IndexSet(rows), byExtendingSelection: false)
        if selectedPeer == nil { empty.stringValue = peers.contains(where: { !$0.local && $0.online }) ? "双击电脑名称，查看它的文件。" : "正在寻找附近的电脑 · 请在另一台电脑运行 LocalBucket，并连接同一局域网。" }
        else { empty.stringValue = filtered.isEmpty ? (search.stringValue.isEmpty ? "把文件或文件夹拖到这里，或点击“添加文件…”" : "没有匹配的文件") : "选中文件拖到桌面或文件夹即可复制，原文件保留。" }
        updateButtons()
    }
    private var selectedEntries: [Entry] { guard selectedPeer != nil else { return [] }; return table.selectedRowIndexes.compactMap { $0 < filtered.count ? filtered[$0] : nil } }
    private func updateButtons() {
        let ready = service != nil && !quitting
        addButton?.isEnabled = ready && selectedPeer != nil
        saveButton?.isEnabled = ready && !selectedEntries.isEmpty
        deleteButton?.isHidden = selectedPeer?.local != true
        deleteButton?.isEnabled = ready && !selectedEntries.isEmpty
        cancelButton?.isHidden = activeJobs.isEmpty
    }
    private func updateNavigation() {
        for view in breadcrumb.arrangedSubviews { breadcrumb.removeArrangedSubview(view); view.removeFromSuperview() }
        let root = button("所有电脑", #selector(goRoot)); breadcrumb.addArrangedSubview(root)
        if let peer = selectedPeer {
            breadcrumb.addArrangedSubview(NSTextField(labelWithString: "›"))
            let b = button(peer.local ? "我的电脑" : peer.name, #selector(goCrumb(_:))); b.identifier = NSUserInterfaceItemIdentifier(""); breadcrumb.addArrangedSubview(b)
            var partial = ""
            for part in directory.split(separator: "/") {
                partial = partial.isEmpty ? String(part) : partial + "/" + part
                breadcrumb.addArrangedSubview(NSTextField(labelWithString: "›"))
                let b = button(String(part), #selector(goCrumb(_:))); b.identifier = NSUserInterfaceItemIdentifier(partial); breadcrumb.addArrangedSubview(b)
            }
        }
    }
    private func navigate(_ peer: Peer?, path: String = "") {
        listGeneration += 1; selectedPeer = peer; directory = path; files = []; filtered = []; search.stringValue = ""
        table.deselectAll(nil); updateNavigation(); reloadTable(); loadFiles()
    }
    @objc private func goRoot() { navigate(nil) }
    @objc private func goCrumb(_ sender: NSButton) { navigate(selectedPeer, path: sender.identifier?.rawValue ?? "") }
    @objc private func openSelection() {
        openRow(table.clickedRow)
    }
    private func openRow(_ row: Int) {
        guard row >= 0 else { return }
        if selectedPeer == nil { guard row < visiblePeers.count else { return }; navigate(visiblePeers[row]) }
        else if row < filtered.count, filtered[row].directory { navigate(selectedPeer, path: filtered[row].path) }
    }
    func controlTextDidChange(_ obj: Notification) { reloadTable() }
    func numberOfRows(in tableView: NSTableView) -> Int { selectedPeer == nil ? visiblePeers.count : filtered.count }
    func tableViewSelectionDidChange(_ notification: Notification) { updateButtons() }
    func tableView(_ tableView: NSTableView, viewFor tableColumn: NSTableColumn?, row: Int) -> NSView? {
        let id = tableColumn?.identifier.rawValue ?? "name"
        let cell = NSTableCellView(), text = NSTextField(labelWithString: "")
        text.lineBreakMode = .byTruncatingMiddle; text.font = .systemFont(ofSize: 13)
        text.translatesAutoresizingMaskIntoConstraints = false; cell.addSubview(text); cell.textField = text
        var leading: CGFloat = 8
        if id == "name" {
            let icon = NSImageView(); icon.translatesAutoresizingMaskIntoConstraints = false; cell.addSubview(icon)
            if selectedPeer == nil { icon.image = NSImage(systemSymbolName: "desktopcomputer", accessibilityDescription: "电脑") }
            else if row < filtered.count { let e = filtered[row]; icon.image = NSWorkspace.shared.icon(for: e.directory ? .folder : (UTType(filenameExtension: (e.name as NSString).pathExtension) ?? .data)) }
            NSLayoutConstraint.activate([icon.leadingAnchor.constraint(equalTo: cell.leadingAnchor, constant: 8), icon.centerYAnchor.constraint(equalTo: cell.centerYAnchor), icon.widthAnchor.constraint(equalToConstant: 24), icon.heightAnchor.constraint(equalToConstant: 24)])
            leading = 44
        }
        NSLayoutConstraint.activate([text.leadingAnchor.constraint(equalTo: cell.leadingAnchor, constant: leading), text.trailingAnchor.constraint(equalTo: cell.trailingAnchor, constant: -8), text.centerYAnchor.constraint(equalTo: cell.centerYAnchor)])
        if selectedPeer == nil {
            guard row < visiblePeers.count else { return cell }; let p = visiblePeers[row]
            text.stringValue = id == "name" ? p.title : (id == "size" ? (p.online ? "在线" : "离线") : (p.local ? "本机" : "附近电脑"))
        } else {
            guard row < filtered.count else { return cell }; let f = filtered[row]
            if id == "name" { text.stringValue = f.name; cell.toolTip = f.name }
            else if id == "size" { text.stringValue = f.directory ? "文件夹" : ByteCountFormatter.string(fromByteCount: f.size, countStyle: .file) }
            else { text.stringValue = DateFormatter.localizedString(from: Date(timeIntervalSince1970: f.modified), dateStyle: .short, timeStyle: .short) }
        }
        if id != "name" { text.textColor = .secondaryLabelColor }
        return cell
    }

    @objc private func refresh() { refreshPeers(); loadFiles() }
    @objc private func addFiles() {
        guard selectedPeer != nil else { return }
        let panel = NSOpenPanel(); panel.canChooseDirectories = true; panel.canChooseFiles = true; panel.allowsMultipleSelection = true
        panel.message = "选择要复制到这台电脑的文件或文件夹"; panel.prompt = "添加"
        panel.beginSheetModal(for: window) { [weak self] response in if response == .OK { self?.importFiles(panel.urls) } }
    }
    private func importFiles(_ urls: [URL]) {
        guard let peer = selectedPeer, !urls.isEmpty else { return }
        submit("import", body: ["peer": peer.id, "path": directory, "sources": urls.map { $0.path }])
    }
    @objc private func saveFiles() {
        guard let peer = selectedPeer, !selectedEntries.isEmpty else { return }; let paths = selectedEntries.map { $0.path }
        let panel = NSOpenPanel(); panel.canChooseDirectories = true; panel.canChooseFiles = false; panel.canCreateDirectories = true
        panel.prompt = "另存到这里"; panel.message = "复制所选文件，原文件保留。"
        panel.beginSheetModal(for: window) { [weak self] response in
            guard response == .OK, let destination = panel.url else { return }
            self?.submit("save", body: ["peer": peer.id, "paths": paths, "destination": destination.path])
        }
    }
    @objc private func deleteFiles() {
        guard selectedPeer?.local == true, !selectedEntries.isEmpty else { return }; let paths = selectedEntries.map { $0.path }
        let alert = NSAlert(); alert.messageText = "将 \(paths.count) 个项目移到废纸篓？"; alert.informativeText = "可在 Finder 的废纸篓中恢复。"; alert.addButton(withTitle: "移到废纸篓"); alert.addButton(withTitle: "取消")
        alert.beginSheetModal(for: window) { [weak self] response in
            guard response == .alertFirstButtonReturn else { return }
            self?.api("delete", body: ["paths": paths]) { result in
                switch result { case .success: self?.state.stringValue = "已移到废纸篓。"; self?.loadFiles(); case .failure(let error): self?.showError(error) }
            }
        }
    }
    private func submit(_ route: String, body: [String: Any]) {
        guard !quitting else { return }
        let action: () -> Void = { [weak self] in self?.submit(route, body: body) }
        retryAction = action; retryButton.isHidden = true; state.stringValue = "正在提交任务…"
        pendingSubmissions += 1
        api(route, body: body) { [weak self] result in
            guard let self = self else { return }; self.pendingSubmissions -= 1
            switch result {
            case .success(let obj):
                guard let job = obj["job"] as? String else { self.showError(failure("服务没有返回任务编号。")); return }
                self.jobRetries[job] = action
                self.activeJobs.append(job); self.state.stringValue = "任务已开始…"; self.updateButtons(); self.pollJobs()
            case .failure(let error): self.showError(error)
            }
        }
    }
    private func pollJobs() {
        guard service != nil, !jobBusy else { return }; jobBusy = true
        api("jobs") { [weak self] result in
            guard let self = self else { return }; self.jobBusy = false
            guard case .success(let obj) = result else { return }
            let jobs = obj["jobs"] as? [[String: Any]] ?? []
            var running: [[String: Any]] = []
            for job in jobs {
                guard let id = job["id"] as? String, let status = job["status"] as? String else { continue }
                if status == "running" { running.append(job) }
                if self.seenJobs[id] != status, status != "running" {
                    let message = job["message"] as? String ?? "任务已完成。"
                    if status == "error" { self.state.stringValue = job["error"] as? String ?? message; self.retryAction = self.jobRetries[id]; self.retryButton.isHidden = self.retryAction == nil }
                    else {
                        var detail = message
                        if let counts = job["result"] as? [String: Any] {
                            detail += "  添加 \(counts["added"] as? Int ?? 0)，跳过 \(counts["skipped"] as? Int ?? 0)，保留新副本 \(counts["renamed"] as? Int ?? 0)。"
                        }
                        self.state.stringValue = detail
                    }
                    self.loadFiles()
                }
                self.seenJobs[id] = status
            }
            self.activeJobs = running.compactMap { $0["id"] as? String }; self.progress.isHidden = running.isEmpty
            if let job = running.first {
                let total = (job["total"] as? NSNumber)?.doubleValue ?? 0, completed = (job["completed"] as? NSNumber)?.doubleValue ?? 0
                self.progress.isIndeterminate = total <= 0
                if total > 0 { self.progress.stopAnimation(nil); self.progress.doubleValue = min(1, completed / total) } else { self.progress.startAnimation(nil) }
                self.state.stringValue = (job["message"] as? String ?? "正在传输…") + (running.count > 1 ? "（\(running.count) 个任务）" : "")
            }
            self.updateButtons()
        }
    }
    private func showError(_ error: Error) { state.stringValue = error.localizedDescription; retryButton.isHidden = retryAction == nil }
    @objc private func retry() { retryAction?() }
    @objc private func cancelJobs() { for id in activeJobs { api("cancel", body: ["job": id]) { [weak self] result in if case .failure(let error) = result { self?.showError(error) } } } }
    @objc private func revealBucket() { if let bucket = service?.bucket { NSWorkspace.shared.open(bucket) } }
    private func loadConfig() {
        api("config") { [weak self] result in
            guard let self = self, case .success(let obj) = result else { return }
            self.guide.isHidden = obj["guide_seen"] as? Bool ?? false
            if obj["legacy_available"] as? Bool == true {
                let a = NSAlert(); a.messageText = "发现旧版测试文件"; a.informativeText = "是否复制到本机 bucket？原型中的文件会保留，不会移动或删除。"; a.addButton(withTitle: "复制导入"); a.addButton(withTitle: "暂不导入")
                a.beginSheetModal(for: self.window) { [weak self] response in if response == .alertFirstButtonReturn { self?.submit("migrate", body: [:]) } }
            }
        }
    }
    @objc private func help() {
        let a = NSAlert(); a.messageText = "在附近电脑间存放文件"
        a.informativeText = "1. 在每台电脑运行 LocalBucket，并连接同一局域网。\n2. 双击电脑名称，进入它的独立 bucket。\n3. 拖入文件或文件夹即可添加；拖出或“另存到…”会复制并保留原文件。\n\n只有本机文件可移到废纸篓。关闭窗口将停止共享并退出。\n\n找不到电脑？确认对方软件正在运行，并允许系统的本地网络访问。部分访客 Wi-Fi 会隔离设备。"
        a.addButton(withTitle: "知道了"); a.addButton(withTitle: "网络诊断"); a.addButton(withTitle: "显示／关闭入门指引")
        a.beginSheetModal(for: window) { [weak self] response in
            guard let self = self else { return }
            if response == .alertSecondButtonReturn { self.diagnostics() }
            if response == .alertThirdButtonReturn { self.guide.isHidden.toggle(); self.api("config", body: ["guide_seen": self.guide.isHidden]) { _ in } }
        }
    }
    private func diagnostics() {
        api("diagnostics") { [weak self] result in
            guard let self = self else { return }; let a = NSAlert(); a.messageText = "网络诊断"
            switch result {
            case .success(let obj): a.informativeText = "\(obj["message"] as? String ?? "")\n服务端口：\(obj["port"] as? Int ?? 47931)\n本机网络：\((obj["addresses"] as? [String] ?? []).joined(separator: "、"))\n发现状态：\(obj["discovery"] as? String ?? "未知")"
            case .failure(let error): a.informativeText = error.localizedDescription
            }
            a.beginSheetModal(for: self.window)
        }
    }
    @objc private func about() { NSApp.orderFrontStandardAboutPanel(options: [.applicationName: "LocalBucket", .applicationVersion: "0.1", .credits: NSAttributedString(string: "附近电脑间的文件存放工具")]) }

    func tableView(_ tableView: NSTableView, pasteboardWriterForRow row: Int) -> NSPasteboardWriting? {
        guard let peer = selectedPeer, row < filtered.count, let service = service else { return nil }
        let entry = filtered[row]
        if peer.local { return service.bucket.appendingPathComponent(entry.path) as NSURL }
        guard let req = request("download", query: ["peer": peer.id, "path": entry.path]) else { return nil }
        let name = entry.name + (entry.directory ? ".zip" : "")
        let promise = DownloadPromise(name: name, request: req, activity: { [weak self] id, task in
            DispatchQueue.main.async {
                guard let self = self else { task?.cancel(); return }
                if self.quitting { task?.cancel() }
                self.downloads[id] = task
            }
        }) { [weak self] message in DispatchQueue.main.async { self?.state.stringValue = message } }
        promises.append(promise)
        return NSFilePromiseProvider(fileType: entry.directory ? UTType.zip.identifier : (UTType(filenameExtension: (entry.name as NSString).pathExtension) ?? .data).identifier, delegate: promise)
    }
    func tableView(_ tableView: NSTableView, draggingSession session: NSDraggingSession, willBeginAt screenPoint: NSPoint, forRowIndexes rowIndexes: IndexSet) {
        if let peer = selectedPeer { dragOrigin = (peer.id, directory) }
    }
    func tableView(_ tableView: NSTableView, draggingSession session: NSDraggingSession, endedAt screenPoint: NSPoint, operation: NSDragOperation) {
        highlight(false); dragOrigin = nil
        // Providers may finish after Finder has accepted a drag. Keep delegates
        // alive for the application lifetime rather than invalidating promises.
    }
    private func highlight(_ enabled: Bool) { scroll.layer?.borderWidth = enabled ? 3 : 0; scroll.layer?.borderColor = NSColor.controlAccentColor.cgColor }
    func tableView(_ tableView: NSTableView, validateDrop info: NSDraggingInfo, proposedRow row: Int, proposedDropOperation dropOperation: NSTableView.DropOperation) -> NSDragOperation {
        tableView.setDropRow(-1, dropOperation: .on)
        guard let peer = selectedPeer else { state.stringValue = "请先双击打开目标电脑，再添加文件。"; highlight(false); return [] }
        if let source = info.draggingSource as? NSTableView, source === table, let origin = dragOrigin, origin.0 == peer.id, origin.1 == directory { state.stringValue = "文件已在这里，无需重复添加。"; highlight(false); return [] }
        guard info.draggingPasteboard.canReadObject(forClasses: [NSURL.self], options: [.urlReadingFileURLsOnly: true]) else { highlight(false); return [] }
        state.stringValue = "添加到「\(peer.local ? "我的电脑" : peer.name)\(directory.isEmpty ? "" : "/" + directory)」"
        highlight(true); return .copy
    }
    func tableView(_ tableView: NSTableView, acceptDrop info: NSDraggingInfo, row: Int, dropOperation: NSTableView.DropOperation) -> Bool {
        highlight(false)
        guard selectedPeer != nil else { return false }
        if let source = info.draggingSource as? NSTableView, source === table, let origin = dragOrigin, origin.0 == selectedPeer?.id, origin.1 == directory { state.stringValue = "文件已在这里，无需重复添加。"; return false }
        guard let urls = info.draggingPasteboard.readObjects(forClasses: [NSURL.self], options: [.urlReadingFileURLsOnly: true]) as? [URL], !urls.isEmpty else { return false }
        importFiles(urls); return true
    }

    func windowShouldClose(_ sender: NSWindow) -> Bool { NSApp.terminate(nil); return false }
    func applicationShouldTerminateAfterLastWindowClosed(_ sender: NSApplication) -> Bool { true }
    func applicationShouldTerminate(_ sender: NSApplication) -> NSApplication.TerminateReply {
        if quitting { return .terminateLater }
        if !activeJobs.isEmpty || !downloads.isEmpty || pendingSubmissions > 0 {
            let a = NSAlert(); a.messageText = "停止共享并退出？"; a.informativeText = "仍有文件正在传输。退出会中断未完成的任务，已完成的文件会保留。"; a.addButton(withTitle: "停止并退出"); a.addButton(withTitle: "继续传输")
            if a.runModal() != .alertFirstButtonReturn { return .terminateCancel }
        }
        quitting = true; downloads.values.forEach { $0.cancel() }; peerTimer?.invalidate(); jobTimer?.invalidate(); updateButtons(); state.stringValue = "正在停止共享…"
        guard let p = process else { return .terminateNow }
        try? input?.fileHandleForWriting.close()
        DispatchQueue.global().async {
            let deadline = Date().addingTimeInterval(2)
            while p.isRunning && Date() < deadline { usleep(50_000) }
            if p.isRunning { p.terminate() }
            let killDeadline = Date().addingTimeInterval(3)
            while p.isRunning && Date() < killDeadline { usleep(50_000) }
            if p.isRunning { kill(p.processIdentifier, SIGKILL) }
            p.waitUntilExit()
            DispatchQueue.main.async { NSApp.reply(toApplicationShouldTerminate: true) }
        }
        return .terminateLater
    }
}

let application = NSApplication.shared
application.setActivationPolicy(.regular)
private let delegate = App()
application.delegate = delegate
application.run()

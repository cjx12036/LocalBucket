import AppKit
import UniformTypeIdentifiers

// Disposable OS capability probe. Not the product's UI/framework decision.
let fm = FileManager.default
let root = fm.homeDirectoryForCurrentUser.appendingPathComponent("Library/Application Support/LocalBucketDragSpike")
let bucket = root.appendingPathComponent("bucket")
let logURL = root.appendingPathComponent("events.log")
func log(_ message: String) {
    let data = "\(ISO8601DateFormatter().string(from: Date())) \(message)\n".data(using: .utf8)!
    if !fm.fileExists(atPath: logURL.path) { fm.createFile(atPath: logURL.path, contents: nil) }
    if let h = try? FileHandle(forWritingTo: logURL) { try? h.seekToEnd(); try? h.write(contentsOf: data); try? h.close() }
}
func unique(_ name: String, in dir: URL) -> URL {
    let original = URL(fileURLWithPath: name)
    var result = dir.appendingPathComponent(original.lastPathComponent)
    var n = 1
    while fm.fileExists(atPath: result.path) {
        let ext = original.pathExtension
        result = dir.appendingPathComponent("\(original.deletingPathExtension().lastPathComponent) (\(n))\(ext.isEmpty ? "" : "." + ext)")
        n += 1
    }
    return result
}
struct Entry {
    let name: String
    let url: URL
    let remote: Bool
}

final class Promise: NSObject, NSFilePromiseProviderDelegate {
    let entry: Entry
    init(_ entry: Entry) { self.entry = entry }
    func filePromiseProvider(_ provider: NSFilePromiseProvider, fileNameForType type: String) -> String { entry.name }
    func filePromiseProvider(_ provider: NSFilePromiseProvider, writePromiseTo url: URL, completionHandler: @escaping (Error?) -> Void) {
        log("PROMISE_BEGIN \(entry.url.absoluteString) -> \(url.path)")
        URLSession.shared.downloadTask(with: entry.url) { temp, response, error in
            do {
                if let error = error { throw error }
                guard let response = response as? HTTPURLResponse, response.statusCode == 200, let temp = temp else {
                    throw NSError(domain: "HTTPDownload", code: (response as? HTTPURLResponse)?.statusCode ?? -1)
                }
                try fm.moveItem(at: temp, to: url)
                log("PROMISE_OK \(url.path)")
                completionHandler(nil)
            } catch { log("PROMISE_ERROR \(error)"); completionHandler(error) }
        }.resume()
    }
}

final class FilesView: NSView, NSDraggingSource {
    var entries: [Entry] = [] { didSet { needsDisplay = true } }
    var selected = -1
    var onDrop: (([URL]) -> Void)?
    var promises: [Promise] = []
    override init(frame: NSRect) {
        super.init(frame: frame)
        registerForDraggedTypes([.fileURL])
    }
    required init?(coder: NSCoder) { fatalError() }
    override var isFlipped: Bool { true }
    override func draw(_ dirtyRect: NSRect) {
        NSColor.controlBackgroundColor.setFill(); bounds.fill()
        let hint = "Drag files/folders IN here. Drag a row OUT to Finder."
        (hint as NSString).draw(at: NSPoint(x: 14, y: 12), withAttributes: [.font: NSFont.systemFont(ofSize: 12), .foregroundColor: NSColor.secondaryLabelColor])
        for (i, e) in entries.enumerated() {
            let y = CGFloat(44 + i * 38)
            if i == selected { NSColor.selectedContentBackgroundColor.withAlphaComponent(0.2).setFill(); NSRect(x: 4, y: y - 4, width: bounds.width - 8, height: 34).fill() }
            let title = (e.remote ? "REMOTE  " : "LOCAL     ") + e.name
            (title as NSString).draw(at: NSPoint(x: 15, y: y), withAttributes: [.font: NSFont.systemFont(ofSize: 14), .foregroundColor: NSColor.labelColor])
        }
    }
    override func mouseDown(with event: NSEvent) {
        let p = convert(event.locationInWindow, from: nil)
        selected = Int((p.y - 40) / 38)
        if p.y < 40 || selected >= entries.count { selected = -1 }
        needsDisplay = true
    }
    override func mouseDragged(with event: NSEvent) {
        guard selected >= 0 && selected < entries.count else { return }
        let e = entries[selected]
        let item: NSDraggingItem
        if e.remote {
            let delegate = Promise(e); promises.append(delegate)
            let provider = NSFilePromiseProvider(fileType: UTType(filenameExtension: (e.name as NSString).pathExtension)?.identifier ?? UTType.data.identifier, delegate: delegate)
            item = NSDraggingItem(pasteboardWriter: provider)
        } else { item = NSDraggingItem(pasteboardWriter: e.url as NSURL) }
        let p = convert(event.locationInWindow, from: nil)
        item.setDraggingFrame(NSRect(x: p.x, y: p.y, width: 32, height: 32), contents: NSWorkspace.shared.icon(for: UTType.data))
        beginDraggingSession(with: [item], event: event, source: self)
        log("DRAG_BEGIN \(e.remote ? "remote-promise" : "local-url") \(e.name)")
    }
    func draggingSession(_ session: NSDraggingSession, sourceOperationMaskFor context: NSDraggingContext) -> NSDragOperation { .copy }
    func ignoreModifierKeys(for session: NSDraggingSession) -> Bool { true }
    func draggingSession(_ session: NSDraggingSession, endedAt screenPoint: NSPoint, operation: NSDragOperation) {
        log("DRAG_END operation=\(operation.rawValue)")
    }
    override func draggingEntered(_ sender: NSDraggingInfo) -> NSDragOperation {
        sender.draggingPasteboard.canReadObject(forClasses: [NSURL.self], options: [.urlReadingFileURLsOnly: true]) ? .copy : []
    }
    override func performDragOperation(_ sender: NSDraggingInfo) -> Bool {
        guard let urls = sender.draggingPasteboard.readObjects(forClasses: [NSURL.self], options: [.urlReadingFileURLsOnly: true]) as? [URL], !urls.isEmpty else { return false }
        onDrop?(urls); return true
    }
}

final class App: NSObject, NSApplicationDelegate {
    var window: NSWindow!
    var files: FilesView!
    var address: NSTextField!
    var status: NSTextField!
    var process: Process?
    var remoteBase: URL?
    var localEntries: [Entry] = []
    var remoteEntries: [Entry] = []
    func applicationDidFinishLaunching(_ notification: Notification) {
        do {
            try fm.createDirectory(at: bucket, withIntermediateDirectories: true)
            let fixture = bucket.appendingPathComponent("Mac 拖出测试.txt")
            if !fm.fileExists(atPath: fixture.path) { try "Mac native copy test — 原文件保留\n".write(to: fixture, atomically: true, encoding: .utf8) }
            let p = Process(); p.executableURL = Bundle.main.resourceURL!.appendingPathComponent("dufs")
            p.arguments = [bucket.path, "-b", "0.0.0.0", "-p", "47931", "--allow-upload", "--allow-archive"]
            p.standardOutput = FileHandle.nullDevice; p.standardError = FileHandle.nullDevice
            try p.run(); process = p
            log("START pid=\(p.processIdentifier) bucket=\(bucket.path)")
        } catch { log("START_ERROR \(error)") }
        window = NSWindow(contentRect: NSRect(x: 40, y: 160, width: 690, height: 520), styleMask: [.titled, .closable, .miniaturizable, .resizable], backing: .buffered, defer: false)
        window.title = "LocalBucket Drag Spike — disposable prototype"
        let content = window.contentView!
        let header = NSTextField(labelWithString: "Mac native drag/drop · Dufs :47931 · test files only")
        header.frame = NSRect(x: 16, y: 478, width: 650, height: 24); content.addSubview(header)
        address = NSTextField(string: "http://127.0.0.1:47931/")
        address.frame = NSRect(x: 16, y: 441, width: 426, height: 26); content.addSubview(address)
        let connect = NSButton(title: "Load remote", target: self, action: #selector(loadRemote)); connect.frame = NSRect(x: 450, y: 439, width: 130, height: 30); content.addSubview(connect)
        let clear = NSButton(title: "Local", target: self, action: #selector(showLocal)); clear.frame = NSRect(x: 580, y: 439, width: 90, height: 30); content.addSubview(clear)
        files = FilesView(frame: NSRect(x: 16, y: 86, width: 658, height: 343)); files.autoresizingMask = [.width, .height]; content.addSubview(files)
        files.onDrop = { [weak self] urls in self?.importFiles(urls) }
        status = NSTextField(wrappingLabelWithString: "Ready. Local mode: drag files in; drag rows out. Remote mode: HTTP upload in; file-promise download out.")
        status.frame = NSRect(x: 16, y: 34, width: 650, height: 44); content.addSubview(status)
        let open = NSButton(title: "Open test bucket in Finder", target: self, action: #selector(openBucket)); open.frame = NSRect(x: 16, y: 4, width: 240, height: 28); content.addSubview(open)
        refreshLocal(); window.makeKeyAndOrderFront(nil); NSApp.activate(ignoringOtherApps: true)
    }
    func refreshLocal() {
        localEntries = ((try? fm.contentsOfDirectory(at: bucket, includingPropertiesForKeys: nil)) ?? []).filter { !$0.lastPathComponent.hasPrefix(".") }.sorted { $0.lastPathComponent < $1.lastPathComponent }.map { Entry(name: $0.lastPathComponent, url: $0, remote: false) }
        if remoteBase == nil { files.entries = localEntries }
    }
    @objc func openBucket() { NSWorkspace.shared.open(bucket) }
    @objc func showLocal() { remoteBase = nil; refreshLocal(); status.stringValue = "LOCAL mode: dropped files are copied into this Mac's test bucket." }
    @objc func loadRemote() {
        guard let base = URL(string: address.stringValue), ["http", "https"].contains(base.scheme ?? "") else { status.stringValue = "Enter http://IP:47931/"; return }
        var components = URLComponents(url: base, resolvingAgainstBaseURL: false)!; components.query = "json"
        URLSession.shared.dataTask(with: components.url!) { data, response, error in
            do {
                if let error = error { throw error }
                guard let data = data, let json = try JSONSerialization.jsonObject(with: data) as? [String: Any], let paths = json["paths"] as? [[String: Any]] else { throw NSError(domain: "BadDirectory", code: 1) }
                let entries = paths.compactMap { obj -> Entry? in
                    guard let name = obj["name"] as? String, obj["path_type"] as? String == "File" else { return nil }
                    return Entry(name: name, url: base.appendingPathComponent(name), remote: true)
                }
                DispatchQueue.main.async { self.remoteBase = base; self.remoteEntries = entries; self.files.entries = entries; self.status.stringValue = "REMOTE: \(entries.count) files. Drag out downloads via a file promise; source remains on server." }
            } catch { DispatchQueue.main.async { self.status.stringValue = "Remote error: \(error.localizedDescription)" }; log("REMOTE_ERROR \(error)") }
        }.resume()
    }
    func importFiles(_ urls: [URL]) {
        if let remote = remoteBase {
            for url in urls {
                var isDir: ObjCBool = false
                guard fm.fileExists(atPath: url.path, isDirectory: &isDir), !isDir.boolValue else { status.stringValue = "Remote folder upload is outside this spike; test regular files."; continue }
                var req = URLRequest(url: remote.appendingPathComponent(url.lastPathComponent)); req.httpMethod = "PUT"
                URLSession.shared.uploadTask(with: req, fromFile: url) { _, response, error in
                    let code = (response as? HTTPURLResponse)?.statusCode ?? 0
                    log("UPLOAD \(url.lastPathComponent) status=\(code) error=\(String(describing: error)) sourceExists=\(fm.fileExists(atPath: url.path))")
                    DispatchQueue.main.async { self.status.stringValue = "Upload \(url.lastPathComponent): HTTP \(code)"; self.loadRemote() }
                }.resume()
            }
        } else {
            for url in urls {
                do { let dest = unique(url.lastPathComponent, in: bucket); try fm.copyItem(at: url, to: dest); log("DROP_IN_OK \(url.lastPathComponent) -> \(dest.path) sourceExists=\(fm.fileExists(atPath: url.path))") }
                catch { log("DROP_IN_ERROR \(error)"); status.stringValue = error.localizedDescription }
            }
            refreshLocal(); status.stringValue = "Copied \(urls.count) item(s) into test bucket. Originals preserved."
        }
    }
    func applicationShouldTerminateAfterLastWindowClosed(_ sender: NSApplication) -> Bool { true }
    func applicationWillTerminate(_ notification: Notification) { process?.terminate(); log("STOP") }
}
let application = NSApplication.shared
application.setActivationPolicy(.regular)
let delegate = App(); application.delegate = delegate
application.run()

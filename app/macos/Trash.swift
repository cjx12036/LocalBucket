import Foundation

guard CommandLine.arguments.count == 2 else { exit(2) }
do {
    let source = URL(fileURLWithPath: CommandLine.arguments[1])
    try FileManager.default.trashItem(at: source, resultingItemURL: nil)
} catch {
    FileHandle.standardError.write(Data(error.localizedDescription.utf8))
    exit(1)
}

// The relay's address as the user typed it, and the URLs the app and its extensions call.
import Foundation

enum RelayURL {
    /// The relay's base URL from what the user typed: a bare host gets https://, a trailing
    /// slash, /link or /api/ws is dropped. nil when it is not a URL.
    static func base(_ typed: String) -> URL? {
        var s = typed.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !s.isEmpty else { return nil }
        if !s.contains("://") { s = "https://" + s }
        s = s.replacingOccurrences(of: "wss://", with: "https://").replacingOccurrences(of: "ws://", with: "http://")
        guard var c = URLComponents(string: s), let host = c.host, !host.isEmpty else { return nil }
        var path = c.path
        for suffix in ["/api/ws", "/link"] where path.hasSuffix(suffix) {
            path.removeLast(suffix.count)
        }
        while path.hasSuffix("/") { path.removeLast() }
        c.path = path
        c.query = nil
        return c.url
    }

    /// The app's live stream for a base URL.
    static func websocket(_ base: URL) -> URL {
        var c = URLComponents(url: base, resolvingAgainstBaseURL: false)!
        c.scheme = c.scheme == "http" ? "ws" : "wss"
        c.path += "/api/ws"
        return c.url!
    }

    static func api(_ base: URL, _ path: String) -> URL { base.appending(path: path) }
}

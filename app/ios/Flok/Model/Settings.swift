// What the app remembers: the relay, the device token, the screen font. The simulator can be
// pointed at a relay without touching the UI: `simctl launch … nl.w4j.flok -relayURL https://…
// -deviceToken …` (launch arguments land in UserDefaults).
import Foundation
import Observation

@MainActor
@Observable
final class Settings {
    static let shared = Settings()

    var relayURL: String { didSet { defaults.set(relayURL, forKey: "relayURL") } }
    var deviceToken: String { didSet { Keychain.set(deviceToken, for: "deviceToken") } }
    var fontSize: Double { didSet { defaults.set(fontSize, forKey: "fontSize") } }
    var wrapLines: Bool { didSet { defaults.set(wrapLines, forKey: "wrapLines") } }
    /// "<relay>|<apns token>" of the last device registration, so it is not repeated at every start.
    var registered: String? { didSet { defaults.set(registered, forKey: "registered") } }

    private let defaults = UserDefaults.standard

    init() {
        relayURL = defaults.string(forKey: "relayURL") ?? ""
        deviceToken = Keychain.get("deviceToken") ?? defaults.string(forKey: "deviceToken") ?? ""
        fontSize = defaults.object(forKey: "fontSize") as? Double ?? 11
        wrapLines = defaults.bool(forKey: "wrapLines")
        registered = defaults.string(forKey: "registered")
    }

    var isConfigured: Bool { baseURL != nil && !deviceToken.isEmpty }
    var baseURL: URL? { RelayURL.base(relayURL) }
}

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

// The flok Live Activity (Dynamic Island, Lock Screen): its attributes and content, decoded by
// ActivityKit from the relay's pushes (internal/relay/live.go: LiveAttributes, LiveState, the
// same names; every field is always sent). One type, two kinds: the overview, the phone's menu
// bar over every instance, and one followed agent.
import ActivityKit
import Foundation

struct FlokActivityAttributes: ActivityAttributes {
    struct ContentState: Codable, Hashable, Sendable {
        var working: Int
        var waiting: Int // blocked: a prompt or a question
        var done: Int // finished, not seen yet
        var rows: [LiveRow]
    }

    var kind: String // overview | agent
    var instance: String
    var pane: String
    var agent: String

    static let overview = FlokActivityAttributes(kind: "overview", instance: "", pane: "", agent: "")
}

struct LiveRow: Codable, Hashable, Sendable, Identifiable {
    var instance: String
    var host: String
    var agent: String
    var pane: String
    var state: String // working | blocked | done | idle | gone
    var label: String
    var since: Double // Unix seconds the state began; 0 = unknown

    var id: String { instance + ":" + pane }
    var started: Date? { since > 0 ? Date(timeIntervalSince1970: since) : nil }
    var place: String { host.isEmpty ? instance : instance + " / " + host }

    /// The app opens this agent: flok://agent?instance=…&pane=…
    var link: URL {
        var c = URLComponents()
        c.scheme = "flok"
        c.host = "agent"
        c.queryItems = [URLQueryItem(name: "instance", value: instance), URLQueryItem(name: "pane", value: pane)]
        return c.url ?? URL(string: "flok://home")!
    }
}

extension FlokActivityAttributes.ContentState {
    static let quiet = Self(working: 0, waiting: 0, done: 0, rows: [])

    /// What the island shows at a glance: what waits first, then what works, then what finished.
    var dominant: String {
        if waiting > 0 { return "blocked" }
        if working > 0 { return "working" }
        if done > 0 { return "done" }
        return "idle"
    }
}

enum FlokLink {
    /// The target of a flok:// link: the agent it names, nil for anything else (flok://home).
    static func target(_ url: URL) -> (instance: String, pane: String)? {
        guard url.scheme == "flok", url.host == "agent", let items = URLComponents(url: url, resolvingAgainstBaseURL: false)?.queryItems,
              let instance = items.first(where: { $0.name == "instance" })?.value, let pane = items.first(where: { $0.name == "pane" })?.value,
              !instance.isEmpty, !pane.isEmpty else { return nil }
        return (instance, pane)
    }
}

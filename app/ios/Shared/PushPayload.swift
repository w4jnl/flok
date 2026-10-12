// What a flok push carries besides its text (the relay's Alert): which agent, where, and why,
// for the actions, the service extension's state tile and the expanded card.
import Foundation

struct PushPayload: Sendable, Equatable {
    var instance: String
    var pane: String
    var agent: String
    var host: String
    var session: String
    var kind: String // the instance's event: blocked | done | error
    var reason: String
    var pushKind: String // what the phone filters on: permission | question | done | error

    init?(_ info: [AnyHashable: Any]) {
        func str(_ key: String) -> String { info[key] as? String ?? "" }
        instance = str("instance")
        pane = str("pane")
        guard !instance.isEmpty, !pane.isEmpty else { return nil }
        agent = str("agent")
        host = str("host")
        session = str("session")
        kind = str("kind")
        reason = str("reason")
        pushKind = str("push_kind")
        if pushKind.isEmpty { // a relay before 0.6.2: the same rule as its PushKind
            pushKind = PushPayload.kind(event: kind, reason: reason)
        }
    }

    init(instance: String, pane: String, agent: String, host: String = "", session: String = "", kind: String, reason: String = "") {
        self.instance = instance
        self.pane = pane
        self.agent = agent
        self.host = host
        self.session = session
        self.kind = kind
        self.reason = reason
        pushKind = PushPayload.kind(event: kind, reason: reason)
    }

    static func kind(event: String, reason: String) -> String {
        switch event {
        case "blocked": reason.hasPrefix("permission") ? "permission" : "question"
        case "error": "error"
        default: "done"
        }
    }

    /// Wants the user: a prompt, a question, a failure.
    var wantsYou: Bool { pushKind != "done" }
    /// Can be answered with keys from the notification.
    var answerable: Bool { pushKind == "permission" || pushKind == "question" }

    /// "Home / macmini · dotfiles", as the subtitle says it.
    var place: String {
        var s = instance
        if !host.isEmpty { s += " / " + host }
        if !session.isEmpty { s += " · " + session }
        return s
    }

    /// The sidebar's glyph for the push.
    var glyph: String {
        switch pushKind {
        case "permission", "question": "●"
        case "error": "✗"
        default: "✓"
        }
    }

    /// The sidebar's words, without the glyph.
    var label: String {
        switch pushKind {
        case "permission":
            let tool = reason.hasPrefix("permission") ? String(reason.dropFirst("permission".count)).droppingPrefix(":") : ""
            return tool.isEmpty ? "needs you: perm" : "needs you: perm:" + tool
        case "question": return reason.isEmpty ? "needs you" : "needs you: " + reason
        case "error": return "failed"
        default: return "done"
        }
    }

    /// The service extension's thumbnail.
    var tileName: String { wantsYou ? "tile-attention" : "tile-done" }
}

private extension String {
    func droppingPrefix(_ p: String) -> String { hasPrefix(p) ? String(dropFirst(p.count)) : self }
}

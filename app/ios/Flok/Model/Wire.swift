// The relay's wire types, as the app reads them: the JSON the relay sends on /api/ws and
// /api/instances is flok's own snapshot plus a thin envelope (see app/API.md). Keys are
// snake_case on the wire; the decoder converts them.
import Foundation

enum Wire {
    static let decoder: JSONDecoder = {
        let d = JSONDecoder()
        d.keyDecodingStrategy = .convertFromSnakeCase
        d.dateDecodingStrategy = .custom { decoder in
            let s = try decoder.singleValueContainer().decode(String.self)
            if let date = Wire.parseDate(s) { return date }
            throw DecodingError.dataCorrupted(.init(codingPath: decoder.codingPath, debugDescription: "not a date: \(s)"))
        }
        return d
    }()

    static let encoder: JSONEncoder = {
        let e = JSONEncoder()
        e.keyEncodingStrategy = .convertToSnakeCase
        return e
    }()

    nonisolated(unsafe) private static let withFraction: ISO8601DateFormatter = {
        let f = ISO8601DateFormatter()
        f.formatOptions = [.withInternetDateTime, .withFractionalSeconds]
        return f
    }()
    nonisolated(unsafe) private static let plain: ISO8601DateFormatter = {
        let f = ISO8601DateFormatter()
        f.formatOptions = [.withInternetDateTime]
        return f
    }()

    /// RFC 3339 as Go writes it: fractional seconds and a zone offset, or neither.
    static func parseDate(_ s: String) -> Date? {
        withFraction.date(from: s) ?? plain.date(from: s)
    }
}

/// One message on the app's WebSocket.
struct Frame: Decodable, Sendable {
    var type: String
    var id: String?
    var instance: String?
    var instances: [InstanceState]?
    var inst: InstanceState?
    var event: AgentEvent?
    var screen: Screen?
    var error: String?
}

/// What the relay knows of one flok: name, whether its link is up, its last snapshot.
struct InstanceState: Decodable, Sendable, Identifiable {
    var name: String
    var online: Bool
    var since: Date
    var version: String?
    var hostname: String?
    var snap: Snapshot?

    var id: String { name }
    var agents: [Agent] { snap?.agents ?? [] }
    var unseen: Int { snap?.unseen ?? 0 }
}

/// The sidebar's published view (snapshot.json), the parts the app shows.
struct Snapshot: Decodable, Sendable {
    var updatedAt: Date?
    var focus: Focus?
    var agents: [Agent]?
    var sessions: [Session]?
    var unseen: Int?
    var hosts: [Host]?
    var frontHost: String?
    var keepAwake: Bool?
}

struct Focus: Decodable, Sendable {
    var host: String?
    var sessionName: String?
    var paneId: String?
}

struct Session: Decodable, Sendable, Identifiable {
    var id: String
    var host: String?
    var name: String
    var rollup: String?
    var agents: Int
}

struct Host: Decodable, Sendable, Identifiable {
    var name: String
    var mode: String
    var state: String
    var detail: String?
    var agents: Int
    var pending: Int
    var front: Bool

    var id: String { name }
}

/// One agent pane.
struct Agent: Decodable, Sendable, Identifiable, Hashable {
    var paneId: String
    var host: String?
    var sessionId: String?
    var sessionName: String?
    var windowId: String?
    var name: String
    var kind: String?
    var state: AgentState
    var reason: String?
    var tool: String?
    var detail: String?
    var unseen: Int
    var stateSince: Date?
    var hasHooks: Bool?

    /// The pane ref the relay addresses: "%12" here, "beta:%12" on the instance's remote host.
    var paneRef: String { PaneRef.compose(host: host, pane: paneId) }
    var id: String { paneRef }
}

enum AgentState: String, Decodable, Sendable {
    case working, blocked, paused, done, idle, unknown

    init(from decoder: Decoder) throws {
        let raw = try decoder.singleValueContainer().decode(String.self)
        self = AgentState(rawValue: raw) ?? .unknown
    }

    /// Attention order, as the sidebar's priority sort: who needs you first.
    var priority: Int {
        switch self {
        case .blocked: 0
        case .done: 1
        case .working: 2
        case .paused: 3
        case .idle: 4
        case .unknown: 5
        }
    }
}

/// A transition the instance announced: an agent needs the user, finished, or failed.
struct AgentEvent: Decodable, Sendable, Identifiable {
    var pane: String
    var host: String?
    var agent: String
    var kind: String
    var reason: String?
    var at: Date

    var id: String { "\(pane) \(at.timeIntervalSince1970)" }
}

struct Screen: Decodable, Sendable {
    var pane: String
    var text: String
}

enum PaneRef {
    /// "%12" for the instance's own tmux, "beta:%12" for a pane on its remote host beta.
    static func compose(host: String?, pane: String) -> String {
        guard let host, !host.isEmpty else { return pane }
        return "\(host):\(pane)"
    }
}

/// What the app sends: an answer, a seen mark, a subscription (JSON bodies, snake_case).
struct AnswerBody: Encodable, Sendable {
    var instance: String
    var pane: String
    var text: String?
    var keys: [String]?
}

struct SeenBody: Encodable, Sendable {
    var instance: String
    var pane: String
}

struct SubscribeFrame: Encodable, Sendable {
    var type = "subscribe"
    var instance: String
    var pane: String
    var on: Bool
}

struct DeviceBody: Encodable, Sendable {
    var token: String
    var name: String
    var sandbox: Bool
}

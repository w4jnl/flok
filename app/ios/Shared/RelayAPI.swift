// The relay's HTTP API (app/API.md) as the app and its notification content extension call it:
// one bearer device token, JSON in and out, the relay's {"error": …} turned into a RelayError.
import Foundation

enum RelayError: LocalizedError {
    case notConfigured
    case http(Int, String)
    case offline

    var errorDescription: String? {
        switch self {
        case .notConfigured: "set the relay and the device token first"
        case .http(let code, let msg):
            if isMissingRoute { "the relay is older than this app: update flok-relay" } else { msg.isEmpty ? "relay answered \(code)" : msg }
        case .offline: "not connected"
        }
    }

    /// A call the relay has no route for (its mux answers without a JSON error): an older relay.
    var isMissingRoute: Bool {
        if case .http(let code, let msg) = self { return (code == 404 || code == 405) && msg.isEmpty }
        return false
    }
}

struct RelayAPI: Sendable {
    let base: URL
    let token: String

    init?(relayURL: String, token: String) {
        guard let base = RelayURL.base(relayURL), !token.isEmpty else { return nil }
        self.base = base
        self.token = token
    }

    /// What the app saved in the shared keychain; nil before it was set up (or for a build
    /// whose signing has no shared access group).
    static func fromKeychain() -> RelayAPI? {
        RelayAPI(relayURL: Keychain.get("relayURL") ?? "", token: Keychain.get("deviceToken") ?? "")
    }

    @discardableResult
    func send(_ method: String, _ path: String, query: [URLQueryItem] = [], body: Data? = nil, session: URLSession = .shared) async throws -> Data {
        var url = RelayURL.api(base, path)
        if !query.isEmpty {
            url.append(queryItems: query)
        }
        var req = URLRequest(url: url, timeoutInterval: 15)
        req.httpMethod = method
        req.setValue("Bearer \(token)", forHTTPHeaderField: "Authorization")
        if let body {
            req.setValue("application/json", forHTTPHeaderField: "Content-Type")
            req.httpBody = body
        }
        let (data, resp) = try await session.data(for: req)
        let code = (resp as? HTTPURLResponse)?.statusCode ?? 0
        if code != 200 {
            let msg = (try? JSONDecoder().decode([String: String].self, from: data))?["error"] ?? ""
            throw RelayError.http(code, msg)
        }
        return data
    }

    /// One look at a pane's screen (GET /api/screen).
    func screen(instance: String, pane: String) async throws -> String {
        let data = try await send("GET", "/api/screen", query: [URLQueryItem(name: "instance", value: instance), URLQueryItem(name: "pane", value: pane)])
        struct Out: Decodable { let text: String }
        return try JSONDecoder().decode(Out.self, from: data).text
    }

    /// Types into an agent pane: text first, then the keys; the relay waits for the instance.
    func answer(instance: String, pane: String, text: String? = nil, keys: [String]? = nil) async throws {
        var body: [String: AnyEncodable] = ["instance": .init(instance), "pane": .init(pane)]
        if let text, !text.isEmpty { body["text"] = .init(text) }
        if let keys, !keys.isEmpty { body["keys"] = .init(keys) }
        try await send("POST", "/api/answer", body: JSONEncoder().encode(body))
    }
}

/// A value of any encodable type, for a small JSON body put together by hand.
struct AnyEncodable: Encodable, Sendable {
    private let encodeValue: @Sendable (Encoder) throws -> Void
    init<T: Encodable & Sendable>(_ value: T) { encodeValue = { try value.encode(to: $0) } }
    func encode(to encoder: Encoder) throws { try encodeValue(encoder) }
}

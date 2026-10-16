// Pairing this phone with the relay: the code `flok phone pair` shows on a Mac (typed, or
// scanned from its QR as a flok://pair link) is redeemed once at POST /api/pair, the one call
// without a token, for a key of this phone's own. The key then lives in the keychain where a
// typed device token did, so the extensions and the rest of the app see no difference.
import Foundation

struct PairBody: Encodable, Sendable {
    var code: String
    var name: String
}

struct PairResponse: Decodable, Sendable {
    var token: String
    var phone: PairedPhone
    var relay: RelayInfo?
}

struct PairedPhone: Decodable, Sendable, Equatable {
    var id: String
    var name: String
}

struct RelayInfo: Decodable, Sendable {
    var version: String?
}

/// A pairing code as the relay makes it: eight letters and digits (no 0/O, 1/I), shown as
/// XXXX-XXXX; typed in any case, with or without the dash.
enum PairCode {
    static let length = 8

    static func normalize(_ s: String) -> String {
        s.uppercased().filter { !" -\u{2010}\u{2011}\u{2013}".contains($0) }
    }

    /// The code with its dash after four characters, as it is shown and typed.
    static func format(_ s: String) -> String {
        let n = normalize(s)
        guard n.count > 4 else { return n }
        let i = n.index(n.startIndex, offsetBy: 4)
        return String(n[..<i]) + "-" + String(n[i...])
    }

    static func isComplete(_ s: String) -> Bool { normalize(s).count == length }
}

extension FlokLink {
    /// A pairing link, flok://pair?code=…&relay=…: what the QR from `flok phone pair` holds. The
    /// relay comes percent-encoded; URLComponents undoes that. Nil for anything that is not a
    /// relay address with a whole code.
    static func pairing(_ url: URL) -> (relay: String, code: String)? {
        guard url.scheme == "flok", url.host == "pair", let items = URLComponents(url: url, resolvingAgainstBaseURL: false)?.queryItems,
              let relay = items.first(where: { $0.name == "relay" })?.value, let code = items.first(where: { $0.name == "code" })?.value,
              RelayURL.base(relay) != nil, PairCode.isComplete(code) else { return nil }
        return (relay, PairCode.format(code))
    }
}

extension RelayAPI {
    /// Redeems a pairing code for this phone's key. There is no token yet, so this does not go
    /// through send; a 429 (too many wrong codes from this address) names its wait.
    static func pair(relay base: URL, code: String, name: String, session: URLSession = .shared) async throws -> PairResponse {
        var req = URLRequest(url: RelayURL.api(base, "/api/pair"), timeoutInterval: 15)
        req.httpMethod = "POST"
        req.setValue("application/json", forHTTPHeaderField: "Content-Type")
        req.httpBody = try Wire.encoder.encode(PairBody(code: PairCode.normalize(code), name: name))
        let (data, resp) = try await session.data(for: req)
        let http = resp as? HTTPURLResponse
        let status = http?.statusCode ?? 0
        guard (200...299).contains(status) else {
            var msg = (try? JSONDecoder().decode([String: String].self, from: data))?["error"] ?? ""
            if status == 429, msg.isEmpty, let retry = http?.value(forHTTPHeaderField: "Retry-After") {
                msg = "too many attempts: try again in \(retry) s"
            }
            throw RelayError.http(status, msg)
        }
        return try Wire.decoder.decode(PairResponse.self, from: data)
    }
}

// The words a recogniser should expect from someone talking to flok's agents: the names the
// app already holds (instances, hosts, sessions, agents) and the identifiers on the agent's
// screen (file names, commands, flags), which a general speech model would otherwise spell as
// it hears them ("relay dot go"). Fed to the engine as contextual strings and to the clean-up
// model as the spellings to prefer.
import Foundation

enum Vocabulary {
    /// The strongest names first (the app's own), then what the screen shows by how often it
    /// shows it; at most `limit` entries, each unique regardless of case.
    static func build(instances: [InstanceState], screen: String?, limit: Int = 100) -> [String] {
        var seen = Set<String>()
        var out: [String] = []
        func add(_ word: String) {
            let w = word.trimmingCharacters(in: .whitespacesAndNewlines)
            guard w.count >= 2, w.count <= 48, seen.insert(w.lowercased()).inserted else { return }
            out.append(w)
        }
        for inst in instances {
            add(inst.name)
            for a in inst.agents {
                add(a.name)
                if let h = a.host { add(h) }
                if let s = a.sessionName { add(s) }
            }
        }
        if let screen {
            for (word, _) in identifiers(in: screen) {
                add(word)
            }
        }
        if out.count > limit {
            out.removeLast(out.count - limit)
        }
        return out
    }

    /// The tokens of a screen that look like code rather than prose (a dot, an underscore, a
    /// slash, a dash inside, camelCase, or a digit next to letters), by count, the most frequent
    /// first. Prose words, numbers, box drawing and URLs' noise are left out.
    static func identifiers(in text: String) -> [(String, Int)] {
        var counts: [String: Int] = [:]
        var order: [String] = []
        for raw in text.split(whereSeparator: { $0.isWhitespace || "()[]{}<>\"'`,;:!?…│─┃━╭╮╰╯├┤┬┴┼║═╔╗╚╝⎿".contains($0) }) {
            var token = String(raw)
            while let last = token.last, ".-_/".contains(last) { token.removeLast() } // a sentence's full stop, a trailing slash
            while let first = token.first, ".-/@#$~".contains(first) && token.count > 1 && !token.hasPrefix("./") && !token.hasPrefix("--") && !token.hasPrefix("-") { token.removeFirst() }
            guard looksLikeCode(token) else { continue }
            if counts[token] == nil { order.append(token) }
            counts[token, default: 0] += 1
        }
        return order.map { ($0, counts[$0]!) }.sorted { a, b in a.1 != b.1 ? a.1 > b.1 : false }
    }

    /// A token that a speech model would not know how to spell.
    static func looksLikeCode(_ t: String) -> Bool {
        guard t.count >= 2, t.count <= 48, t.unicodeScalars.allSatisfy({ $0.isASCII && !$0.properties.isWhitespace }) else { return false }
        if t.hasPrefix("-"), !t.hasPrefix("--"), t.count <= 6, t.dropFirst().allSatisfy(\.isLetter) { return true } // -v, -run
        guard t.count >= 3, !t.allSatisfy({ $0.isNumber || $0 == "." || $0 == "%" }) else { return false }
        if t.hasPrefix("http://") || t.hasPrefix("https://") { return false }
        let letters = t.filter(\.isLetter)
        guard let first = t.first, letters.count >= 1 else { return false }
        if t.contains(where: \.isNumber) && first.isLetter { return true } // m11, tmux3, p256, x86_64 (12s and 1.2k start with a digit)
        guard letters.count >= 2 else { return false }
        let inner = t.dropFirst().dropLast()
        if inner.contains(where: { "._/-".contains($0) }) { return true } // relay.go, flok-relay, internal/ui, snapshot_json
        if t.hasPrefix("--") { return true } // --no-focus
        if t.contains(where: \.isUppercase) && t.contains(where: \.isLowercase) && first.isLowercase { return true } // camelCase
        if t.contains(where: \.isUppercase) && t.contains(where: \.isLowercase) && t.dropFirst().contains(where: \.isUppercase) { return true } // TypeAnswer
        return false
    }
}

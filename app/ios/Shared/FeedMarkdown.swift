import Foundation

/// How an agent's text reads on the phone. It is markdown as Claude writes it; SwiftUI renders
/// the inline part (bold, code, links) but not the blocks, so the text is split into blocks
/// first: paragraphs, headings, list items, fenced code, quotes, rules and tables (kept whole
/// in a monospaced strip). Pure, so it is tested without a view.
enum FeedMarkdown {
    enum Kind: Equatable {
        case paragraph
        case heading(Int)
        case bullet(indent: Int) // an unordered item; the marker is drawn by the view
        case numbered(indent: Int, label: String) // "1." as written
        case code(lang: String)
        case quote
        case rule
        case table
    }

    struct Block: Identifiable, Equatable {
        let id: Int
        let kind: Kind
        var lines: [String]
        /// The text as one string: lines of a paragraph or an item joined with spaces (a soft
        /// break), of code or a table with newlines.
        var text: String {
            switch kind {
            case .code, .table: return lines.joined(separator: "\n")
            default: return lines.joined(separator: " ")
            }
        }
    }

    static func blocks(of text: String) -> [Block] {
        var out: [Block] = []
        var cur: Block?
        var inCode = false
        func close() {
            if let b = cur, !b.lines.isEmpty || b.kind.standsAlone { out.append(b) }
            cur = nil
        }
        func open(_ kind: Kind, _ line: String?) {
            close()
            cur = Block(id: out.count, kind: kind, lines: line.map { [$0] } ?? [])
        }
        for raw in text.split(separator: "\n", omittingEmptySubsequences: false) {
            let line = String(raw)
            let trimmed = line.trimmingCharacters(in: .whitespaces)
            if inCode {
                if trimmed.hasPrefix("```") { inCode = false; close() } else { cur?.lines.append(line) }
                continue
            }
            if trimmed.hasPrefix("```") {
                inCode = true
                open(.code(lang: String(trimmed.dropFirst(3)).trimmingCharacters(in: .whitespaces)), nil)
                continue
            }
            if trimmed.isEmpty { close(); continue }
            if let h = heading(trimmed) { open(.heading(h.level), h.text); close(); continue }
            if isRule(trimmed) { open(.rule, nil); close(); continue }
            if trimmed.hasPrefix("|") {
                if case .table = cur?.kind { cur?.lines.append(trimmed) } else { open(.table, trimmed) }
                continue
            }
            if trimmed.hasPrefix(">") {
                let body = String(trimmed.dropFirst()).trimmingCharacters(in: .whitespaces)
                if case .quote = cur?.kind { cur?.lines.append(body) } else { open(.quote, body) }
                continue
            }
            let indent = (line.count - line.drop(while: { $0 == " " }).count) / 2
            if let item = bullet(trimmed) { open(.bullet(indent: indent), item); continue }
            if let (label, item) = numbered(trimmed) { open(.numbered(indent: indent, label: label), item); continue }
            switch cur?.kind {
            case .paragraph?, .bullet?, .numbered?: cur?.lines.append(trimmed) // a continuation line
            default: open(.paragraph, trimmed)
            }
        }
        close()
        return out
    }

    private static func heading(_ s: String) -> (level: Int, text: String)? {
        var level = 0
        var rest = Substring(s)
        while rest.first == "#", level < 6 { level += 1; rest = rest.dropFirst() }
        guard level > 0, rest.first == " " else { return nil }
        return (level, rest.trimmingCharacters(in: .whitespaces))
    }

    private static func isRule(_ s: String) -> Bool {
        s.count >= 3 && (Set(s) == ["-"] || Set(s) == ["*"] || Set(s) == ["_"])
    }

    private static func bullet(_ s: String) -> String? {
        for m in ["- ", "* ", "+ ", "• "] where s.hasPrefix(m) { return String(s.dropFirst(m.count)) }
        return nil
    }

    private static func numbered(_ s: String) -> (String, String)? {
        var digits = Substring(s).prefix(while: \.isNumber)
        guard !digits.isEmpty, digits.count <= 3 else { return nil }
        var rest = s.dropFirst(digits.count)
        guard rest.first == "." || rest.first == ")" else { return nil }
        digits.append(rest.removeFirst())
        guard rest.first == " " else { return nil }
        return (String(digits), rest.trimmingCharacters(in: .whitespaces))
    }

    /// The inline markdown of one line or paragraph, for `Text`; what does not parse is shown
    /// as written.
    static func inline(_ s: String) -> AttributedString {
        (try? AttributedString(markdown: s, options: .init(interpretedSyntax: .inlineOnlyPreservingWhitespace))) ?? AttributedString(s)
    }
}

extension FeedMarkdown.Kind {
    /// A block that is kept without lines: an empty code fence, a rule.
    var standsAlone: Bool {
        switch self {
        case .code, .rule: return true
        default: return false
        }
    }
}

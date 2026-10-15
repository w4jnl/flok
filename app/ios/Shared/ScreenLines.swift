import Foundation

/// How a captured screen reads when long lines wrap. The pane on the Mac is wider than the
/// phone, so prose wraps at words, which reads well; but a rule (Claude Code's dividers, the
/// frame of its prompt, a `=====` underline) is as wide as the pane and folds into two or three
/// rows, and a table row folds so its right border lands on a row of its own. So lines are
/// sorted: `prose` wraps, a `rule` is cut to the screen's width (its label kept), and `boxed`
/// lines (table rows, box edges, `|` columns) keep their shape in a block that scrolls sideways.
enum ScreenLines {
    enum Kind: Equatable { case prose, rule, boxed }

    /// A run of consecutive lines of one kind, in screen order; `id` is the first line's index.
    struct Block: Identifiable, Equatable {
        let id: Int
        let kind: Kind
        var lines: [String]
        var text: String { lines.joined(separator: "\n") }
    }

    /// Horizontal rule characters: box drawing, the ASCII stand-ins, and the block elements a
    /// TUI draws a thick edge with (`▀▀▀` over a box, `▁▁▁` under it, `███` as a bar).
    static let ruleChars: Set<Character> = ["─", "━", "═", "╌", "╍", "┄", "┅", "┈", "┉", "-", "=", "_", "⎯", "―", "—",
                                            "▀", "▄", "█", "▔", "▁", "▂", "▃", "▅", "▆", "▇", "▬"]
    /// Vertical edges, corners and tees: a table row or a box edge has two or more of them.
    static let boxChars: Set<Character> = ["│", "┃", "║", "┆", "┇", "┊", "┋", "╎", "╏",
                                           "┌", "┐", "└", "┘", "├", "┤", "┬", "┴", "┼", "╭", "╮", "╰", "╯",
                                           "╔", "╗", "╚", "╝", "╠", "╣", "╦", "╩", "╬", "╒", "╕", "╘", "╛", "╞", "╡", "╤", "╧", "╪"]

    /// Vertical bars: the right edge of a box wider than the phone, when one is a line's only bar.
    static let edgeChars: Set<Character> = ["│", "┃", "║", "┆", "┇", "┊", "┋", "╎", "╏"]

    /// The line without its trailing blanks and without the right edge of a box that is wider
    /// than the phone: a bar that is the line's only box character, at its end after a blank,
    /// would land on a row of its own once the text wraps, and a line that is nothing but a bar
    /// is that edge on an empty row (Copilot frames its messages so). A bar at the start (a
    /// gutter), one glued to text, or a line with two (a table row) stays.
    static func unedged(_ line: String) -> String {
        var l = Substring(line)
        while l.last == " " { l.removeLast() }
        guard let last = l.last, edgeChars.contains(last) else { return String(l) }
        var before = l.dropLast()
        guard before.isEmpty || before.last == " ", !before.contains(where: { boxChars.contains($0) }) else { return String(l) }
        while before.last == " " { before.removeLast() }
        return String(before)
    }

    /// The kind of one line. A box edge like `┌────┐` is boxed (it belongs with its rows), a
    /// bare divider like `────` or `── name ─` is a rule, a single `│` or `|` in a line of text
    /// (a quote gutter, a shell pipe) leaves it prose.
    static func kind(of line: String) -> Kind {
        let trimmed = line.trimmingCharacters(in: .whitespaces)
        if trimmed.isEmpty { return .prose }
        var rule = 0, box = 0, bars = 0, pluses = 0, run = 0, longest = 0
        for ch in trimmed {
            if ruleChars.contains(ch) {
                rule += 1
                run += 1
                longest = max(longest, run)
            } else {
                run = 0
            }
            if boxChars.contains(ch) { box += 1 }
            if ch == "|" { bars += 1 }
            if ch == "+" { pluses += 1 }
        }
        if box >= 2 || bars >= 2 || (pluses >= 2 && longest >= 3) { return .boxed }
        if longest >= 4 && rule * 10 >= trimmed.count * 6 { return .rule }
        return .prose
    }

    /// The screen as blocks of consecutive lines of one kind, each line `unedged`.
    static func blocks(of text: String) -> [Block] {
        var out: [Block] = []
        for (i, raw) in text.components(separatedBy: "\n").enumerated() {
            let line = unedged(raw)
            let k = kind(of: line)
            if let last = out.indices.last, out[last].kind == k {
                out[last].lines.append(line)
            } else {
                out.append(Block(id: i, kind: k, lines: [line]))
            }
        }
        return out
    }

    /// A rule cut to `columns` characters: the characters come out of its longest run of rule
    /// characters, so `──── name ─` keeps its name; a rule whose label alone is too wide is cut
    /// at the end.
    static func fit(_ line: String, columns: Int) -> String {
        let chars = Array(line)
        let excess = chars.count - max(columns, 1)
        if excess <= 0 { return line }
        var best = (start: 0, length: 0), start = 0, length = 0
        for (i, ch) in chars.enumerated() {
            if ruleChars.contains(ch) {
                if length == 0 { start = i }
                length += 1
                if length > best.length { best = (start, length) }
            } else {
                length = 0
            }
        }
        if best.length > excess {
            let keep = best.length - excess
            return String(chars[..<best.start]) + String(chars[best.start..<best.start + keep]) + String(chars[(best.start + best.length)...])
        }
        return String(chars.prefix(max(columns, 1)))
    }

    /// The widest line that is not a rule, in characters: what a "fit the screen" zoom should
    /// measure, since a rule is as wide as the pane whatever the text is.
    static func widest(of text: String) -> Int {
        text.components(separatedBy: "\n").map(unedged).filter { kind(of: $0) != .rule }.map(\.count).max() ?? 0
    }
}

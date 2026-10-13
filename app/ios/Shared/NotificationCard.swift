// The expanded notification (long press), in the brand's dress: the terminal ground, JetBrains
// Mono, the state colour and glyph, the tail of the agent's screen as the relay sees it now,
// and the keys to answer a prompt or a question without opening the app. The content extension
// hosts it; it lives here so the app's tests can render it.
import SwiftUI

enum CardScreen: Equatable, Sendable {
    case loading
    case text(String)
    case unavailable(String)
}

struct CardKey: Identifiable, Sendable {
    let label: String
    let text: String?
    let keys: [String]?
    var id: String { label }

    static let all: [CardKey] = [
        CardKey(label: "1", text: "1", keys: nil), CardKey(label: "2", text: "2", keys: nil), CardKey(label: "3", text: "3", keys: nil),
        CardKey(label: "y", text: "y", keys: nil), CardKey(label: "n", text: "n", keys: nil),
        CardKey(label: "↵", text: nil, keys: ["Enter"]), CardKey(label: "esc", text: nil, keys: ["Escape"]),
    ]
}

struct NotificationCard: View {
    let push: PushPayload
    var screen: CardScreen = .loading
    var status: String?
    var tailLines = 12
    var onKey: (CardKey) -> Void = { _ in }

    private var accent: Color { push.wantsYou ? Brand.blocked : Brand.done }

    var body: some View {
        VStack(alignment: .leading, spacing: 10) {
            HStack(alignment: .firstTextBaseline, spacing: 8) {
                Text(push.glyph).font(Brand.mono(16, bold: true)).foregroundStyle(accent)
                VStack(alignment: .leading, spacing: 3) {
                    Text(push.agent.isEmpty ? push.pane : push.agent).font(Brand.mono(15, bold: true)).foregroundStyle(Brand.ink)
                    Text(push.place).font(Brand.mono(11)).foregroundStyle(Brand.idle)
                }
                .lineLimit(1)
                Spacer(minLength: 8)
                Brand.wordmark(12)
            }
            Text(push.label).font(Brand.mono(13, bold: true)).foregroundStyle(accent).lineLimit(2)
            screenPanel
            if push.answerable {
                HStack(spacing: 6) {
                    ForEach(CardKey.all) { k in
                        Button { onKey(k) } label: {
                            Text(k.label).font(Brand.mono(14, bold: true)).foregroundStyle(Brand.ink)
                                .frame(maxWidth: .infinity, minHeight: 34)
                                .background(Brand.raised, in: RoundedRectangle(cornerRadius: 7))
                                .overlay(RoundedRectangle(cornerRadius: 7).strokeBorder(Brand.working.opacity(0.45), lineWidth: 1))
                        }
                        .buttonStyle(.plain)
                        .accessibilityLabel(k.keys?.first ?? k.text ?? k.label)
                    }
                }
            }
            if let status {
                Text(status).font(Brand.mono(11)).foregroundStyle(Brand.idle)
            }
        }
        .padding(14)
        .frame(maxWidth: .infinity, alignment: .leading)
        .background(Brand.ground)
        .environment(\.colorScheme, .dark)
    }

    @ViewBuilder private var screenPanel: some View {
        Group {
            switch screen {
            case .loading:
                Text("looking at the screen…").foregroundStyle(Brand.idle)
            case .unavailable(let why):
                Text(why).foregroundStyle(Brand.idle)
            case .text(let s):
                Text(NotificationCard.tail(s, lines: tailLines)).foregroundStyle(Brand.ink.opacity(0.92))
            }
        }
        .font(Brand.mono(10))
        .lineLimit(tailLines)
        .frame(maxWidth: .infinity, alignment: .leading)
        .padding(10)
        .background(Brand.raised, in: RoundedRectangle(cornerRadius: 8))
    }

    /// The last lines of a screen that hold anything, right-trimmed, so a prompt at the bottom
    /// of a mostly empty pane is what shows.
    nonisolated static func tail(_ text: String, lines n: Int) -> String {
        var lines = text.components(separatedBy: "\n").map { line in
            var l = Substring(line)
            while l.last == " " { l.removeLast() }
            return String(l)
        }
        while let last = lines.last, last.isEmpty { lines.removeLast() }
        return lines.suffix(n).joined(separator: "\n")
    }
}

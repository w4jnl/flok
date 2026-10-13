// The sidebar's glyphs and colours, as the phone shows them: the same meaning in both themes.
import SwiftUI
import UIKit

enum StateStyle {
    static func glyph(_ s: AgentState) -> String {
        switch s {
        case .working: "◐" // StateGlyph turns it
        case .blocked: "●"
        case .done: "✓"
        case .paused: "◍"
        case .idle: "○"
        case .unknown: "~"
        }
    }

    static func color(_ s: AgentState) -> Color {
        switch s {
        case .working: Palette.cyan
        case .blocked: Palette.orange
        case .done: Palette.green
        case .paused, .idle, .unknown: Palette.comment
        }
    }

    static func label(_ a: Agent) -> String {
        switch a.state {
        case .blocked:
            if let r = a.reason, r.hasPrefix("permission:") { return "needs you: perm:" + r.dropFirst("permission:".count) }
            if let r = a.reason, !r.isEmpty { return "needs you: " + r }
            return "needs you"
        case .done: return a.reason == "error" ? "failed" : "done"
        case .working:
            if let t = a.tool, !t.isEmpty { return "working · " + t }
            return "working"
        case .paused:
            if let d = a.detail, !d.isEmpty { return "paused · " + d }
            return "paused"
        case .idle: return "idle"
        case .unknown: return ""
        }
    }
}

/// A state's glyph; a working agent's turns like the sidebar's spinner (◐ ◓ ◑ ◒, a quarter every
/// 250 ms, the sidebar's spinner_ms default), and stands still with Reduce Motion.
struct StateGlyph: View {
    let state: AgentState
    @Environment(\.accessibilityReduceMotion) private var reduceMotion

    static let frames = ["◐", "◓", "◑", "◒"]
    static let frameTime = 0.25

    var body: some View {
        if state == .working && !reduceMotion {
            TimelineView(.periodic(from: .now, by: Self.frameTime)) { ctx in
                Text(Self.frame(at: ctx.date))
            }
        } else {
            Text(StateStyle.glyph(state))
        }
    }

    static func frame(at date: Date) -> String {
        frames[Int(date.timeIntervalSinceReferenceDate / frameTime) % frames.count]
    }
}

/// Dracula, with darker twins for a light screen.
enum Palette {
    static let cyan = dyn(light: (0.00, 0.56, 0.62), dark: (0.545, 0.914, 0.992))
    static let orange = dyn(light: (0.80, 0.42, 0.00), dark: (1.0, 0.722, 0.424))
    static let green = dyn(light: (0.09, 0.56, 0.24), dark: (0.314, 0.98, 0.482))
    static let purple = dyn(light: (0.45, 0.30, 0.80), dark: (0.741, 0.576, 0.976))
    static let comment = dyn(light: (0.45, 0.47, 0.55), dark: (0.384, 0.447, 0.643))

    private static func dyn(light: (Double, Double, Double), dark: (Double, Double, Double)) -> Color {
        Color(UIColor { tc in
            let c = tc.userInterfaceStyle == .dark ? dark : light
            return UIColor(red: c.0, green: c.1, blue: c.2, alpha: 1)
        })
    }
}

func elapsed(since: Date?, now: Date = .now) -> String {
    guard let since else { return "" }
    let s = max(0, Int(now.timeIntervalSince(since)))
    if s >= 3600 { return String(format: "%d:%02d:%02d", s / 3600, s % 3600 / 60, s % 60) }
    return String(format: "%d:%02d", s / 60, s % 60)
}

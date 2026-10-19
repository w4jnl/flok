// The Live Activity's faces, in the brand's dress (assets/brand/README.md): the Lock Screen view
// on the terminal ground, and the Dynamic Island's compact, minimal and expanded parts. The
// widget extension puts them in its ActivityConfiguration; they live here so the app's tests
// can render them. Elapsed times are SwiftUI timers: the relay pushes state changes only.
import SwiftUI
import UIKit

enum LiveStyle {
    static func color(_ state: String) -> Color {
        switch state {
        case "working": Brand.working
        case "blocked": Brand.blocked
        case "done": Brand.done
        default: Brand.idle
        }
    }

    static func glyph(_ state: String) -> String {
        switch state {
        case "working": "◐"
        case "blocked": "●"
        case "done": "✓"
        case "gone": "◌"
        default: "○"
        }
    }

    /// The flok mark (the menu bar's template icon), tinted.
    static func mark(_ color: Color, size: CGFloat) -> some View {
        Image(uiImage: UIImage(named: "flok-mark") ?? UIImage())
            .renderingMode(.template)
            .resizable()
            .scaledToFit()
            .frame(width: size, height: size)
            .foregroundStyle(color)
    }
}

/// "◐ 2  ● 1  ✓ 1", the counts that are not zero.
struct LiveCounts: View {
    let state: FlokActivityAttributes.ContentState
    var size: CGFloat = 13

    var body: some View {
        HStack(spacing: 8) {
            if state.working > 0 { count("working", state.working) }
            if state.waiting > 0 { count("blocked", state.waiting) }
            if state.done > 0 { count("done", state.done) }
        }
    }

    private func count(_ s: String, _ n: Int) -> some View {
        Text("\(LiveStyle.glyph(s)) \(n)").font(Brand.mono(size, bold: true)).foregroundStyle(LiveStyle.color(s))
    }
}

/// One agent: glyph, name, where, the sidebar's words, and how long it has been so.
struct LiveRowView: View {
    let row: LiveRow
    var compact = false

    var body: some View {
        HStack(alignment: .firstTextBaseline, spacing: 8) {
            Text(LiveStyle.glyph(row.state)).font(Brand.mono(compact ? 12 : 14, bold: true)).foregroundStyle(LiveStyle.color(row.state))
            VStack(alignment: .leading, spacing: 1) {
                HStack(spacing: 6) {
                    Text(row.agent.isEmpty ? row.pane : row.agent).font(Brand.mono(compact ? 12 : 14, bold: true)).foregroundStyle(Brand.ink)
                    Text(row.place).font(Brand.mono(compact ? 9 : 10)).foregroundStyle(Brand.idle)
                }
                .hiddenWhenLocked()
                if !compact {
                    Text(row.label).font(Brand.mono(11)).foregroundStyle(LiveStyle.color(row.state)).hiddenWhenLocked()
                }
            }
            .lineLimit(1)
            Spacer(minLength: 4)
            if let t = row.started, row.state == "working" || row.state == "blocked" {
                Text(t, style: .timer).font(Brand.mono(compact ? 11 : 12)).monospacedDigit().foregroundStyle(Brand.idle)
                    .multilineTextAlignment(.trailing).frame(maxWidth: 64, alignment: .trailing)
            }
        }
    }
}

/// The Lock Screen (and the banner on a phone without the island).
struct LiveLockScreen: View {
    let attributes: FlokActivityAttributes
    let state: FlokActivityAttributes.ContentState

    var body: some View {
        VStack(alignment: .leading, spacing: 8) {
            HStack(spacing: 8) {
                LiveStyle.mark(LiveStyle.color(state.dominant == "idle" ? "working" : state.dominant), size: 18)
                Brand.wordmark(13)
                Spacer()
                if attributes.kind == "overview" { LiveCounts(state: state) }
            }
            if state.rows.isEmpty {
                Text("all quiet").font(Brand.mono(12)).foregroundStyle(Brand.idle)
            }
            ForEach(state.rows) { r in
                Link(destination: r.link) { LiveRowView(row: r) }
            }
        }
        .padding(14)
        .environment(\.colorScheme, .dark)
    }
}

// MARK: the Dynamic Island

struct LiveCompactLeading: View {
    let state: FlokActivityAttributes.ContentState
    var body: some View { LiveStyle.mark(LiveStyle.color(state.dominant == "idle" ? "working" : state.dominant), size: 18) }
}

struct LiveCompactTrailing: View {
    let attributes: FlokActivityAttributes
    let state: FlokActivityAttributes.ContentState

    var body: some View {
        if attributes.kind == "agent", let r = state.rows.first {
            if let t = r.started, r.state == "working" || r.state == "blocked" {
                Text(t, style: .timer).font(Brand.mono(13, bold: true)).monospacedDigit().foregroundStyle(LiveStyle.color(r.state))
                    .multilineTextAlignment(.trailing).frame(maxWidth: 52)
            } else {
                Text(LiveStyle.glyph(r.state)).font(Brand.mono(14, bold: true)).foregroundStyle(LiveStyle.color(r.state))
            }
        } else if state.waiting > 0 {
            Text("● \(state.waiting)").font(Brand.mono(13, bold: true)).foregroundStyle(Brand.blocked)
        } else if state.working > 0 {
            Text("◐ \(state.working)").font(Brand.mono(13, bold: true)).foregroundStyle(Brand.working)
        } else {
            Text("✓ \(state.done)").font(Brand.mono(13, bold: true)).foregroundStyle(Brand.done)
        }
    }
}

struct LiveMinimal: View {
    let state: FlokActivityAttributes.ContentState
    var body: some View {
        Text(LiveStyle.glyph(state.dominant)).font(Brand.mono(14, bold: true)).foregroundStyle(LiveStyle.color(state.dominant))
    }
}

/// The expanded island's bottom: the rows, small.
struct LiveExpandedBottom: View {
    let state: FlokActivityAttributes.ContentState

    var body: some View {
        VStack(alignment: .leading, spacing: 4) {
            if state.rows.isEmpty {
                Text("all quiet").font(Brand.mono(12)).foregroundStyle(Brand.idle)
            }
            ForEach(state.rows) { r in
                Link(destination: r.link) {
                    VStack(alignment: .leading, spacing: 1) {
                        LiveRowView(row: r, compact: true)
                        if r.state == "blocked" {
                            Text(r.label).font(Brand.mono(10)).foregroundStyle(Brand.blocked).padding(.leading, 20).lineLimit(1).hiddenWhenLocked()
                        }
                    }
                }
            }
        }
        .environment(\.colorScheme, .dark)
    }
}

// One agent: its state, its live screen, and the keys to answer it.
import SwiftUI

struct AgentView: View {
    let target: Target
    @Environment(Relay.self) private var relay
    @Environment(Settings.self) private var settings
    @Environment(\.verticalSizeClass) private var vClass
    @Environment(\.dismiss) private var dismiss
    @Environment(\.scenePhase) private var phase
    @State private var input = ""
    @State private var notice: String?
    @State private var confirmInterrupt = false
    @State private var inputChoice: Bool? // the user's show/hide of the bottom; nil = the orientation's default
    @FocusState private var typing: Bool

    private var found: (InstanceState, Agent)? { relay.agent(target) }
    /// Landscape on a phone: little height, so the chrome shrinks to two thin strips.
    private var compact: Bool { vClass == .compact }
    private var inputShown: Bool { inputChoice ?? !compact }

    var body: some View {
        VStack(spacing: 0) {
            if compact {
                compactBar
            } else {
                header
            }
            Divider()
            ScreenView(text: relay.screens[target], fontSize: settings.fontSize, wrap: settings.wrapLines, onZoom: { settings.fontSize = $0 })
                .onTapGesture { typing = false } // a tap on the screen puts the keyboard away
            Divider()
            if inputShown {
                // The keys get the whole row and the controls sit in the reply row, each in its
                // own place: nothing floats over anything (the keyboard's own toolbar did, over
                // the send button, on iOS 26 and later).
                KeyBar { key in Task { await send(text: key.text, keys: key.keys) } }
                    .clipped()
                HStack(spacing: 8) {
                    Button { inputChoice = false; typing = false } label: { Image(systemName: "chevron.down").fontWeight(.semibold) }
                        .buttonStyle(.borderless)
                        .accessibilityLabel("Hide the keys and the reply field")
                    TextField("type a reply, Enter sends it", text: $input, axis: .vertical)
                        .textFieldStyle(.roundedBorder)
                        .lineLimit(1...4)
                        .autocorrectionDisabled()
                        .textInputAutocapitalization(.never)
                        .focused($typing)
                        .onSubmit { Task { await sendInput() } }
                    if typing {
                        Button { typing = false } label: { Image(systemName: "keyboard.chevron.compact.down") }
                            .buttonStyle(.bordered)
                            .accessibilityLabel("Hide the keyboard")
                    }
                    Button { Task { await sendInput() } } label: { Image(systemName: "paperplane.fill") }
                        .buttonStyle(.borderedProminent)
                        .disabled(input.isEmpty)
                }
                .padding(.horizontal, 10).padding(.vertical, compact ? 4 : 8)
            } else {
                HStack {
                    if let notice { Text(notice).font(.footnote).foregroundStyle(.secondary).lineLimit(1) }
                    Spacer()
                    Button { inputChoice = true } label: { Label("Keys", systemImage: "chevron.up").labelStyle(.iconOnly) }
                        .buttonStyle(.borderless)
                        .accessibilityLabel("Show the keys")
                }
                .padding(.horizontal, 12).padding(.vertical, 4)
            }
            if inputShown, let notice {
                Text(notice).font(.footnote).foregroundStyle(.secondary).padding(.bottom, 4)
            }
        }
        .navigationTitle(found?.1.name ?? target.pane)
        .navigationBarTitleDisplayMode(.inline)
        .toolbar(compact ? .hidden : .visible, for: .navigationBar)
        .toolbar {
            ToolbarItem(placement: .topBarTrailing) { textMenu }
            ToolbarItem(placement: .topBarTrailing) { actionMenu }
        }
        .confirmationDialog("Send Ctrl-C to \(found?.1.name ?? "the agent")?", isPresented: $confirmInterrupt, titleVisibility: .visible) {
            Button("Interrupt", role: .destructive) { Task { await send(keys: ["C-c"]) } }
        }
        .task(id: target) {
            relay.subscribe(target)
            await seenIfUnseen()
        }
        .onDisappear { relay.unsubscribe(target) }
        // Looking at the agent here is looking at it: as focusing its pane in tmux, it is marked
        // seen when it opens, when it finishes while open, and when the app comes back to it.
        .onChange(of: found?.1.unseen ?? 0) { _, n in if n > 0 { Task { await seenIfUnseen() } } }
        .onChange(of: phase) { _, p in if p == .active { Task { await seenIfUnseen() } } }
        .onChange(of: compact) { _, _ in inputChoice = nil } // a turn of the phone goes back to that orientation's default
    }

    /// Portrait: the agent's state under the navigation bar.
    @ViewBuilder private var header: some View {
        if let (inst, a) = found {
            HStack(spacing: 10) {
                StateGlyph(state: a.state).foregroundStyle(StateStyle.color(a.state)).font(.system(.title3, design: .monospaced))
                VStack(alignment: .leading, spacing: 2) {
                    Text(StateStyle.label(a)).foregroundStyle(StateStyle.color(a.state)).font(.subheadline.weight(.medium))
                    HStack(spacing: 6) {
                        Text(inst.name)
                        if let h = a.host, !h.isEmpty { Text("/ \(h)") }
                        if let s = a.sessionName, !s.isEmpty { Text("· \(s)") }
                        if let k = a.kind, !k.isEmpty { Text("· \(k)") }
                        Text("· \(a.paneId)")
                    }
                    .font(.caption).foregroundStyle(.secondary).lineLimit(1)
                }
                Spacer()
                if !inst.online {
                    Label("offline", systemImage: "wifi.slash").font(.caption).foregroundStyle(Palette.orange)
                }
            }
            .padding(.horizontal, 12).padding(.vertical, 8)
        } else {
            Text("this agent is gone from the instance's view").font(.footnote).foregroundStyle(.secondary).padding(8)
        }
    }

    /// Landscape: one thin strip with the way back, the state and the menus.
    private var compactBar: some View {
        HStack(spacing: 10) {
            Button { dismiss() } label: { Image(systemName: "chevron.left").fontWeight(.semibold) }
                .buttonStyle(.borderless)
            if let (inst, a) = found {
                StateGlyph(state: a.state).foregroundStyle(StateStyle.color(a.state)).font(.system(.body, design: .monospaced))
                Text(a.name).fontWeight(.semibold)
                Text(StateStyle.label(a)).foregroundStyle(StateStyle.color(a.state))
                Text("\(inst.name)\(a.host.map { " / " + $0 } ?? "") · \(a.paneId)").foregroundStyle(.secondary)
                if !inst.online { Label("offline", systemImage: "wifi.slash").foregroundStyle(Palette.orange) }
            } else {
                Text(target.pane).foregroundStyle(.secondary)
            }
            Spacer()
            textMenu
            actionMenu
        }
        .font(.footnote)
        .lineLimit(1)
        .padding(.horizontal, 12).padding(.vertical, 5)
    }

    private var textMenu: some View {
        Menu {
            Button { settings.fontSize = max(5, settings.fontSize - 1) } label: { Label("Smaller text", systemImage: "textformat.size.smaller") }
            Button { settings.fontSize = min(24, settings.fontSize + 1) } label: { Label("Larger text", systemImage: "textformat.size.larger") }
            Button { settings.wrapLines.toggle() } label: { Label(settings.wrapLines ? "Scroll long lines" : "Wrap long lines", systemImage: settings.wrapLines ? "arrow.left.and.right" : "text.justify.left") }
        } label: { Image(systemName: "textformat.size") }
    }

    private var actionMenu: some View {
        Menu {
            Button { Task { await send(keys: ["Enter"]) } } label: { Label("Approve (Enter)", systemImage: "checkmark") }
            Button { Task { await send(keys: ["Escape"]) } } label: { Label("Deny (Esc)", systemImage: "xmark") }
            Button { Task { await seen() } } label: { Label("Mark seen", systemImage: "eye") }
            muteMenu
            Divider()
            Button(role: .destructive) { confirmInterrupt = true } label: { Label("Interrupt (Ctrl-C)", systemImage: "stop.circle") }
        } label: { Image(systemName: "ellipsis.circle") }
    }

    /// Mutes this agent's notifications in the settings the relay filters with: matched by
    /// instance, host and name, so a restarted agent stays muted.
    @ViewBuilder private var muteMenu: some View {
        if let (inst, a) = found {
            if let m = settings.notificationPrefs.mute(instance: inst.name, host: a.host, agent: a.name) {
                Button {
                    relay.updatePrefs { $0.unmute(instance: inst.name, host: a.host, agent: a.name) }
                    flash("notifications on")
                } label: {
                    Label(m.until.map { "Unmute (muted until \($0.formatted(date: .omitted, time: .shortened)))" } ?? "Unmute", systemImage: "bell")
                }
            } else {
                Menu {
                    Button("For 1 hour") { mute(inst.name, a, until: .now.addingTimeInterval(3600)) }
                    Button("Until tomorrow 8:00") { mute(inst.name, a, until: NotificationPrefs.tomorrowMorning()) }
                    Button("Always") { mute(inst.name, a, until: nil) }
                } label: { Label("Mute notifications", systemImage: "bell.slash") }
            }
        }
    }

    private func mute(_ instance: String, _ a: Agent, until: Date?) {
        relay.updatePrefs { $0.mute(instance: instance, host: a.host, agent: a.name, until: until) }
        flash("muted")
    }

    private func sendInput() async {
        let text = input
        guard !text.isEmpty else { return }
        input = ""
        await send(text: text, keys: ["Enter"])
    }

    private func seenIfUnseen() async {
        guard phase == .active, let (_, a) = found, a.unseen > 0 else { return }
        try? await relay.markSeen(target)
    }

    private func send(text: String? = nil, keys: [String]? = nil) async {
        do {
            try await relay.answer(target, text: text, keys: keys)
            await seenIfUnseen()
            flash("sent")
        } catch {
            flash(error.localizedDescription)
        }
    }

    private func seen() async {
        do { try await relay.markSeen(target); flash("marked seen") } catch { flash(error.localizedDescription) }
    }

    private func flash(_ s: String) {
        notice = s
        Task {
            try? await Task.sleep(for: .seconds(2.5))
            if notice == s { notice = nil }
        }
    }
}

/// A pane's text, monospaced, following the bottom as it changes. Wide lines scroll sideways
/// or wrap (wrap); a pinch zooms the text, a double tap fits the widest line to the screen.
struct ScreenView: View {
    let text: String?
    let fontSize: Double
    var wrap = false
    var onZoom: (Double) -> Void = { _ in }
    @State private var waitedLong = false
    @State private var pinch: CGFloat = 1

    var body: some View {
        GeometryReader { geo in
            ScrollViewReader { proxy in
                ScrollView(wrap ? [.vertical] : [.vertical, .horizontal]) {
                    Text(trimmed)
                        .font(.system(size: fontSize * pinch, design: .monospaced))
                        .fixedSize(horizontal: !wrap, vertical: false)
                        .frame(maxWidth: .infinity, alignment: .leading)
                        .padding(8)
                        .textSelection(.enabled)
                    Color.clear.frame(width: 1, height: 1).id("bottom").frame(maxWidth: .infinity, alignment: .leading)
                }
                .background(Color(UIColor.secondarySystemBackground))
                .scrollDismissesKeyboard(.immediately)
                .simultaneousGesture(MagnifyGesture()
                    .onChanged { pinch = max(0.4, min(3, $0.magnification)) }
                    .onEnded { _ in
                        onZoom(max(5, min(24, (fontSize * pinch).rounded())))
                        pinch = 1
                    })
                .onTapGesture(count: 2) { onZoom(Self.fitting(trimmed, width: geo.size.width - 16)) }
                .onChange(of: text) { _, _ in withAnimation(.linear(duration: 0.1)) { proxy.scrollTo("bottom", anchor: UnitPoint(x: 0, y: 1)) } } // follow the bottom, keep the left edge
            .onChange(of: text) { _, _ in withAnimation(.linear(duration: 0.1)) { proxy.scrollTo("bottom", anchor: UnitPoint(x: 0, y: 1)) } } // follow the bottom, keep the left edge
                .overlay {
                    if text == nil {
                        VStack(spacing: 8) {
                            ProgressView("waiting for the screen…").font(.footnote)
                            if waitedLong {
                                Text("Nothing yet. The instance may be offline, or this agent sits on a host whose flok needs an upgrade (`flok host install <host>`).")
                                    .font(.footnote).foregroundStyle(.secondary).multilineTextAlignment(.center).padding(.horizontal, 24)
                            }
                        }
                        .task {
                            try? await Task.sleep(for: .seconds(6))
                            waitedLong = true
                        }
                    }
                }
            }
        }
    }

    /// The font size at which the widest line of the screen just fits width points.
    static func fitting(_ text: String, width: CGFloat) -> Double {
        let cols = max(20, text.components(separatedBy: "\n").map(\.count).max() ?? 80)
        let em = ("M" as NSString).size(withAttributes: [.font: UIFont.monospacedSystemFont(ofSize: 10, weight: .regular)]).width / 10
        return max(5, min(24, (Double(width) / (Double(cols) * em)).rounded(.down)))
    }

    private var trimmed: String {
        guard let text else { return "" }
        var lines = text.split(separator: "\n", omittingEmptySubsequences: false).map { $0.replacingOccurrences(of: "\\s+$", with: "", options: .regularExpression) }
        while let last = lines.last, last.isEmpty { lines.removeLast() }
        return lines.joined(separator: "\n")
    }
}

struct KeyBar: View {
    struct Key: Identifiable {
        let label: String
        let text: String?
        let keys: [String]?
        var id: String { label }
    }

    static let keys: [Key] = [
        Key(label: "1", text: "1", keys: nil), Key(label: "2", text: "2", keys: nil), Key(label: "3", text: "3", keys: nil),
        Key(label: "y", text: "y", keys: nil), Key(label: "n", text: "n", keys: nil),
        Key(label: "↵", text: nil, keys: ["Enter"]), Key(label: "esc", text: nil, keys: ["Escape"]),
        Key(label: "↑", text: nil, keys: ["Up"]), Key(label: "↓", text: nil, keys: ["Down"]),
        Key(label: "⇥", text: nil, keys: ["Tab"]), Key(label: "⌫", text: nil, keys: ["BSpace"]),
    ]

    let onKey: (Key) -> Void

    var body: some View {
        ScrollView(.horizontal, showsIndicators: false) {
            HStack(spacing: 6) {
                ForEach(Self.keys) { k in
                    Button(k.label) { onKey(k) }
                        .buttonStyle(.bordered)
                        .font(.system(.body, design: .monospaced))
                }
            }
            .padding(.horizontal, 10).padding(.vertical, 6)
        }
    }
}

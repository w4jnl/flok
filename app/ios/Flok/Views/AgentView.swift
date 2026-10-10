// One agent: its state, its live screen, and the keys to answer it.
import SwiftUI

struct AgentView: View {
    let target: Target
    @Environment(Relay.self) private var relay
    @Environment(Settings.self) private var settings
    @State private var input = ""
    @State private var notice: String?
    @State private var confirmInterrupt = false
    @FocusState private var typing: Bool

    private var found: (InstanceState, Agent)? { relay.agent(target) }

    var body: some View {
        VStack(spacing: 0) {
            header
            Divider()
            ScreenView(text: relay.screens[target], fontSize: settings.fontSize)
            Divider()
            KeyBar { key in Task { await send(text: key.text, keys: key.keys) } }
            HStack(spacing: 8) {
                TextField("type a reply, Enter sends it", text: $input, axis: .vertical)
                    .textFieldStyle(.roundedBorder)
                    .lineLimit(1...4)
                    .autocorrectionDisabled()
                    .textInputAutocapitalization(.never)
                    .focused($typing)
                    .onSubmit { Task { await sendInput() } }
                Button { Task { await sendInput() } } label: { Image(systemName: "paperplane.fill") }
                    .buttonStyle(.borderedProminent)
                    .disabled(input.isEmpty)
            }
            .padding(10)
            if let notice {
                Text(notice).font(.footnote).foregroundStyle(.secondary).padding(.bottom, 6)
            }
        }
        .navigationTitle(found?.1.name ?? target.pane)
        .navigationBarTitleDisplayMode(.inline)
        .toolbar {
            ToolbarItem(placement: .topBarTrailing) {
                Menu {
                    Button { Task { await send(keys: ["Enter"]) } } label: { Label("Approve (Enter)", systemImage: "checkmark") }
                    Button { Task { await send(keys: ["Escape"]) } } label: { Label("Deny (Esc)", systemImage: "xmark") }
                    Button { Task { await seen() } } label: { Label("Mark seen", systemImage: "eye") }
                    Divider()
                    Button(role: .destructive) { confirmInterrupt = true } label: { Label("Interrupt (Ctrl-C)", systemImage: "stop.circle") }
                } label: { Image(systemName: "ellipsis.circle") }
            }
        }
        .confirmationDialog("Send Ctrl-C to \(found?.1.name ?? "the agent")?", isPresented: $confirmInterrupt, titleVisibility: .visible) {
            Button("Interrupt", role: .destructive) { Task { await send(keys: ["C-c"]) } }
        }
        .task(id: target) { relay.subscribe(target) }
        .onDisappear { relay.unsubscribe(target) }
    }

    @ViewBuilder private var header: some View {
        if let (inst, a) = found {
            HStack(spacing: 10) {
                Text(StateStyle.glyph(a.state)).foregroundStyle(StateStyle.color(a.state)).font(.system(.title3, design: .monospaced))
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

    private func sendInput() async {
        let text = input
        guard !text.isEmpty else { return }
        input = ""
        await send(text: text, keys: ["Enter"])
    }

    private func send(text: String? = nil, keys: [String]? = nil) async {
        do {
            try await relay.answer(target, text: text, keys: keys)
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

/// A pane's text, monospaced, following the bottom as it changes; wide lines scroll sideways.
struct ScreenView: View {
    let text: String?
    let fontSize: Double

    var body: some View {
        ScrollViewReader { proxy in
            ScrollView([.vertical, .horizontal]) {
                Text(trimmed)
                    .font(.system(size: fontSize, design: .monospaced))
                    .fixedSize(horizontal: true, vertical: false)
                    .frame(maxWidth: .infinity, alignment: .leading)
                    .padding(8)
                    .textSelection(.enabled)
                Color.clear.frame(width: 1, height: 1).id("bottom").frame(maxWidth: .infinity, alignment: .leading)
            }
            .background(Color(UIColor.secondarySystemBackground))
            .onChange(of: text) { _, _ in withAnimation(.linear(duration: 0.1)) { proxy.scrollTo("bottom", anchor: UnitPoint(x: 0, y: 1)) } } // follow the bottom, keep the left edge
            .overlay {
                if text == nil {
                    ProgressView("waiting for the screen…").font(.footnote)
                }
            }
        }
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

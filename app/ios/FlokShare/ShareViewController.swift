// The share sheet's "flok": a screenshot, a photo, a PDF, a piece of text or a link from any
// app goes to an agent. The extension reads the relay and the device token from the keychain
// group the app shares, lists the agents (the ones that need you first), and sends the file
// with a few optional words through POST /api/answer, Enter after it. The app itself never
// has to open.
import SwiftUI
import UIKit
import UniformTypeIdentifiers

enum SharedItem: Sendable {
    case image(Data, String)
    case pdf(Data, String)
    case text(String)
}

final class ShareViewController: UIViewController {
    override func viewDidLoad() {
        super.viewDidLoad()
        view.backgroundColor = .systemGroupedBackground
        let root = ShareView(
            load: { [weak self] in await self?.loadItem() },
            finish: { [weak self] in self?.extensionContext?.completeRequest(returningItems: nil) },
            cancel: { [weak self] in self?.extensionContext?.cancelRequest(withError: CocoaError(.userCancelled)) })
        let host = UIHostingController(rootView: root)
        addChild(host)
        host.view.translatesAutoresizingMaskIntoConstraints = false
        view.addSubview(host.view)
        NSLayoutConstraint.activate([
            host.view.topAnchor.constraint(equalTo: view.topAnchor),
            host.view.bottomAnchor.constraint(equalTo: view.bottomAnchor),
            host.view.leadingAnchor.constraint(equalTo: view.leadingAnchor),
            host.view.trailingAnchor.constraint(equalTo: view.trailingAnchor),
        ])
        host.didMove(toParent: self)
    }

    /// The first attachment flok can use: an image, a PDF, a link or text.
    private func loadItem() async -> SharedItem? {
        let items = extensionContext?.inputItems as? [NSExtensionItem] ?? []
        for item in items {
            for p in item.attachments ?? [] {
                let name = p.suggestedName ?? ""
                if p.hasItemConformingToTypeIdentifier(UTType.image.identifier), let d = try? await Self.data(p, UTType.image) {
                    return .image(d, name)
                }
                if p.hasItemConformingToTypeIdentifier(UTType.pdf.identifier), let d = try? await Self.data(p, UTType.pdf) {
                    return .pdf(d, name)
                }
                if p.hasItemConformingToTypeIdentifier(UTType.url.identifier), let s = await Self.string(p, UTType.url), !s.isEmpty {
                    return .text(s)
                }
                if p.hasItemConformingToTypeIdentifier(UTType.plainText.identifier), let s = await Self.string(p, UTType.plainText), !s.isEmpty {
                    return .text(s)
                }
            }
        }
        return nil
    }

    private static func data(_ p: NSItemProvider, _ type: UTType) async throws -> Data {
        try await withCheckedThrowingContinuation { c in
            _ = p.loadDataRepresentation(forTypeIdentifier: type.identifier) { data, error in
                if let data {
                    c.resume(returning: data)
                } else {
                    c.resume(throwing: error ?? CocoaError(.fileReadUnknown))
                }
            }
        }
    }

    private static func string(_ p: NSItemProvider, _ type: UTType) async -> String? {
        await withCheckedContinuation { c in
            p.loadItem(forTypeIdentifier: type.identifier, options: nil) { item, _ in
                let s: String?
                switch item {
                case let url as URL: s = url.absoluteString
                case let text as String: s = text
                case let data as Data: s = String(data: data, encoding: .utf8)
                default: s = nil
                }
                c.resume(returning: s)
            }
        }
    }
}

struct ShareView: View {
    let load: @MainActor () async -> SharedItem?
    let finish: @MainActor () -> Void
    let cancel: @MainActor () -> Void

    @State private var item: SharedItem?
    @State private var file: AttachedFile?
    @State private var sharedText = ""
    @State private var note = ""
    @State private var instances: [InstanceState] = []
    @State private var selected: Pick?
    @State private var status: String?
    @State private var loading = true
    @State private var sending = false
    @State private var sent = false

    struct Pick: Hashable {
        var instance: String
        var pane: String
    }

    var body: some View {
        NavigationStack {
            Form {
                Section {
                    preview
                } header: {
                    Text("What")
                }
                Section {
                    if loading {
                        ProgressView("loading the agents…")
                    } else if instances.isEmpty {
                        Text(status ?? "no instance is linked to the relay").foregroundStyle(.secondary)
                    }
                    ForEach(instances) { inst in
                        if !inst.agents.isEmpty {
                            Text(inst.name + (inst.online ? "" : " · offline")).font(.caption).foregroundStyle(.secondary).listRowSeparator(.hidden)
                            ForEach(inst.agents) { a in
                                row(inst, a)
                            }
                        }
                    }
                } header: {
                    Text("To")
                }
                Section {
                    TextField("a few words to go with it (optional)", text: $note, axis: .vertical).lineLimit(1...4)
                } footer: {
                    if let warning {
                        Text(warning).foregroundStyle(.orange)
                    } else if let status, !loading {
                        Text(status)
                    }
                }
            }
            .navigationTitle("Send to flok")
            .navigationBarTitleDisplayMode(.inline)
            .toolbar {
                ToolbarItem(placement: .cancellationAction) { Button("Cancel") { cancel() } }
                ToolbarItem(placement: .confirmationAction) {
                    Button(sent ? "Sent" : "Send") { Task { await send() } }
                        .disabled(selected == nil || sending || sent || loading || warning != nil || (file == nil && sharedText.isEmpty && note.isEmpty))
                }
            }
            .task { await prepare() }
        }
    }

    @ViewBuilder private var preview: some View {
        if let file {
            AttachmentPreview(file: file)
        } else if !sharedText.isEmpty {
            Text(sharedText).font(.footnote).lineLimit(6)
        } else if loading {
            ProgressView()
        } else {
            Text("nothing flok can send: share an image, a PDF, text or a link").foregroundStyle(.secondary).font(.footnote)
        }
    }

    private func row(_ inst: InstanceState, _ a: Agent) -> some View {
        let pick = Pick(instance: inst.name, pane: a.paneRef)
        return Button {
            selected = pick
        } label: {
            HStack(spacing: 10) {
                Text(glyph(a.state)).font(.system(.body, design: .monospaced)).foregroundStyle(color(a.state))
                VStack(alignment: .leading, spacing: 1) {
                    Text(a.name).foregroundStyle(.primary)
                    Text([a.host, a.sessionName].compactMap { $0 }.filter { !$0.isEmpty }.joined(separator: " · ") + "  " + label(a))
                        .font(.caption).foregroundStyle(.secondary).lineLimit(1)
                }
                Spacer()
                if selected == pick {
                    Image(systemName: "checkmark").foregroundStyle(Color.accentColor)
                }
            }
        }
    }

    /// Why this cannot go to the chosen agent as it is.
    private var warning: String? {
        guard let selected, let inst = instances.first(where: { $0.name == selected.instance }) else { return nil }
        if !inst.online { return "\(inst.name) is offline" }
        if file != nil && !inst.takesFiles { return "flok on \(inst.name) is too old for files: update it there" }
        let text = composed
        if (text.contains("\n") || text.count > shortAnswerLimit) && !inst.takesLongText { return "flok on \(inst.name) takes at most \(shortAnswerLimit) characters on one line: update it there" }
        return nil
    }

    /// The words typed here, then the shared text.
    private var composed: String {
        [note.trimmingCharacters(in: .whitespacesAndNewlines), sharedText].filter { !$0.isEmpty }.joined(separator: "\n")
    }

    private func prepare() async {
        async let loaded = load()
        if let api = RelayAPI.fromKeychain() {
            do {
                let list = try await api.instances()
                instances = list.sorted { a, b in a.online != b.online ? a.online : a.name < b.name }
                // the agent that needs you most, else the first one
                let candidates = instances.filter(\.online).flatMap { inst in inst.agents.map { (inst, $0) } }
                if let hit = candidates.min(by: { $0.1.state.priority < $1.1.state.priority }) {
                    selected = Pick(instance: hit.0.name, pane: hit.1.paneRef)
                }
            } catch {
                status = error.localizedDescription
            }
        } else {
            status = "open flok once and set the relay up there"
        }
        switch await loaded {
        case .image(let data, let name)?:
            if let img = UIImage(data: data) {
                file = AttachedFile.image(img, name: name.isEmpty ? "image" : name)
            }
            if file == nil { status = "that image cannot be read" }
        case .pdf(let data, let name)?:
            file = AttachedFile.pdf(data, name: name.isEmpty ? "document" : name)
            if file == nil { status = "that PDF is too large (4 MB at most)" }
        case .text(let s)?:
            sharedText = s
        case nil:
            break
        }
        loading = false
    }

    private func send() async {
        guard let selected, let api = RelayAPI.fromKeychain() else { return }
        sending = true
        defer { sending = false }
        do {
            let text = composed
            try await api.answer(instance: selected.instance, pane: selected.pane, text: text.isEmpty ? nil : text, keys: ["Enter"], file: file)
            try? await api.seen(instance: selected.instance, pane: selected.pane)
            sent = true
            status = "sent"
            try? await Task.sleep(for: .milliseconds(500))
            finish()
        } catch {
            status = error.localizedDescription
        }
    }

    private func glyph(_ s: AgentState) -> String {
        switch s {
        case .blocked: "●"
        case .working: "✻"
        case .paused: "◌"
        case .done: "✓"
        case .idle: "○"
        case .unknown: "?"
        }
    }

    private func color(_ s: AgentState) -> Color {
        switch s {
        case .blocked: .orange
        case .working: .blue
        case .paused: .purple
        case .done: .green
        case .idle, .unknown: .secondary
        }
    }

    private func label(_ a: Agent) -> String {
        switch a.state {
        case .blocked: "needs you" + (a.reason.map { ": " + $0 } ?? "")
        case .working: "working" + (a.tool.map { ": " + $0 } ?? "")
        default: a.state.rawValue
        }
    }
}

struct AttachmentPreview: View {
    let file: AttachedFile

    var body: some View {
        HStack(spacing: 10) {
            if file.mime.hasPrefix("image/"), let img = UIImage(data: file.data) {
                Image(uiImage: img).resizable().scaledToFill().frame(width: 56, height: 56).clipShape(RoundedRectangle(cornerRadius: 8))
            } else {
                Image(systemName: "doc.richtext").font(.largeTitle).frame(width: 56, height: 56)
            }
            VStack(alignment: .leading, spacing: 2) {
                Text(file.name).lineLimit(1)
                Text("\(file.sizeLabel) · saved next to the agent, its path typed into the prompt").font(.caption).foregroundStyle(.secondary)
            }
        }
    }
}

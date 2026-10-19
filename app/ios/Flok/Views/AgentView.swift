// One agent: its state, its live screen, and the keys to answer it; a reply typed, dictated
// (hold the microphone) or with a picture attached.
import PhotosUI
import SwiftUI

struct AgentView: View {
    let target: Target
    @Environment(Relay.self) private var relay
    @Environment(Settings.self) private var settings
    @Environment(LockGate.self) private var lock
    @Environment(\.verticalSizeClass) private var vClass
    @Environment(\.dismiss) private var dismiss
    @Environment(\.scenePhase) private var phase
    @State private var input = ""
    @State private var notice: String?
    @State private var confirmInterrupt = false
    @State private var inputChoice: Bool? // the user's show/hide of the bottom; nil = the orientation's default
    @State private var attachments: [AttachedFile] = []
    @State private var showPhotos = false
    @State private var showCamera = false
    @State private var photoItems: [PhotosPickerItem] = []
    @State private var voice = Voice()
    @State private var voiceBase = "" // what the field held before the microphone was pressed
    @State private var pressStarted: Date?
    @FocusState private var typing: Bool

    private var found: (InstanceState, Agent)? { relay.agent(target) }
    /// Landscape on a phone: little height, so the chrome shrinks to two thin strips.
    private var compact: Bool { vClass == .compact }
    private var inputShown: Bool { inputChoice ?? !compact }
    private var takesFiles: Bool { found?.0.takesFiles ?? false }

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
                if attachments.count == 1, let f = attachments.first {
                    AttachmentChip(file: f) { attachments.removeAll() }
                        .padding(.horizontal, 10)
                } else if !attachments.isEmpty {
                    ScrollView(.horizontal, showsIndicators: false) {
                        HStack(spacing: 6) {
                            ForEach(Array(attachments.enumerated()), id: \.offset) { i, f in
                                AttachmentChip(file: f) { attachments.remove(at: i) }.frame(width: 230)
                            }
                        }
                        .padding(.horizontal, 10)
                    }
                }
                HStack(spacing: 6) {
                    Button { inputChoice = false; typing = false } label: { Image(systemName: "chevron.down").fontWeight(.semibold) }
                        .buttonStyle(.borderless)
                        .accessibilityLabel("Hide the keys and the reply field")
                    TextField(voice.listening ? "listening…" : "type a reply, Enter sends it", text: $input, axis: .vertical)
                        .textFieldStyle(.roundedBorder)
                        .lineLimit(1...5)
                        .autocorrectionDisabled()
                        .textInputAutocapitalization(.never)
                        .focused($typing)
                        .onSubmit { Task { await sendInput() } }
                    if typing {
                        Button { typing = false } label: { Image(systemName: "keyboard.chevron.compact.down") }
                            .buttonStyle(.bordered)
                            .accessibilityLabel("Hide the keyboard")
                    } else {
                        attachMenu
                        VoiceButton(voice: voice, onDown: voiceDown, onUp: voiceUp)
                    }
                    Button { Task { await sendInput() } } label: { Image(systemName: "paperplane.fill") }
                        .buttonStyle(.borderedProminent)
                        .disabled(input.isEmpty && attachments.isEmpty)
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
        .photosPicker(isPresented: $showPhotos, selection: $photoItems, maxSelectionCount: max(1, AttachedFile.maxCount - attachments.count), matching: .images)
        .onChange(of: photoItems) { _, items in
            guard !items.isEmpty else { return }
            photoItems = []
            Task { await attach(items) }
        }
        .fullScreenCover(isPresented: $showCamera) {
            CameraPicker { image in
                showCamera = false
                if let image { attach(image, name: "photo") }
            }
            .ignoresSafeArea()
        }
        .task(id: target) {
            relay.subscribe(target)
            await seenIfUnseen()
        }
        .onDisappear {
            relay.unsubscribe(target)
            voice.cancel()
        }
        // Looking at the agent here is looking at it: as focusing its pane in tmux, it is marked
        // seen when it opens, when it finishes while open, and when the app comes back to it;
        // not while the app lock's cover is up, nobody saw it then.
        .onChange(of: found?.1.unseen ?? 0) { _, n in if n > 0 { Task { await seenIfUnseen() } } }
        .onChange(of: phase) { _, p in if p == .active { Task { await seenIfUnseen() } } }
        .onChange(of: lock.locked) { _, l in if !l { Task { await seenIfUnseen() } } }
        .onChange(of: compact) { _, _ in inputChoice = nil } // a turn of the phone goes back to that orientation's default
        .onChange(of: voice.live) { _, live in if voice.listening { input = voiceBase + live } } // the words as they come
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
                    OfflineLabel(instance: inst).font(.caption)
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
                if !inst.online { OfflineLabel(instance: inst) }
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
            followButton
            Divider()
            Button(role: .destructive) { confirmInterrupt = true } label: { Label("Interrupt (Ctrl-C)", systemImage: "stop.circle") }
        } label: { Image(systemName: "ellipsis.circle") }
    }

    /// A picture for the agent: from the library, the camera or the clipboard. It is shrunk
    /// here, saved next to the agent by the instance, and its path typed after the words.
    private var attachMenu: some View {
        Menu {
            if takesFiles {
                Button { showPhotos = true } label: { Label("Photo Library", systemImage: "photo.on.rectangle") }
                if UIImagePickerController.isSourceTypeAvailable(.camera) {
                    Button { showCamera = true } label: { Label("Take Photo", systemImage: "camera") }
                }
                if UIPasteboard.general.hasImages {
                    Button { pasteImage() } label: { Label("Paste Image", systemImage: "doc.on.clipboard") }
                }
            } else {
                Text(found == nil ? "no agent" : "flok on \(target.instance) is too old for files: update it there")
            }
        } label: {
            Image(systemName: attachments.isEmpty ? "paperclip" : "paperclip.badge.ellipsis")
                .font(.body.weight(.semibold))
                .frame(width: 34, height: 32)
                .background(Color.accentColor.opacity(0.12), in: RoundedRectangle(cornerRadius: 8))
        }
        .accessibilityLabel("Attach a picture")
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
                    Label(m.until.map { "Unmute (muted until \(NotificationPrefs.untilWords($0)))" } ?? "Unmute", systemImage: "bell")
                }
            } else {
                let morning = NotificationPrefs.nextMorning()
                Menu {
                    Button("For 1 hour") { mute(inst.name, a, until: .now.addingTimeInterval(3600)) }
                    Button("Until \(NotificationPrefs.untilWords(morning))") { mute(inst.name, a, until: morning) }
                    Button("Always") { mute(inst.name, a, until: nil) }
                } label: { Label("Mute notifications", systemImage: "bell.slash") }
            }
        }
    }

    /// Follows this agent in the Dynamic Island and on the Lock Screen until it finishes.
    @ViewBuilder private var followButton: some View {
        if let (inst, a) = found {
            if LiveActivities.shared.following(target) {
                Button {
                    LiveActivities.shared.unfollow(target)
                    flash("not following")
                } label: { Label("Stop following", systemImage: "rectangle.portrait.slash") }
            } else {
                Button {
                    do {
                        try LiveActivities.shared.follow(target, instance: inst, agent: a)
                        flash("following in the Dynamic Island")
                    } catch {
                        flash(LiveActivities.shared.enabled ? error.localizedDescription : "Live Activities are off for flok in Settings")
                    }
                } label: { Label("Follow in Dynamic Island", systemImage: "capsule.portrait") }
            }
        }
    }

    private func mute(_ instance: String, _ a: Agent, until: Date?) {
        relay.updatePrefs { $0.mute(instance: instance, host: a.host, agent: a.name, until: until) }
        flash("muted")
    }

    // MARK: attachments

    private func attach(_ items: [PhotosPickerItem]) async {
        for item in items {
            guard let data = try? await item.loadTransferable(type: Data.self), let image = UIImage(data: data) else {
                flash("a picture cannot be read")
                continue
            }
            attach(image, name: "photo")
        }
    }

    private func attach(_ image: UIImage, name: String) {
        guard attachments.count < AttachedFile.maxCount else {
            flash("at most \(AttachedFile.maxCount) files in one reply")
            return
        }
        guard let file = AttachedFile.image(image, name: name) else {
            flash("that picture cannot be used")
            return
        }
        attachments.append(file)
        flash(attachments.count == 1 ? "attached \(file.name), \(file.sizeLabel)" : "\(attachments.count) files attached")
    }

    private func pasteImage() {
        guard let image = UIPasteboard.general.image else {
            flash("no picture on the clipboard")
            return
        }
        attach(image, name: "clipboard")
    }

    // MARK: voice

    private var vocabulary: [String] { Vocabulary.build(instances: relay.instances, screen: relay.screens[target]) }
    private var voiceLocale: Locale { settings.voiceLocale.isEmpty ? Locale.current : Locale(identifier: settings.voiceLocale) }

    /// The microphone pressed: start listening, after whatever the field holds. A press while
    /// it listens (tap mode) ends it on release.
    private func voiceDown() {
        guard !voice.listening else {
            pressStarted = nil
            return
        }
        pressStarted = .now
        typing = false
        let base = input.trimmingCharacters(in: .whitespacesAndNewlines)
        voiceBase = base.isEmpty ? "" : base + " "
        let words = vocabulary
        let locale = voiceLocale
        Task { await voice.start(vocabulary: words, locale: locale) }
    }

    /// Released: a hold ends the dictation, a tap leaves it listening until the next tap.
    private func voiceUp() {
        if let started = pressStarted, Date.now.timeIntervalSince(started) < 0.35 {
            pressStarted = nil
            return
        }
        pressStarted = nil
        Task { await finishVoice() }
    }

    private func finishVoice() async {
        let text = await voice.stop(cleanup: settings.voiceCleanup, vocabulary: vocabulary)
        if let e = voice.lastError {
            flash(e)
        }
        input = voiceBase + text
        guard !text.isEmpty else { return }
        if settings.voiceSendOnRelease {
            await sendInput()
        }
    }

    // MARK: sending

    private func sendInput() async {
        let text = input
        let files = attachments
        guard !text.isEmpty || !files.isEmpty else { return }
        if let inst = found?.0, !inst.takesLongText, text.contains("\n") || text.count > shortAnswerLimit {
            flash("flok on \(inst.name) takes at most \(shortAnswerLimit) characters on one line: update it there")
            return
        }
        input = ""
        attachments = []
        await send(text: text, keys: ["Enter"], files: files)
    }

    private func seenIfUnseen() async {
        guard phase == .active, !lock.locked, let (_, a) = found, a.unseen > 0 else { return }
        try? await relay.markSeen(target)
    }

    private func send(text: String? = nil, keys: [String]? = nil, files: [AttachedFile] = []) async {
        do {
            try await relay.answer(target, text: text, keys: keys, files: files)
            await seenIfUnseen()
            flash(files.isEmpty ? "sent" : files.count == 1 ? "sent with \(files[0].name)" : "sent with \(files.count) files")
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

/// A pane's text, monospaced, following the bottom as it changes while the reader is there:
/// scrolled up, it holds still as the screen refreshes under it (BottomFollower), and an arrow
/// in the corner leads back. Wide lines scroll sideways or wrap (wrap): prose at words, a rule
/// cut to the width, a table in a block of its own that scrolls sideways (ScreenLines); a pinch
/// zooms the text, a double tap fits the widest line of text to the screen.
struct ScreenView: View {
    let text: String?
    let fontSize: Double
    var wrap = false
    var onZoom: (Double) -> Void = { _ in }
    @State private var waitedLong = false
    @State private var pinch: CGFloat = 1
    @State private var follow = BottomFollower()

    var body: some View {
        GeometryReader { geo in
            ScrollViewReader { proxy in
                ScrollView(wrap ? [.vertical] : [.vertical, .horizontal]) {
                    VStack(alignment: .leading, spacing: 0) {
                        if wrap {
                            wrapped(width: geo.size.width - 16)
                                .padding(8)
                        } else {
                            Text(trimmed)
                                .font(.system(size: fontSize * pinch, design: .monospaced))
                                .fixedSize(horizontal: true, vertical: false)
                                .frame(maxWidth: .infinity, alignment: .leading)
                                .padding(8)
                                .textSelection(.enabled)
                        }
                        Color.clear.frame(width: 1, height: 1).id("bottom").frame(maxWidth: .infinity, alignment: .leading)
                    }
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
                .modifier(FollowWatch(follow: follow))
                .onChange(of: text) { _, _ in if follow.following { toBottom(proxy) } }
                .onChange(of: geo.size) { _, _ in if follow.following { toBottom(proxy) } } // the keyboard came up, the phone turned
                .overlay(alignment: .bottomTrailing) {
                    if !follow.following, text != nil {
                        Button {
                            follow.resume()
                            toBottom(proxy)
                        } label: {
                            Image(systemName: "arrow.down").fontWeight(.semibold).padding(10)
                        }
                        .background(.thinMaterial, in: Circle())
                        .padding(12)
                        .accessibilityLabel("Back to the bottom of the screen, following it again")
                        .transition(.opacity)
                    }
                }
                .animation(.easeInOut(duration: 0.15), value: follow.following)
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

    /// The screen in wrap mode, block by block: prose wraps, a rule is cut to the columns that
    /// fit, a table keeps its lines whole and scrolls sideways on its own.
    @ViewBuilder private func wrapped(width: CGFloat) -> some View {
        let size = fontSize * pinch
        let font = Font.system(size: size, design: .monospaced)
        let cols = max(8, Int(width / max(Self.em(size), 1)) - 1) // one column short of the edge: no ellipsis from a rounding

        VStack(alignment: .leading, spacing: 0) {
            ForEach(ScreenLines.blocks(of: trimmed)) { block in
                switch block.kind {
                case .prose:
                    Text(block.text).font(font)
                        .frame(maxWidth: .infinity, alignment: .leading)
                        .textSelection(.enabled)
                case .rule:
                    ForEach(Array(block.lines.enumerated()), id: \.offset) { _, line in
                        Text(ScreenLines.fit(line, columns: cols)).font(font).lineLimit(1)
                            .frame(maxWidth: .infinity, alignment: .leading)
                    }
                case .boxed:
                    ScrollView(.horizontal) {
                        Text(block.text).font(font)
                            .fixedSize(horizontal: true, vertical: false)
                            .textSelection(.enabled)
                    }
                }
            }
        }
    }

    /// The width of one character of the monospaced system font at size points.
    static func em(_ size: Double) -> Double {
        ("M" as NSString).size(withAttributes: [.font: UIFont.monospacedSystemFont(ofSize: size, weight: .regular)]).width
    }

    /// The font size at which the widest line of text just fits width points; rules do not
    /// count, they are as wide as the pane whatever it shows.
    static func fitting(_ text: String, width: CGFloat) -> Double {
        let widest = ScreenLines.widest(of: text)
        let cols = max(20, widest == 0 ? 80 : widest)
        return max(5, min(24, (Double(width) / (Double(cols) * em(10) / 10)).rounded(.down)))
    }

    private var trimmed: String {
        guard let text else { return "" }
        var lines = text.split(separator: "\n", omittingEmptySubsequences: false).map { $0.replacingOccurrences(of: "\\s+$", with: "", options: .regularExpression) }
        while let last = lines.last, last.isEmpty { lines.removeLast() }
        return lines.joined(separator: "\n")
    }

    /// Scrolls to the bottom, keeping the left edge; the frames the scroll passes through are
    /// the view's doing, not the reader's, so they do not end the following.
    private func toBottom(_ proxy: ScrollViewProxy) {
        follow.settle()
        withAnimation(.linear(duration: 0.1)) { proxy.scrollTo("bottom", anchor: UnitPoint(x: 0, y: 1)) }
    }
}

/// Tells the follower where the reader is after every scroll and every layout, from the scroll
/// view's own geometry (iOS 18). Before it the follower hears nothing and the screen follows the
/// bottom as it always did: a GeometryReader in the content, reporting its frame in the scroll
/// view's coordinate space through a preference, gave one empty frame and never spoke again.
private struct FollowWatch: ViewModifier {
    let follow: BottomFollower

    struct Place: Equatable {
        let distance: CGFloat
        let offset: CGFloat
    }

    func body(content: Content) -> some View {
        if #available(iOS 18, *) {
            content.onScrollGeometryChange(for: Place.self) { g in
                Place(distance: g.contentSize.height - g.visibleRect.maxY, offset: g.contentOffset.y)
            } action: { _, place in
                follow.look(distance: place.distance, offset: place.offset)
            }
        } else {
            content
        }
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

/// Why an instance's agent cannot be reached: switched off there (quiet, on purpose), or offline.
struct OfflineLabel: View {
    let instance: InstanceState

    var body: some View {
        if instance.switchedOff {
            Label("link off", systemImage: "pause.circle").foregroundStyle(Palette.comment)
        } else {
            Label("offline", systemImage: "wifi.slash").foregroundStyle(Palette.orange)
        }
    }
}

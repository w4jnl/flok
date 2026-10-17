import SwiftUI

struct SettingsView: View {
    @Environment(Relay.self) private var relay
    @Environment(Settings.self) private var settings
    @Environment(\.dismiss) private var dismiss
    @State private var showPair = false
    @State private var showApprove = false
    @State private var notifications = Notifications.shared
    @State private var voiceLocales: [Locale] = []

    private var voiceFooter: String {
        var s = "Hold the microphone next to the reply field and talk; let go to stop, or tap it once to keep listening. The agent's names and the words on its screen are given to the recogniser, so file names come out as written."
        if let why = Cleanup.unavailableReason {
            s += " The tidy-up (punctuation, filler words out, names spelled as code) is off here: \(why)."
        } else {
            s += " The tidy-up runs on this phone and fixes punctuation, drops filler words and spells names as code."
        }
        return s
    }

    var body: some View {
        @Bindable var settings = settings
        Form {
            Section {
                LabeledContent("Relay", value: settings.relayURL.isEmpty ? "none yet" : (settings.baseURL?.host() ?? settings.relayURL))
                LabeledContent("Paired as", value: settings.pairedPhoneName ?? (settings.deviceToken.isEmpty ? "not yet" : "a device token"))
                if let tenant = settings.pairedTenant {
                    LabeledContent("Tenant", value: tenant)
                }
                Button(settings.isConfigured ? "Pair this phone again…" : "Pair this phone…") { showPair = true }
                if settings.isConfigured {
                    Button("Link a computer…") { showApprove = true }
                }
                LabeledContent("Status", value: relay.state.label)
            } header: {
                Text("Relay")
            } footer: {
                Text("Pairing gives this phone a key of its own on the relay: `flok phone pair` on a Mac shows the code, `flok phone forget` revokes it. A device token from the relay's config still works, under Pair. Link a computer approves a Mac that shows `flok link join`'s code into this phone's tenant, the floks it sees.")
            }
            Section {
                LabeledContent("Permission", value: permissionLabel)
                LabeledContent("Registered with the relay", value: notifications.isRegistered ? "yes" : (notifications.apnsToken == nil ? "no APNs token yet" : "no"))
                if notifications.authorization == .notDetermined {
                    Button("Enable notifications") { Task { await notifications.enable() } }
                } else if notifications.authorization == .denied {
                    Text("Allow notifications for flok in the Settings app.").font(.footnote).foregroundStyle(.secondary)
                } else {
                    NavigationLink("Filters and sounds") { NotificationSettingsView() }
                    Button("Register again") {
                        settings.registered = nil
                        Task { await notifications.registerIfNeeded() }
                    }
                }
                if let e = notifications.lastError {
                    Text(e).font(.footnote).foregroundStyle(Palette.orange)
                }
            } header: {
                Text("Notifications")
            } footer: {
                Text("Pushes come through the relay's APNs key. This phone tells the relay which Apple service its token belongs to (a build from Xcode: sandbox).")
            }
            Section {
                Stepper("Font size \(Int(settings.fontSize))", value: $settings.fontSize, in: 5...24)
                Toggle("Wrap long lines", isOn: $settings.wrapLines)
            } header: {
                Text("Screen")
            } footer: {
                Text("On the agent's screen a pinch zooms the text and a double tap fits the widest line; the Aa menu has the same.")
            }
            Section {
                LabeledContent("Recognition", value: Voice.engineName)
                Picker("Language", selection: $settings.voiceLocale) {
                    Text("the phone's").tag("")
                    ForEach(voiceLocales, id: \.identifier) { l in
                        Text(Locale.current.localizedString(forIdentifier: l.identifier) ?? l.identifier).tag(l.identifier)
                    }
                }
                Toggle("Tidy up with Apple Intelligence", isOn: $settings.voiceCleanup)
                    .disabled(!Cleanup.available)
                Toggle("Send as soon as the button is released", isOn: $settings.voiceSendOnRelease)
            } header: {
                Text("Voice")
            } footer: {
                Text(voiceFooter)
            }
            .task { voiceLocales = await Voice.supportedLocales() }
            Section("About") {
                LabeledContent("Version", value: Bundle.main.object(forInfoDictionaryKey: "CFBundleShortVersionString") as? String ?? "")
                LabeledContent("Instances", value: "\(relay.instances.count)")
                NavigationLink("Acknowledgements") { AcknowledgementsView() }
            }
        }
        .navigationTitle("Settings")
        .navigationBarTitleDisplayMode(.inline)
        .toolbar { ToolbarItem(placement: .confirmationAction) { Button("Done") { dismiss() } } }
        .sheet(isPresented: $showApprove) {
            NavigationStack { ApproveView() }
        }
        .sheet(isPresented: $showPair) {
            NavigationStack { PairView() }
        }
    }

    private var permissionLabel: String {
        switch notifications.authorization {
        case .authorized: "allowed"
        case .provisional: "provisional"
        case .ephemeral: "ephemeral"
        case .denied: "denied"
        case .notDetermined: "not asked yet"
        @unknown default: "?"
        }
    }
}

import SwiftUI

struct SettingsView: View {
    @Environment(Relay.self) private var relay
    @Environment(Settings.self) private var settings
    @Environment(\.dismiss) private var dismiss
    @State private var url = ""
    @State private var token = ""
    @State private var notifications = Notifications.shared

    var body: some View {
        @Bindable var settings = settings
        Form {
            Section {
                TextField("https://flok.example.net", text: $url)
                    .keyboardType(.URL).textInputAutocapitalization(.never).autocorrectionDisabled()
                SecureField("device token", text: $token)
                    .textInputAutocapitalization(.never).autocorrectionDisabled()
                Button("Save and connect") {
                    settings.relayURL = url
                    settings.deviceToken = token.trimmingCharacters(in: .whitespacesAndNewlines)
                    relay.connect()
                    Task { await notifications.registerIfNeeded() }
                }
                .disabled(RelayURL.base(url) == nil || token.trimmingCharacters(in: .whitespaces).isEmpty)
                LabeledContent("Status", value: relay.state.label)
            } header: {
                Text("Relay")
            } footer: {
                Text("One of the relay's device_tokens (FLOK_RELAY_DEVICE_TOKENS in the stack). Not an instance's [link] token: that one logs a flok in, this one logs the phone in.")
            }
            Section {
                LabeledContent("Permission", value: permissionLabel)
                LabeledContent("Registered with the relay", value: notifications.isRegistered ? "yes" : (notifications.apnsToken == nil ? "no APNs token yet" : "no"))
                if notifications.authorization == .notDetermined {
                    Button("Enable notifications") { Task { await notifications.enable() } }
                } else if notifications.authorization == .denied {
                    Text("Allow notifications for flok in the Settings app.").font(.footnote).foregroundStyle(.secondary)
                } else {
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
            Section("Screen") {
                Stepper("Font size \(Int(settings.fontSize))", value: $settings.fontSize, in: 7...18)
            }
            Section("About") {
                LabeledContent("Version", value: Bundle.main.object(forInfoDictionaryKey: "CFBundleShortVersionString") as? String ?? "")
                LabeledContent("Instances", value: "\(relay.instances.count)")
            }
        }
        .navigationTitle("Settings")
        .navigationBarTitleDisplayMode(.inline)
        .toolbar { ToolbarItem(placement: .confirmationAction) { Button("Done") { dismiss() } } }
        .onAppear {
            url = settings.relayURL
            token = settings.deviceToken
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

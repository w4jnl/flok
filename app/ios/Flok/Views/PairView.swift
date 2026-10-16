// Pairing this phone: scan the QR `flok phone pair` shows on a Mac, or type its code and the
// relay's address, and the relay hands this phone a key of its own. The old way, a device token
// from the relay's config, stays behind a disclosure for a relay that predates pairing.
import SwiftUI

struct PairView: View {
    @Environment(Relay.self) private var relay
    @Environment(Settings.self) private var settings
    @Environment(\.dismiss) private var dismiss
    /// From a scanned or opened flok://pair link: the fields are filled and the pairing starts.
    var prefill: (relay: String, code: String)?

    @State private var relayURL = ""
    @State private var code = ""
    @State private var scanning = false
    @State private var busy = false
    @State private var error: String?
    @State private var pairedAs: String?
    @State private var manualURL = ""
    @State private var manualToken = ""
    @State private var showManual = false

    var body: some View {
        Form {
            Section {
                if QRScanner.canScan {
                    Button { scanning = true } label: { Label("Scan the code", systemImage: "qrcode.viewfinder") }
                }
                TextField("https://flok.example.net", text: $relayURL)
                    .keyboardType(.URL).textInputAutocapitalization(.never).autocorrectionDisabled()
                TextField("AB7Q-K2MN", text: $code)
                    .font(.body.monospaced()).textInputAutocapitalization(.characters).autocorrectionDisabled()
                    .onChange(of: code) { _, v in
                        let f = PairCode.format(v)
                        if f != v { code = f }
                    }
                Button {
                    Task { await pair(relay: relayURL, code: code) }
                } label: {
                    if busy { ProgressView() } else { Text("Pair") }
                }
                .disabled(busy || RelayURL.base(relayURL) == nil || !PairCode.isComplete(code))
                if let error {
                    Text(error).font(.footnote).foregroundStyle(Palette.orange)
                }
                if let pairedAs {
                    Label("paired as \(pairedAs)", systemImage: "checkmark.circle").foregroundStyle(Palette.green)
                }
            } header: {
                Text("Pair this phone")
            } footer: {
                Text("Run `flok phone pair` on a Mac that runs flok: it shows a code and its QR, good for five minutes. Scan it with the Camera app or here, or type the code and the relay's address.")
            }
            Section {
                DisclosureGroup("Enter a device token instead", isExpanded: $showManual) {
                    TextField("https://flok.example.net", text: $manualURL)
                        .keyboardType(.URL).textInputAutocapitalization(.never).autocorrectionDisabled()
                    SecureField("device token", text: $manualToken)
                        .textInputAutocapitalization(.never).autocorrectionDisabled()
                    Button("Save and connect") {
                        settings.relayURL = manualURL
                        settings.deviceToken = manualToken.trimmingCharacters(in: .whitespacesAndNewlines)
                        settings.pairedPhoneID = nil
                        settings.pairedPhoneName = nil
                        settings.registered = nil
                        relay.connect()
                        Task { await Notifications.shared.registerIfNeeded() }
                        dismiss()
                    }
                    .disabled(RelayURL.base(manualURL) == nil || manualToken.trimmingCharacters(in: .whitespaces).isEmpty)
                }
            } footer: {
                Text("One of the relay's device_tokens (FLOK_RELAY_DEVICE_TOKENS in the stack), for a relay that predates pairing. Not an instance's [link] token: that one logs a flok in, this one logs the phone in.")
            }
        }
        .navigationTitle("Pair")
        .navigationBarTitleDisplayMode(.inline)
        .toolbar { ToolbarItem(placement: .cancellationAction) { Button("Close") { dismiss() } } }
        .fullScreenCover(isPresented: $scanning) {
            NavigationStack {
                QRScanner { r, c in
                    scanning = false
                    relayURL = r
                    code = c
                    Task { await pair(relay: r, code: c) }
                }
                .ignoresSafeArea()
                .navigationTitle("Scan the code")
                .navigationBarTitleDisplayMode(.inline)
                .toolbar { ToolbarItem(placement: .cancellationAction) { Button("Cancel") { scanning = false } } }
            }
        }
        .onAppear {
            relayURL = prefill?.relay ?? settings.relayURL
            manualURL = settings.relayURL
            if let p = prefill { code = p.code }
        }
        .task {
            if let p = prefill { await pair(relay: p.relay, code: p.code) }
        }
    }

    private func pair(relay r: String, code c: String) async {
        busy = true
        error = nil
        defer { busy = false }
        do {
            let phone = try await relay.pair(relay: r, code: c)
            pairedAs = phone.name
            try? await Task.sleep(for: .seconds(0.8)) // a moment to read it
            dismiss()
        } catch {
            self.error = error.localizedDescription
        }
    }
}

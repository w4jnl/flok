// Linking a computer: `flok link join` on a Mac shows a code and its QR; this phone, paired
// already, looks the flok up (its name, machine and flok version, so the right one is let in),
// approves it into its own tenant, and the Mac confirms and links. Scan the QR here or with the
// Camera app (it opens flok), or type the code.
import SwiftUI

struct ApproveView: View {
    @Environment(Relay.self) private var relay
    @Environment(Settings.self) private var settings
    @Environment(\.dismiss) private var dismiss
    /// From a scanned or opened flok://link: the code is filled in and looked up at once.
    var prefill: (relay: String, code: String)?

    @State private var code = ""
    @State private var scanning = false
    @State private var busy = false
    @State private var error: String?
    @State private var found: JoinLookup?
    @State private var approved: String?

    var body: some View {
        Form {
            Section {
                if QRScanner.canScan {
                    Button { scanning = true } label: { Label("Scan the code", systemImage: "qrcode.viewfinder") }
                }
                TextField("AB7Q-K2MN", text: $code)
                    .font(.body.monospaced()).textInputAutocapitalization(.characters).autocorrectionDisabled()
                    .onChange(of: code) { _, v in
                        let f = PairCode.format(v)
                        if f != v { code = f }
                        if found?.pending != nil, approved == nil { found = nil }
                    }
                if found == nil {
                    Button {
                        Task { await lookup(code) }
                    } label: {
                        if busy { ProgressView() } else { Text("Look it up") }
                    }
                    .disabled(busy || !PairCode.isComplete(code))
                }
                if let error {
                    Text(error).font(.footnote).foregroundStyle(Palette.orange)
                }
            } header: {
                Text("Link a computer")
            } footer: {
                Text("Run `flok link join <relay>` on the Mac: it shows a code and its QR, good for five minutes. Look the code up here, check it is the machine you expect, and approve it into your tenant (\(settings.pairedTenant ?? "the floks this phone sees")). The Mac then shows who approved it and links.")
            }
            if let found {
                Section {
                    LabeledContent("Computer", value: found.pending.name)
                    if let h = found.pending.hostname, !h.isEmpty { LabeledContent("Machine", value: h) }
                    if let v = found.pending.version, !v.isEmpty { LabeledContent("flok", value: v) }
                    if let f = found.pending.from, !f.isEmpty { LabeledContent("From", value: f) }
                    if let approved {
                        Label(approved, systemImage: "checkmark.circle").foregroundStyle(Palette.green)
                    } else {
                        Button {
                            Task { await approve(code) }
                        } label: {
                            if busy { ProgressView() } else { Text("Approve into \(found.tenant?.name ?? settings.pairedTenant ?? "your tenant")") }
                        }
                        .disabled(busy)
                    }
                } header: {
                    Text("Waiting to join")
                } footer: {
                    Text("Approve only a machine you set up yourself: it sees nothing of yours, but its agents reach this phone.")
                }
            }
        }
        .navigationTitle("Link a computer")
        .navigationBarTitleDisplayMode(.inline)
        .toolbar { ToolbarItem(placement: .cancellationAction) { Button("Close") { dismiss() } } }
        .fullScreenCover(isPresented: $scanning) {
            NavigationStack {
                QRScanner(parse: FlokLink.linking) { r, c in
                    scanning = false
                    code = c
                    Task { await lookup(c, relay: r) }
                }
                .ignoresSafeArea()
                .navigationTitle("Scan the code")
                .navigationBarTitleDisplayMode(.inline)
                .toolbar { ToolbarItem(placement: .cancellationAction) { Button("Cancel") { scanning = false } } }
            }
        }
        .onAppear {
            if let p = prefill { code = p.code }
        }
        .task {
            if let p = prefill { await lookup(p.code, relay: p.relay) }
        }
    }

    /// The relay a scanned code names must be this phone's: a code for another relay would be
    /// looked up on the wrong one.
    private func sameRelay(_ r: String?) -> String? {
        guard let r, let scanned = RelayURL.base(r), let mine = settings.baseURL, scanned != mine else { return nil }
        return "that code is for \(scanned.host() ?? r); this phone is paired with \(mine.host() ?? settings.relayURL)"
    }

    private func lookup(_ c: String, relay r: String? = nil) async {
        if let why = sameRelay(r) {
            error = why
            return
        }
        busy = true
        error = nil
        found = nil
        approved = nil
        defer { busy = false }
        do {
            found = try await relay.lookupJoin(code: c)
        } catch {
            self.error = error.localizedDescription
        }
    }

    private func approve(_ c: String) async {
        busy = true
        error = nil
        defer { busy = false }
        do {
            let r = try await relay.approveJoin(code: c)
            found = r
            approved = "approved: \(r.pending.name) links once it confirms there"
        } catch {
            self.error = error.localizedDescription
        }
    }
}

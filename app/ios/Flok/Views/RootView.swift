import SwiftUI

struct RootView: View {
    @Environment(Relay.self) private var relay
    @Environment(Settings.self) private var settings
    @State private var path: [Target] = []
    @State private var showSettings = false
    @State private var showPair = false
    @State private var pairRequest: (relay: String, code: String)?
    @State private var showApprove = false
    @State private var approveRequest: (relay: String, code: String)?

    var body: some View {
        NavigationStack(path: $path) {
            InstancesView(showSettings: $showSettings, showPair: $showPair)
                .navigationDestination(for: Target.self) { AgentView(target: $0) }
        }
        .sheet(isPresented: $showSettings) {
            NavigationStack { SettingsView() }
        }
        .sheet(isPresented: $showPair, onDismiss: { pairRequest = nil }) {
            NavigationStack { PairView(prefill: pairRequest) }
        }
        .sheet(isPresented: $showApprove, onDismiss: { approveRequest = nil }) {
            NavigationStack { ApproveView(prefill: approveRequest) }
        }
        .onAppear {
            if !settings.isConfigured { showPair = true }
            relay.connect()
            relay.expireMutes()
            // development: `simctl launch … -open Home:%12` opens a pane straight away
            if let o = UserDefaults.standard.string(forKey: "open"), let i = o.firstIndex(of: ":") {
                path = [Target(instance: String(o[..<i]), pane: String(o[o.index(after: i)...]))]
            }
        }
        .onOpenURL { url in // the Live Activity: flok://agent?instance=…&pane=…; the pairing QR: flok://pair?…; a Mac's join QR: flok://link?…
            if let p = FlokLink.pairing(url) {
                pairRequest = p
                showSettings = false
                showPair = true
            } else if let l = FlokLink.linking(url) {
                approveRequest = l
                showSettings = false
                showPair = false
                showApprove = true
            } else if let t = FlokLink.target(url) {
                relay.pendingOpen = Target(instance: t.instance, pane: t.pane)
            } else {
                showSettings = false
                path = []
            }
        }
        .onChange(of: relay.pendingOpen) { _, t in
            guard let t else { return }
            showSettings = false
            path = [t]
            relay.pendingOpen = nil
        }
    }
}

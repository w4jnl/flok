import SwiftUI

struct RootView: View {
    @Environment(Relay.self) private var relay
    @Environment(Settings.self) private var settings
    @State private var path: [Target] = []
    @State private var showSettings = false

    var body: some View {
        NavigationStack(path: $path) {
            InstancesView(showSettings: $showSettings)
                .navigationDestination(for: Target.self) { AgentView(target: $0) }
        }
        .sheet(isPresented: $showSettings) {
            NavigationStack { SettingsView() }
        }
        .onAppear {
            if !settings.isConfigured { showSettings = true }
            relay.connect()
            // development: `simctl launch … -open Home:%12` opens a pane straight away
            if let o = UserDefaults.standard.string(forKey: "open"), let i = o.firstIndex(of: ":") {
                path = [Target(instance: String(o[..<i]), pane: String(o[o.index(after: i)...]))]
            }
        }
        .onOpenURL { url in // the Live Activity: flok://agent?instance=…&pane=…
            if let t = FlokLink.target(url) {
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

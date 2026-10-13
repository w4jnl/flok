// Every instance with its agents, the ones that need you first when the filter is on.
import SwiftUI

struct InstancesView: View {
    @Environment(Relay.self) private var relay
    @Environment(Settings.self) private var settings
    @Binding var showSettings: Bool
    @State private var needsYouOnly = false

    var body: some View {
        List {
            if case .failed(let why) = relay.state {
                Label(why, systemImage: "wifi.exclamationmark").foregroundStyle(.secondary).font(.footnote)
            } else if relay.state == .connecting && relay.instances.isEmpty {
                Label("connecting…", systemImage: "antenna.radiowaves.left.and.right").foregroundStyle(.secondary).font(.footnote)
            }
            ForEach(relay.instances) { inst in
                Section {
                    let agents = rows(inst)
                    if agents.isEmpty {
                        Text(needsYouOnly ? "nothing waiting" : "no agents").foregroundStyle(.secondary).font(.footnote)
                    }
                    ForEach(agents) { a in
                        NavigationLink(value: Target(instance: inst.name, pane: a.paneRef)) {
                            AgentRow(agent: a, muted: settings.notificationPrefs.mute(instance: inst.name, host: a.host, agent: a.name) != nil)
                        }
                    }
                } header: {
                    InstanceHeader(instance: inst)
                }
            }
        }
        .listStyle(.insetGrouped)
        .overlay {
            if !settings.isConfigured {
                ContentUnavailableView {
                    Label("Set up the relay", systemImage: "antenna.radiowaves.left.and.right")
                } description: {
                    Text("The relay's address and a device token connect this phone to your flok instances.")
                } actions: {
                    Button("Settings") { showSettings = true }.buttonStyle(.borderedProminent)
                }
            } else if relay.instances.isEmpty, relay.state == .connected {
                ContentUnavailableView("No instance has linked in yet", systemImage: "desktopcomputer",
                                       description: Text("A flok with [link] url and token set shows up here as soon as it connects."))
            }
        }
        .navigationTitle("flok")
        .toolbar {
            ToolbarItem(placement: .topBarLeading) {
                Toggle(isOn: $needsYouOnly) { Label("Needs you", systemImage: "bell.badge") }
                    .toggleStyle(.button)
            }
            ToolbarItem(placement: .topBarTrailing) {
                Button { showSettings = true } label: { Label("Settings", systemImage: "gearshape") }
            }
        }
        .refreshable { await relay.refresh() }
    }

    private func rows(_ inst: InstanceState) -> [Agent] {
        var agents = inst.agents
        if needsYouOnly {
            agents = agents.filter { $0.state == .blocked || $0.unseen > 0 }
            agents.sort { ($0.state.priority, $0.name) < ($1.state.priority, $1.name) }
        }
        return agents
    }
}

struct InstanceHeader: View {
    let instance: InstanceState

    var body: some View {
        HStack {
            Circle().fill(instance.online ? Palette.green : Palette.comment).frame(width: 8, height: 8)
            Text(instance.name).font(.headline).textCase(nil)
            if let h = instance.hostname, !h.isEmpty, h != instance.name {
                Text(h.split(separator: ".").first.map(String.init) ?? h).foregroundStyle(.secondary).textCase(nil)
            }
            Spacer()
            if instance.online {
                let n = instance.agents.count
                Text(instance.unseen > 0 ? "\(n) · \(instance.unseen) waiting" : "\(n) agents").foregroundStyle(instance.unseen > 0 ? Palette.orange : .secondary).textCase(nil)
            } else {
                Text("offline since \(instance.since.formatted(date: .omitted, time: .shortened))").foregroundStyle(.secondary).textCase(nil)
            }
        }
        .font(.subheadline)
    }
}

struct AgentRow: View {
    let agent: Agent
    var muted = false
    @State private var now = Date()
    private let tick = Timer.publish(every: 1, on: .main, in: .common).autoconnect()

    var body: some View {
        HStack(alignment: .firstTextBaseline, spacing: 10) {
            StateGlyph(state: agent.state)
                .foregroundStyle(StateStyle.color(agent.state))
                .font(.system(.body, design: .monospaced))
                .frame(width: 16)
            VStack(alignment: .leading, spacing: 2) {
                HStack(spacing: 6) {
                    if let h = agent.host, !h.isEmpty {
                        Text(h).foregroundStyle(Palette.purple).font(.caption)
                    }
                    Text(agent.name).fontWeight(agent.unseen > 0 ? .semibold : .regular)
                    if muted {
                        Image(systemName: "bell.slash").font(.caption2).foregroundStyle(.secondary).accessibilityLabel("muted")
                    }
                }
                HStack(spacing: 6) {
                    Text(StateStyle.label(agent)).foregroundStyle(StateStyle.color(agent.state))
                    if let s = agent.sessionName, !s.isEmpty {
                        Text("· \(s)").foregroundStyle(.secondary)
                    }
                }
                .font(.footnote)
                .lineLimit(1)
            }
            Spacer()
            VStack(alignment: .trailing, spacing: 2) {
                if agent.unseen > 0 {
                    Text("\(agent.unseen)")
                        .font(.caption2.bold())
                        .padding(.horizontal, 6).padding(.vertical, 2)
                        .background(Palette.orange, in: Capsule())
                        .foregroundStyle(.black)
                }
                if agent.state == .working || agent.state == .blocked {
                    Text(elapsed(since: agent.stateSince, now: now)).font(.caption2).foregroundStyle(.secondary).monospacedDigit()
                }
            }
        }
        .onReceive(tick) { now = $0 }
    }
}

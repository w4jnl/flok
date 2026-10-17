// Every instance with its agents; the filter in the corner narrows them to the ones that need
// you, or the ones doing something, in attention order.
import Combine
import SwiftUI

struct InstancesView: View {
    @Environment(Relay.self) private var relay
    @Environment(Settings.self) private var settings
    @Binding var showSettings: Bool
    @Binding var showPair: Bool
    @AppStorage("agentFilter") private var filterRaw = 0
    private var filter: AgentFilter { AgentFilter(rawValue: filterRaw) }

    /// One switch of the filter as a binding for the menu's checkmarks.
    private func switched(_ flag: AgentFilter) -> Binding<Bool> {
        Binding(get: { filter.contains(flag) }, set: { on in
            var f = filter
            if on { f.insert(flag) } else { f.remove(flag) }
            filterRaw = f.rawValue
        })
    }

    var body: some View {
        List {
            if relay.state == .forgotten {
                HStack {
                    Label(relay.state.label, systemImage: "iphone.slash").foregroundStyle(.secondary).font(.footnote)
                    Spacer()
                    Button("Pair") { showPair = true }.buttonStyle(.borderedProminent).controlSize(.small)
                }
            } else if case .failed(let why) = relay.state {
                Label(why, systemImage: "wifi.exclamationmark").foregroundStyle(.secondary).font(.footnote)
            } else if relay.state == .connecting && relay.instances.isEmpty {
                Label("connecting…", systemImage: "antenna.radiowaves.left.and.right").foregroundStyle(.secondary).font(.footnote)
            }
            ForEach(relay.instances) { inst in
                Section {
                    let agents = rows(inst)
                    if agents.isEmpty {
                        Text(filter.emptyText).foregroundStyle(.secondary).font(.footnote)
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
                    Label("Pair this phone", systemImage: "qrcode")
                } description: {
                    Text("Run `flok phone pair` on a Mac that runs flok and scan the code it shows: this phone gets its own key to your relay.")
                } actions: {
                    Button("Pair") { showPair = true }.buttonStyle(.borderedProminent)
                    Button("Settings") { showSettings = true }
                }
            } else if relay.instances.isEmpty, relay.state == .connected {
                ContentUnavailableView("No instance has linked in yet", systemImage: "desktopcomputer",
                                       description: Text("A flok with [link] url and token set shows up here as soon as it connects."))
            }
        }
        .navigationTitle("flok")
        .toolbar {
            ToolbarItem(placement: .topBarLeading) {
                Menu {
                    Toggle(isOn: switched(.needsYou)) { Label("Needs you", systemImage: "bell.badge") }
                    Toggle(isOn: switched(.active)) { Label("Active", systemImage: "bolt") }
                    Divider()
                    Button { filterRaw = 0 } label: { Label("All agents", systemImage: "line.3.horizontal.decrease") }
                        .disabled(filter.isEmpty)
                } label: {
                    Label(filter.title, systemImage: filter.icon)
                        .symbolVariant(filter.isEmpty ? .none : .fill)
                }
                .accessibilityLabel("Filter: \(filter.title)")
            }
            ToolbarItem(placement: .topBarTrailing) {
                Button { showSettings = true } label: { Label("Settings", systemImage: "gearshape") }
            }
        }
        .refreshable { await relay.refresh() }
    }

    private func rows(_ inst: InstanceState) -> [Agent] { filter.apply(inst.agents) }
}

struct InstanceHeader: View {
    let instance: InstanceState

    var body: some View {
        HStack {
            if instance.switchedOff { // a ring, like the sidebar's mark for a link switched off
                Circle().strokeBorder(Palette.comment, lineWidth: 1.5).frame(width: 8, height: 8)
            } else {
                Circle().fill(instance.online ? Palette.green : Palette.comment).frame(width: 8, height: 8)
            }
            Text(instance.name).font(.headline).textCase(nil)
            if let h = instance.hostname, !h.isEmpty, h != instance.name {
                Text(h.split(separator: ".").first.map(String.init) ?? h).foregroundStyle(.secondary).textCase(nil)
            }
            Spacer()
            if instance.online {
                let n = instance.agents.count
                Text(instance.unseen > 0 ? "\(n) · \(instance.unseen) waiting" : "\(n) agents").foregroundStyle(instance.unseen > 0 ? Palette.orange : .secondary).textCase(nil)
            } else {
                Text("\(instance.switchedOff ? "off" : "offline") since \(instance.since.formatted(date: .omitted, time: .shortened))").foregroundStyle(.secondary).textCase(nil)
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

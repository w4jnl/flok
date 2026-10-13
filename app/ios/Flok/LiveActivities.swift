// The app's side of the Live Activity: it hands the relay the tokens (push-to-start, and each
// running activity's update token) so the relay can start, update and end them while the app
// sleeps, starts the overview itself when it is open and something works or waits, and
// follows one agent on request. The relay decides what the activities show (relay/live.go);
// the app's local start uses the same rules so the first frame is right.
import ActivityKit
import Foundation

struct ActivityReport: Encodable, Sendable {
    var id: String
    var token: String
    var kind: String
    var instance: String?
    var pane: String?
}

struct ActivityBody: Encodable, Sendable {
    var startToken: String?
    var activities: [ActivityReport]
}

@MainActor
final class LiveActivities {
    static let shared = LiveActivities()

    weak var relay: Relay?
    private var startToken: String?
    private var tokens: [String: String] = [:] // activity id → its update token
    private var watched: Set<String> = []
    private var reporting: Task<Void, Never>?

    var enabled: Bool { ActivityAuthorizationInfo().areActivitiesEnabled }

    func setup(relay: Relay) {
        self.relay = relay
        if #available(iOS 17.2, *) {
            Task {
                for await data in Activity<FlokActivityAttributes>.pushToStartTokenUpdates {
                    self.startToken = data.hex
                    self.reportSoon()
                }
            }
        }
        Task {
            for await a in Activity<FlokActivityAttributes>.activityUpdates { self.watch(a) } // started by a push too
        }
        for a in Activity<FlokActivityAttributes>.activities { watch(a) }
    }

    private func watch(_ a: Activity<FlokActivityAttributes>) {
        guard watched.insert(a.id).inserted else { return }
        let id = a.id
        Task {
            for await data in a.pushTokenUpdates {
                self.tokens[id] = data.hex
                self.reportSoon()
            }
        }
        Task {
            for await st in a.activityStateUpdates where st == .ended || st == .dismissed {
                self.tokens[id] = nil
                self.watched.remove(id)
                self.reportSoon()
            }
        }
    }

    /// Sends the tokens a moment later (a start and its token arrive close together).
    func reportSoon() {
        reporting?.cancel()
        reporting = Task {
            try? await Task.sleep(for: .milliseconds(300))
            guard !Task.isCancelled else { return }
            await report()
        }
    }

    func report() async {
        let list = Activity<FlokActivityAttributes>.activities.compactMap { a -> ActivityReport? in
            guard a.activityState == .active || a.activityState == .stale, let token = tokens[a.id] else { return nil }
            let agent = a.attributes.kind == "agent"
            return ActivityReport(id: a.id, token: token, kind: a.attributes.kind, instance: agent ? a.attributes.instance : nil, pane: agent ? a.attributes.pane : nil)
        }
        await relay?.reportActivities(start: startToken, activities: list)
    }

    // MARK: the overview

    private var overviews: [Activity<FlokActivityAttributes>] {
        Activity<FlokActivityAttributes>.activities.filter { $0.attributes.kind == "overview" && ($0.activityState == .active || $0.activityState == .stale) }
    }

    /// With the app open: starts the overview when something works or waits and none runs, or
    /// ends it when the user switched it off.
    func ensureOverview(_ instances: [InstanceState], allowed: Bool) {
        if !allowed {
            for id in overviews.map(\.id) { Task { await Self.end(id) } }
            return
        }
        let st = Self.overview(instances)
        guard enabled, overviews.isEmpty, st.working + st.waiting > 0 else { return }
        if let a = try? Activity<FlokActivityAttributes>.request(attributes: FlokActivityAttributes.overview, content: .init(state: st, staleDate: nil), pushType: .token) {
            watch(a)
        }
    }

    /// The relay's overview (relay/live.go overviewLocked), for the first frame of a local start.
    nonisolated static func overview(_ instances: [InstanceState]) -> FlokActivityAttributes.ContentState {
        var st = FlokActivityAttributes.ContentState.quiet
        var ranked: [(rank: Int, row: LiveRow)] = []
        for inst in instances where inst.online {
            for a in inst.agents {
                switch a.state {
                case .blocked:
                    st.waiting += 1
                    ranked.append((0, row(inst.name, a)))
                case .done where a.unseen > 0:
                    st.done += 1
                    ranked.append((1, row(inst.name, a)))
                case .working:
                    st.working += 1
                    ranked.append((2, row(inst.name, a)))
                default:
                    break
                }
            }
        }
        ranked.sort { x, y in
            if x.rank != y.rank { return x.rank < y.rank }
            if x.row.since != y.row.since { return x.rank == 1 ? x.row.since > y.row.since : x.row.since < y.row.since }
            return x.row.id < y.row.id
        }
        st.rows = ranked.prefix(3).map(\.row)
        return st
    }

    nonisolated static func row(_ instance: String, _ a: Agent) -> LiveRow {
        let state: String, label: String
        switch a.state {
        case .working: (state, label) = ("working", "working")
        case .blocked: (state, label) = ("blocked", StateStyle.label(a))
        case .done: (state, label) = ("done", a.reason == "error" ? "failed" : "done")
        default: (state, label) = ("idle", a.state.rawValue)
        }
        return LiveRow(instance: instance, host: a.host ?? "", agent: a.name, pane: a.paneRef, state: state, label: label,
                       since: a.stateSince.map { $0.timeIntervalSince1970.rounded(.down) } ?? 0)
    }

    // MARK: one agent

    func following(_ t: Target) -> Bool {
        Activity<FlokActivityAttributes>.activities.contains { $0.attributes.kind == "agent" && $0.attributes.instance == t.instance && $0.attributes.pane == t.pane && $0.activityState == .active }
    }

    func follow(_ t: Target, instance: InstanceState, agent a: Agent) throws {
        let r = Self.row(instance.name, a)
        let st = FlokActivityAttributes.ContentState(working: r.state == "working" ? 1 : 0, waiting: r.state == "blocked" ? 1 : 0, done: r.state == "done" ? 1 : 0, rows: [r])
        let attrs = FlokActivityAttributes(kind: "agent", instance: t.instance, pane: t.pane, agent: a.name)
        let act = try Activity<FlokActivityAttributes>.request(attributes: attrs, content: .init(state: st, staleDate: nil), pushType: .token)
        watch(act)
    }

    func unfollow(_ t: Target) {
        for a in Activity<FlokActivityAttributes>.activities where a.attributes.kind == "agent" && a.attributes.instance == t.instance && a.attributes.pane == t.pane {
            let id = a.id
            Task { await Self.end(id) }
        }
    }

    /// Ends an activity by id; looked up off the main actor, because an Activity is not Sendable.
    nonisolated private static func end(_ id: String) async {
        for a in Activity<FlokActivityAttributes>.activities where a.id == id {
            await a.end(nil, dismissalPolicy: .immediate)
        }
    }
}

private extension Data {
    var hex: String { map { String(format: "%02x", $0) }.joined() }
}

// The relay client: one WebSocket for the live view (instances, events, screens), HTTP for
// what the user does (answer, seen, device registration). Everything runs on the main actor;
// the socket's awaits suspend, nothing blocks.
import Foundation
import Observation
import UIKit
import UserNotifications

/// An agent pane on an instance, the app's navigation unit.
struct Target: Hashable, Sendable {
    var instance: String
    var pane: String // pane ref: %12, beta:%12
}

struct EventRecord: Identifiable, Sendable {
    var instance: String
    var event: AgentEvent
    var id: String { instance + " " + event.id }
}

@MainActor
@Observable
final class Relay {
    enum State: Equatable {
        case off, connecting, connected
        case failed(String)

        var label: String {
            switch self {
            case .off: "not configured"
            case .connecting: "connecting"
            case .connected: "connected"
            case .failed(let why): why
            }
        }
    }

    static let shared = Relay(settings: .shared)

    let settings: Settings
    private(set) var state: State = .off
    private(set) var instances: [InstanceState] = []
    private(set) var events: [EventRecord] = []
    private(set) var screens: [Target: String] = [:]
    /// Set by a notification tap: the view navigates there and clears it.
    var pendingOpen: Target?
    /// The last failure to hand the notification settings to the relay; nil once they got there.
    private(set) var prefsError: String?
    private var prefsSync: Task<Void, Never>?
    private var muteExpiry: Task<Void, Never>?

    private var session: URLSession
    private var task: URLSessionWebSocketTask?
    private var generation = 0
    private var attempt = 0
    private var subscriptions: Set<Target> = []
    private var reconnect: Task<Void, Never>?

    init(settings: Settings) {
        self.settings = settings
        let cfg = URLSessionConfiguration.default
        cfg.waitsForConnectivity = true
        cfg.timeoutIntervalForRequest = 20
        session = URLSession(configuration: cfg)
    }

    var totalUnseen: Int { instances.reduce(0) { $0 + $1.unseen } }

    func agent(_ t: Target) -> (InstanceState, Agent)? {
        guard let inst = instances.first(where: { $0.name == t.instance }),
              let a = inst.agents.first(where: { $0.paneRef == t.pane }) else { return nil }
        return (inst, a)
    }

    // MARK: connection

    /// Starts (or restarts) the live stream.
    func connect() {
        disconnect()
        guard settings.isConfigured, let base = settings.baseURL else {
            state = .off
            return
        }
        generation += 1
        let gen = generation
        state = .connecting
        Task { await run(gen: gen, base: base) }
    }

    func disconnect() {
        generation += 1
        reconnect?.cancel()
        reconnect = nil
        task?.cancel(with: .goingAway, reason: nil)
        task = nil
    }

    private func run(gen: Int, base: URL) async {
        var req = URLRequest(url: RelayURL.websocket(base))
        req.setValue("Bearer \(settings.deviceToken)", forHTTPHeaderField: "Authorization")
        let ws = session.webSocketTask(with: req)
        ws.maximumMessageSize = 16 << 20
        task = ws
        ws.resume()
        let pinger = Task { [weak self] in
            while !Task.isCancelled {
                try? await Task.sleep(for: .seconds(20))
                guard !Task.isCancelled, let self, self.generation == gen else { return }
                ws.sendPing { _ in }
            }
        }
        defer { pinger.cancel() }
        var failure = "connection closed"
        do {
            while generation == gen {
                let msg = try await ws.receive()
                guard generation == gen else { return }
                let data: Data
                switch msg {
                case .data(let d): data = d
                case .string(let s): data = Data(s.utf8)
                @unknown default: continue
                }
                if state != .connected {
                    state = .connected
                    attempt = 0
                    for t in subscriptions { send(SubscribeFrame(instance: t.instance, pane: t.pane, on: true)) }
                }
                if let frame = try? Wire.decoder.decode(Frame.self, from: data) {
                    handle(frame)
                }
            }
        } catch {
            if let code = (ws.response as? HTTPURLResponse)?.statusCode, code == 401 || code == 403 {
                failure = "the relay refused the token: it needs one of its device_tokens (an instance's [link] token is a different one)"
            } else if let code = (ws.response as? HTTPURLResponse)?.statusCode, code != 101 {
                failure = "relay answered \(code)"
            } else {
                failure = (error as? URLError)?.localizedDescription ?? error.localizedDescription
            }
        }
        guard generation == gen else { return }
        task = nil
        state = .failed(failure)
        let delay = min(30.0, pow(2.0, Double(min(attempt, 5))))
        attempt += 1
        reconnect = Task { [weak self] in
            try? await Task.sleep(for: .seconds(delay))
            guard !Task.isCancelled, let self, self.generation == gen else { return }
            self.connect()
        }
    }

    private func handle(_ f: Frame) {
        switch f.type {
        case "instances":
            instances = (f.instances ?? []).sorted { $0.name < $1.name }
            updateBadge()
        case "instance":
            guard let inst = f.inst else { return }
            if let i = instances.firstIndex(where: { $0.name == inst.name }) {
                instances[i] = inst
            } else {
                instances.append(inst)
                instances.sort { $0.name < $1.name }
            }
            updateBadge()
        case "event":
            guard let ev = f.event, let name = f.instance else { return }
            events.insert(EventRecord(instance: name, event: ev), at: 0)
            if events.count > 100 { events.removeLast(events.count - 100) }
        case "screen":
            guard let sc = f.screen, let name = f.instance else { return }
            screens[Target(instance: name, pane: sc.pane)] = sc.text
        default:
            break
        }
    }

    private func send<T: Encodable>(_ v: T) {
        guard let task, let data = try? Wire.encoder.encode(v), let s = String(data: data, encoding: .utf8) else { return }
        task.send(.string(s)) { _ in }
    }

    /// The badge, and the notifications of agents that no longer wait on the user (seen or
    /// answered anywhere) taken off the Lock Screen; instances without a snapshot are left alone.
    private func updateBadge() {
        let n = totalUnseen
        let waiting = Self.waitingKeys(instances)
        let judged = Set(instances.filter { $0.snap != nil }.map(\.name))
        Task {
            try? await UNUserNotificationCenter.current().setBadgeCount(n)
            await Notifications.clearDelivered(waiting: waiting, judged: judged)
        }
        if UIApplication.shared.applicationState == .active {
            LiveActivities.shared.ensureOverview(instances, allowed: settings.notificationPrefs.showsActivity)
        }
    }

    /// Hands the relay this phone's Live Activity tokens (sent whole); before the phone
    /// registered there is no record to put them on, and the registration sends them after.
    func reportActivities(start: String?, activities: [ActivityReport]) async {
        guard let token = Self.registeredToken(settings.registered, relayURL: settings.relayURL) else { return }
        try? await call("PUT", "/api/devices/\(token)/activity", body: Wire.encoder.encode(ActivityBody(startToken: start, activities: activities)))
    }

    /// "<instance>:<pane>" of every agent that waits on the user: unseen, or blocked (a block
    /// stays until it is answered, seen or not). The relay's sync push uses the same names.
    nonisolated static func waitingKeys(_ instances: [InstanceState]) -> Set<String> {
        var out = Set<String>()
        for inst in instances {
            for a in inst.agents where a.unseen > 0 || a.state == .blocked {
                out.insert(inst.name + ":" + a.paneRef)
            }
        }
        return out
    }

    // MARK: screens

    func subscribe(_ t: Target) {
        subscriptions.insert(t)
        if state == .connected { send(SubscribeFrame(instance: t.instance, pane: t.pane, on: true)) }
    }

    func unsubscribe(_ t: Target) {
        subscriptions.remove(t)
        screens[t] = nil
        if state == .connected { send(SubscribeFrame(instance: t.instance, pane: t.pane, on: false)) }
    }

    // MARK: commands (HTTP, so a lock-screen action works without the socket)

    private func post<T: Encodable>(_ path: String, _ body: T) async throws {
        try await call("POST", path, body: Wire.encoder.encode(body))
    }

    private func call(_ method: String, _ path: String, body: Data? = nil) async throws {
        guard let api = RelayAPI(relayURL: settings.relayURL, token: settings.deviceToken) else { throw RelayError.notConfigured }
        try await api.send(method, path, body: body, session: session)
    }

    /// Types into an agent pane: text first, then the keys; a file is saved on the agent's
    /// machine and its path typed after the text. The relay waits for the instance to confirm,
    /// so a thrown error means it did not happen.
    func answer(_ t: Target, text: String? = nil, keys: [String]? = nil, file: AttachedFile? = nil) async throws {
        try await post("/api/answer", AnswerBody(instance: t.instance, pane: t.pane, text: text?.isEmpty == false ? text : nil, keys: keys?.isEmpty == false ? keys : nil, file: file))
    }

    func markSeen(_ t: Target) async throws {
        try await post("/api/seen", SeenBody(instance: t.instance, pane: t.pane))
    }

    func registerDevice(apnsToken: String) async throws {
        let sandbox: Bool
        #if DEBUG
        sandbox = true
        #else
        sandbox = false
        #endif
        var prefs = settings.notificationPrefs
        prefs.prune()
        try await post("/api/devices", DeviceBody(token: apnsToken, name: UIDevice.current.name, sandbox: sandbox, prefs: prefs))
        prefsError = nil
        let before = settings.registered
        settings.registered = "\(settings.relayURL)|\(apnsToken)"
        LiveActivities.shared.reportSoon() // the relay knows the phone now: its activity tokens follow
        // The token this app registered before (an Xcode build that TestFlight replaced, a phone
        // restored from a backup) would stay in the relay's list until a push to it fails.
        if let old = Self.replacedToken(before, relayURL: settings.relayURL, now: apnsToken) {
            try? await call("DELETE", "/api/devices/\(old)")
        }
    }

    /// The token of an earlier registration with this relay that `now` replaces; nil when there
    /// is none, it was another relay's (whose list this device token cannot reach), or the same.
    nonisolated static func replacedToken(_ registered: String?, relayURL: String, now: String) -> String? {
        guard let token = registeredToken(registered, relayURL: relayURL), token != now else { return nil }
        return token
    }

    /// The push token this phone registered with a relay, from a `registered` record.
    nonisolated static func registeredToken(_ registered: String?, relayURL: String) -> String? {
        guard let registered, let bar = registered.lastIndex(of: "|") else { return nil }
        let url = String(registered[..<bar]), token = String(registered[registered.index(after: bar)...])
        guard url == relayURL, !token.isEmpty else { return nil }
        return token
    }

    // MARK: notification settings

    /// Changes the notification settings and hands them to the relay a moment later (a run of
    /// toggles is one request).
    func updatePrefs(_ change: (inout NotificationPrefs) -> Void) {
        change(&settings.notificationPrefs)
        expireMutes()
        prefsSync?.cancel()
        prefsSync = Task {
            try? await Task.sleep(for: .milliseconds(600))
            guard !Task.isCancelled else { return }
            await syncPrefs()
        }
    }

    /// Changes the settings and sends them at once: a lock-screen action waits for it.
    func updatePrefsNow(_ change: (inout NotificationPrefs) -> Void) async {
        change(&settings.notificationPrefs)
        expireMutes()
        prefsSync?.cancel()
        await syncPrefs()
    }

    /// Drops the mutes whose time is up and wakes at the next one's end to drop it too, so a
    /// screen showing "muted" turns back when the mute ends (the relay ignores an ended mute by
    /// itself). Called at start, on every change and when the app comes to the front.
    func expireMutes() {
        if settings.notificationPrefs.hasExpired() { settings.notificationPrefs.prune() }
        muteExpiry?.cancel()
        guard let next = settings.notificationPrefs.nextExpiry() else { return }
        muteExpiry = Task {
            try? await Task.sleep(for: .seconds(max(1, next.timeIntervalSinceNow)))
            guard !Task.isCancelled else { return }
            self.expireMutes()
        }
    }

    /// Sends the settings now. Before the phone registered
    /// there is nothing to update: the registration carries them.
    func syncPrefs() async {
        guard let token = Self.registeredToken(settings.registered, relayURL: settings.relayURL) else { return }
        var prefs = settings.notificationPrefs
        prefs.prune()
        do {
            try await call("PUT", "/api/devices/\(token)/prefs", body: Wire.encoder.encode(prefs))
            prefsError = nil
        } catch RelayError.http(404, "no such device") { // the relay forgot this phone: register again, with them
            settings.registered = nil
            await Notifications.shared.registerIfNeeded()
        } catch {
            prefsError = error.localizedDescription
        }
    }

    /// One fetch of every instance (pull to refresh; the stream keeps it current otherwise).
    func refresh() async {
        guard let base = settings.baseURL, !settings.deviceToken.isEmpty else { return }
        var req = URLRequest(url: RelayURL.api(base, "/api/instances"))
        req.setValue("Bearer \(settings.deviceToken)", forHTTPHeaderField: "Authorization")
        if let (data, resp) = try? await session.data(for: req), (resp as? HTTPURLResponse)?.statusCode == 200,
           let f = try? Wire.decoder.decode(Frame.self, from: data) {
            handle(f)
        }
        if state != .connected { connect() }
    }
}

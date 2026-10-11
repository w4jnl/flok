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

enum RelayError: LocalizedError {
    case notConfigured
    case http(Int, String)
    case offline

    var errorDescription: String? {
        switch self {
        case .notConfigured: "set the relay and the device token first"
        case .http(let code, let msg): msg.isEmpty ? "relay answered \(code)" : msg
        case .offline: "not connected"
        }
    }
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

    private func updateBadge() {
        let n = totalUnseen
        Task { try? await UNUserNotificationCenter.current().setBadgeCount(n) }
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
        guard let base = settings.baseURL, !settings.deviceToken.isEmpty else { throw RelayError.notConfigured }
        var req = URLRequest(url: RelayURL.api(base, path))
        req.httpMethod = "POST"
        req.setValue("Bearer \(settings.deviceToken)", forHTTPHeaderField: "Authorization")
        req.setValue("application/json", forHTTPHeaderField: "Content-Type")
        req.httpBody = try Wire.encoder.encode(body)
        let (data, resp) = try await session.data(for: req)
        let code = (resp as? HTTPURLResponse)?.statusCode ?? 0
        if code != 200 {
            let msg = (try? JSONDecoder().decode([String: String].self, from: data))?["error"] ?? ""
            throw RelayError.http(code, msg)
        }
    }

    /// Types into an agent pane: text first, then the keys. The relay waits for the instance
    /// to confirm, so a thrown error means it did not happen.
    func answer(_ t: Target, text: String? = nil, keys: [String]? = nil) async throws {
        try await post("/api/answer", AnswerBody(instance: t.instance, pane: t.pane, text: text?.isEmpty == false ? text : nil, keys: keys?.isEmpty == false ? keys : nil))
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
        try await post("/api/devices", DeviceBody(token: apnsToken, name: UIDevice.current.name, sandbox: sandbox))
        settings.registered = "\(settings.relayURL)|\(apnsToken)"
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

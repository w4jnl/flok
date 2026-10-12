// What this phone wants pushed, as the relay reads it before every push (app/API.md, Prefs):
// per kind on or off, its sound and whether it breaks through Focus, and the muted agents. The
// app owns them: kept in the preferences, sent whole with the registration and on every change.
import Foundation

enum PushKind: String, CaseIterable, Identifiable, Sendable {
    case permission, question, done, error
    var id: String { rawValue }

    var title: String {
        switch self {
        case .permission: "Permission prompt"
        case .question: "Question or waiting"
        case .done: "Finished"
        case .error: "Failed"
        }
    }

    var glyph: String {
        switch self {
        case .permission, .question: "●"
        case .done: "✓"
        case .error: "✗"
        }
    }

    /// Breaks through Focus unless switched off: the kinds that wait on you.
    var timeSensitiveByDefault: Bool { self == .permission || self == .question }

    /// flok's sound for the kind, bundled with the app (the sidebar's: request, done).
    var flokSound: String { self == .done ? "flok-done" : "flok-request" }
}

enum PushSound: String, CaseIterable, Identifiable, Sendable {
    case flok, system = "default", none
    var id: String { rawValue }

    var title: String {
        switch self {
        case .flok: "flok"
        case .system: "iOS default"
        case .none: "silent"
        }
    }
}

struct NotificationPrefs: Codable, Equatable, Sendable {
    struct KindPrefs: Codable, Equatable, Sendable {
        var off: Bool?
        var sound: String? // flok (nil), "default", "none"
        var timeSensitive: Bool?
    }

    struct Mute: Codable, Equatable, Hashable, Identifiable, Sendable {
        var instance: String
        var host: String?
        var agent: String
        var until: Date? // nil: until unmuted
        var id: String { [instance, host ?? "", agent].joined(separator: "\u{1f}") }

        /// "Home / macmini · docker" without the session: what a mute matches.
        var place: String { instance + ((host ?? "").isEmpty ? "" : " / " + (host ?? "")) }
    }

    var kinds: [String: KindPrefs] = [:]
    var mutes: [Mute] = []

    // MARK: per kind

    func isOn(_ k: PushKind) -> Bool { kinds[k.rawValue]?.off != true }

    mutating func setOn(_ k: PushKind, _ on: Bool) {
        kinds[k.rawValue, default: KindPrefs()].off = on ? nil : true
        tidy(k)
    }

    func sound(_ k: PushKind) -> PushSound { PushSound(rawValue: kinds[k.rawValue]?.sound ?? "flok") ?? .flok }

    mutating func setSound(_ k: PushKind, _ s: PushSound) {
        kinds[k.rawValue, default: KindPrefs()].sound = s == .flok ? nil : s.rawValue
        tidy(k)
    }

    func timeSensitive(_ k: PushKind) -> Bool { kinds[k.rawValue]?.timeSensitive ?? k.timeSensitiveByDefault }

    mutating func setTimeSensitive(_ k: PushKind, _ on: Bool) {
        kinds[k.rawValue, default: KindPrefs()].timeSensitive = on == k.timeSensitiveByDefault ? nil : on
        tidy(k)
    }

    /// A kind back at its defaults is dropped, so the relay's record stays short.
    private mutating func tidy(_ k: PushKind) {
        if kinds[k.rawValue] == KindPrefs() { kinds[k.rawValue] = nil }
    }

    // MARK: mutes

    func mute(instance: String, host: String?, agent: String, now: Date = .now) -> Mute? {
        mutes.first { $0.instance == instance && ($0.host ?? "") == (host ?? "") && $0.agent == agent && ($0.until.map { now < $0 } ?? true) }
    }

    /// Mutes an agent until a time (nil: for good), replacing an earlier mute of it.
    mutating func mute(instance: String, host: String?, agent: String, until: Date?) {
        unmute(instance: instance, host: host, agent: agent)
        mutes.append(Mute(instance: instance, host: (host ?? "").isEmpty ? nil : host, agent: agent, until: until))
    }

    mutating func unmute(instance: String, host: String?, agent: String) {
        mutes.removeAll { $0.instance == instance && ($0.host ?? "") == (host ?? "") && $0.agent == agent }
    }

    /// Drops mutes whose time is up.
    mutating func prune(now: Date = .now) {
        mutes.removeAll { m in m.until.map { $0 <= now } ?? false }
    }

    /// The next morning at 8, for "until tomorrow".
    static func tomorrowMorning(from now: Date = .now, calendar: Calendar = .current) -> Date {
        let start = calendar.startOfDay(for: calendar.date(byAdding: .day, value: 1, to: now) ?? now)
        return calendar.date(byAdding: .hour, value: 8, to: start) ?? start
    }
}

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
    var noActivity: Bool? // true: no flok overview in the Dynamic Island / on the Lock Screen
    /// true: finished and failed agents stay notifications while the overview shows; nil: they
    /// light up the overview instead, one thing on the Lock Screen rather than two.
    var finishedAsNotification: Bool?

    var showsActivity: Bool { noActivity != true }
    var finishedInIsland: Bool { showsActivity && finishedAsNotification != true }

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

    /// Whether a mute's time is up, so prune would change something.
    func hasExpired(now: Date = .now) -> Bool {
        mutes.contains { m in m.until.map { $0 <= now } ?? false }
    }

    /// When the next mute ends; nil when none has an end.
    func nextExpiry(after now: Date = .now) -> Date? {
        mutes.compactMap(\.until).filter { $0 > now }.min()
    }

    /// 8:00 the next morning: today's when it is still more than an hour away (muted after
    /// midnight), else tomorrow's.
    static func nextMorning(from now: Date = .now, calendar: Calendar = .current) -> Date {
        let today = calendar.date(bySettingHour: 8, minute: 0, second: 0, of: now) ?? now
        if today.timeIntervalSince(now) > 3600 { return today }
        return calendar.date(byAdding: .day, value: 1, to: today) ?? today
    }

    /// "8:00", "tomorrow 8:00" or "Wed 8:00": when a mute ends, in words.
    static func untilWords(_ d: Date, now: Date = .now, calendar: Calendar = .current) -> String {
        let time = d.formatted(.dateTime.hour().minute())
        if calendar.isDate(d, inSameDayAs: now) { return time }
        if let tomorrow = calendar.date(byAdding: .day, value: 1, to: now), calendar.isDate(d, inSameDayAs: tomorrow) { return "tomorrow " + time }
        return d.formatted(.dateTime.weekday().hour().minute())
    }
}

/// The phone's registration with the relay (POST /api/devices): its push token, a name, which
/// APNs service the token belongs to, and these settings. Here rather than in Shared/Wire.swift
/// because the extensions do not carry NotificationPrefs.
struct DeviceBody: Encodable, Sendable {
    var token: String
    var name: String
    var sandbox: Bool
    var prefs: NotificationPrefs?
}

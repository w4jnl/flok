// What the app remembers: the relay, the device token, the screen font, the notification
// settings. The simulator can be pointed at a relay without touching the UI: `simctl launch …
// nl.w4j.flok -relayURL https://… -deviceToken …` (launch arguments land in UserDefaults). The
// relay's address and the token are mirrored into the shared keychain group for the expanded
// notification.
import Foundation
import Observation

@MainActor
@Observable
final class Settings {
    static let shared = Settings()

    var relayURL: String { didSet { defaults.set(relayURL, forKey: "relayURL"); Keychain.set(relayURL, for: "relayURL") } }
    var deviceToken: String { didSet { Keychain.set(deviceToken, for: "deviceToken") } }
    var fontSize: Double { didSet { defaults.set(fontSize, forKey: "fontSize") } }
    var wrapLines: Bool { didSet { defaults.set(wrapLines, forKey: "wrapLines") } }
    /// "<relay>|<apns token>" of the last device registration, so it is not repeated at every start.
    var registered: String? { didSet { defaults.set(registered, forKey: "registered") } }
    /// What this phone wants pushed; Relay.updatePrefs changes it and tells the relay.
    var notificationPrefs: NotificationPrefs {
        didSet { defaults.set(try? JSONEncoder().encode(notificationPrefs), forKey: "notificationPrefs") }
    }

    private let defaults = UserDefaults.standard

    init() {
        relayURL = defaults.string(forKey: "relayURL") ?? ""
        deviceToken = Keychain.get("deviceToken") ?? defaults.string(forKey: "deviceToken") ?? ""
        fontSize = defaults.object(forKey: "fontSize") as? Double ?? 11
        wrapLines = defaults.bool(forKey: "wrapLines")
        registered = defaults.string(forKey: "registered")
        notificationPrefs = defaults.data(forKey: "notificationPrefs").flatMap { try? JSONDecoder().decode(NotificationPrefs.self, from: $0) } ?? NotificationPrefs()
        // Saved again at every start: an item from a build before the shared keychain group moves
        // into it, and the extension finds the relay even when only the token was in the keychain.
        Keychain.set(deviceToken, for: "deviceToken")
        Keychain.set(relayURL, for: "relayURL")
    }

    var isConfigured: Bool { baseURL != nil && !deviceToken.isEmpty }
    var baseURL: URL? { RelayURL.base(relayURL) }
}

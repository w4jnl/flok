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
    /// Voice: tidy the dictated text with the on-device model, send it as soon as the button
    /// is released, and the language ("" = the phone's).
    var voiceCleanup: Bool { didSet { defaults.set(voiceCleanup, forKey: "voiceCleanup") } }
    var voiceSendOnRelease: Bool { didSet { defaults.set(voiceSendOnRelease, forKey: "voiceSendOnRelease") } }
    var voiceLocale: String { didSet { defaults.set(voiceLocale, forKey: "voiceLocale") } }
    /// "<relay>|<apns token>" of the last device registration, so it is not repeated at every start.
    var registered: String? { didSet { defaults.set(registered, forKey: "registered") } }
    /// The pairing this phone's key came from (flok phone pair); nil when a device token was typed.
    var pairedPhoneID: String? { didSet { defaults.set(pairedPhoneID, forKey: "pairedPhoneID") } }
    var pairedPhoneName: String? { didSet { defaults.set(pairedPhoneName, forKey: "pairedPhoneName") } }
    /// The tenant the pairing put this phone in (the floks it sees); nil before the relay said.
    var pairedTenant: String? { didSet { defaults.set(pairedTenant, forKey: "pairedTenant") } }
    /// What this phone wants pushed; Relay.updatePrefs changes it and tells the relay.
    var notificationPrefs: NotificationPrefs {
        didSet { defaults.set(try? JSONEncoder().encode(notificationPrefs), forKey: "notificationPrefs") }
    }
    /// The app lock (Settings › Privacy): Face ID before the app shows anything after it was away
    /// longer than the grace, and on a cold start when lockOnLaunch. LockGate applies them.
    var lockEnabled: Bool { didSet { defaults.set(lockEnabled, forKey: "lockEnabled") } }
    var lockGraceSeconds: Double { didSet { defaults.set(lockGraceSeconds, forKey: "lockGraceSeconds") } }
    var lockOnLaunch: Bool { didSet { defaults.set(lockOnLaunch, forKey: "lockOnLaunch") } }
    /// Agent names and screens redacted on the Lock Screen until the phone is unlocked; in the
    /// keychain group, where the Live Activity and the notification card read it.
    var hideDetailsLocked: Bool { didSet { Privacy.setHideDetails(hideDetailsLocked) } }

    private let defaults = UserDefaults.standard

    init() {
        relayURL = defaults.string(forKey: "relayURL") ?? ""
        // -deviceToken on the command line (a scripted start) wins over the keychain's token
        let launched = defaults.volatileDomain(forName: UserDefaults.argumentDomain)["deviceToken"] as? String
        deviceToken = launched ?? Keychain.get("deviceToken") ?? defaults.string(forKey: "deviceToken") ?? ""
        fontSize = defaults.object(forKey: "fontSize") as? Double ?? 11
        wrapLines = defaults.object(forKey: "wrapLines") as? Bool ?? true   // wrap until the user says otherwise
        voiceCleanup = defaults.object(forKey: "voiceCleanup") as? Bool ?? true
        voiceSendOnRelease = defaults.bool(forKey: "voiceSendOnRelease")
        voiceLocale = defaults.string(forKey: "voiceLocale") ?? ""
        registered = defaults.string(forKey: "registered")
        pairedPhoneID = defaults.string(forKey: "pairedPhoneID")
        pairedPhoneName = defaults.string(forKey: "pairedPhoneName")
        pairedTenant = defaults.string(forKey: "pairedTenant")
        notificationPrefs = defaults.data(forKey: "notificationPrefs").flatMap { try? JSONDecoder().decode(NotificationPrefs.self, from: $0) } ?? NotificationPrefs()
        lockEnabled = defaults.object(forKey: "lockEnabled") as? Bool ?? false
        lockGraceSeconds = defaults.object(forKey: "lockGraceSeconds") as? Double ?? LockPolicy.defaultGrace
        lockOnLaunch = defaults.object(forKey: "lockOnLaunch") as? Bool ?? true
        hideDetailsLocked = Privacy.hideDetails()
        // Saved again at every start: an item from a build before the shared keychain group moves
        // into it, and the extension finds the relay even when only the token was in the keychain.
        Keychain.set(deviceToken, for: "deviceToken")
        Keychain.set(relayURL, for: "relayURL")
    }

    var isConfigured: Bool { baseURL != nil && !deviceToken.isEmpty }
    var baseURL: URL? { RelayURL.base(relayURL) }
    var lockGrace: LockGrace { LockGrace(seconds: lockGraceSeconds) }
}

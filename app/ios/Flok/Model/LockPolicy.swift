// The app lock's decisions, pure: when a return to the foreground asks for Face ID, which grace
// periods Settings offers, how much of the phone's own unlock counts, and what the enrolled
// biometry is called. LockGate applies them. The shape is the guide's (../ios-design-and-
// solution-guide.md §3.10), shared with the journal.
import Foundation
import LocalAuthentication

enum LockPolicy {
    /// What Settings › Privacy › Lock after offers, in seconds; 0 is at once.
    static let graces: [(label: String, seconds: Double)] = [
        ("Immediately", 0), ("30 seconds", 30), ("1 minute", 60), ("5 minutes", 300), ("15 minutes", 900), ("1 hour", 3600),
    ]
    static let defaultGrace: Double = 60

    /// Whether the app locks as it becomes active. `launching` is the first activation of this
    /// process, which cannot know how long the app was away: `onLaunch` decides. Later, `since`
    /// is the time since the app went to the background, measured on a clock that keeps counting
    /// while the phone sleeps; nil when it only went inactive (Control Center, a call, the Face
    /// ID sheet itself), which never locks.
    nonisolated static func shouldLock(since: Duration?, grace: Duration, enabled: Bool, onLaunch: Bool, launching: Bool) -> Bool {
        guard enabled else { return false }
        if launching { return onLaunch }
        guard let since else { return false }
        return since >= grace
    }

    /// How long a Face ID or Touch ID unlock of the phone itself, on its Lock Screen, counts as
    /// this app's: within the grace, and never longer than the five minutes iOS allows, so a tap
    /// on a push right after unlocking the phone opens the app without a second glance.
    /// Immediately (grace zero) reuses nothing.
    nonisolated static func reuse(forGrace grace: Double) -> TimeInterval {
        min(max(grace, 0), LATouchIDAuthenticationMaximumAllowableReuseDuration)
    }

    nonisolated static func label(_ biometry: LABiometryType) -> String {
        switch biometry {
        case .faceID: "Face ID"
        case .touchID: "Touch ID"
        case .opticID: "Optic ID"
        case .none: "passcode"
        @unknown default: "passcode"
        }
    }

    nonisolated static func symbol(_ biometry: LABiometryType) -> String {
        switch biometry {
        case .faceID: "faceid"
        case .touchID: "touchid"
        case .opticID: "opticid"
        case .none: "lock.fill"
        @unknown default: "lock.fill"
        }
    }
}

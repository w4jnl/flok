// Whether agent names and screens are hidden on the Lock Screen until the phone is unlocked: one
// flag the app writes (Settings › Privacy) into the keychain group it shares with the content
// extension and the widget, which read it as they render. It rides on `.privacySensitive`, so
// the phone's own Face ID resolves it, with no prompt of its own; the app lock (LockGate) is a
// separate thing, for the app itself. The keychain group is the one shared store the targets
// already have: an App Group would be a capability the developer account has to grant first.
import SwiftUI

enum Privacy {
    static let key = "hideDetailsLocked"

    /// The flag as stored: "1" for on, nothing for off.
    nonisolated static func hideDetails(_ stored: String?) -> Bool { stored == "1" }
    nonisolated static func hideDetails() -> Bool { hideDetails(Keychain.get(key)) }
    nonisolated static func setHideDetails(_ on: Bool) { Keychain.set(on ? "1" : "", for: key) }
}

extension View {
    /// An agent's name, place, label or screen: redacted while the phone is locked when the
    /// user asked for that.
    func hiddenWhenLocked() -> some View { privacySensitive(Privacy.hideDetails()) }
}

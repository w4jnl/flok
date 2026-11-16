// The device token and the relay's address live in the keychain, in the access group the app
// shares with its notification content extension (the first of keychain-access-groups in both
// entitlements, so items go there without naming it): the expanded notification reads them to
// fetch the screen and answer. Everything else the app remembers is a preference.
import Foundation
import Security

enum Keychain {
    static let service = "nl.w4j.flok"

    /// What a read came back with. `locked` is the keychain refusing for now, not an absence:
    /// the items are protected until the phone's first unlock after a restart, and iOS can
    /// launch the app before that (a background push), so nothing may be written over them
    /// then; the same for any other error.
    enum Read: Equatable {
        case found(String)
        case missing
        case locked
    }

    static func read(_ key: String) -> Read {
        let query: [String: Any] = [
            kSecClass as String: kSecClassGenericPassword,
            kSecAttrService as String: service,
            kSecAttrAccount as String: key,
            kSecReturnData as String: true,
            kSecMatchLimit as String: kSecMatchLimitOne,
        ]
        var item: CFTypeRef?
        switch SecItemCopyMatching(query as CFDictionary, &item) {
        case errSecSuccess:
            guard let data = item as? Data, let s = String(data: data, encoding: .utf8) else { return .missing }
            return .found(s)
        case errSecItemNotFound:
            return .missing
        default:
            return .locked
        }
    }

    /// The value, or nil when there is none or it cannot be read now (see `read`).
    static func get(_ key: String) -> String? {
        if case .found(let s) = read(key) { return s }
        return nil
    }

    /// Writes the value; an empty one removes the item.
    static func set(_ value: String, for key: String) {
        let query: [String: Any] = [
            kSecClass as String: kSecClassGenericPassword,
            kSecAttrService as String: service,
            kSecAttrAccount as String: key,
        ]
        SecItemDelete(query as CFDictionary)
        guard !value.isEmpty else { return }
        var add = query
        add[kSecValueData as String] = Data(value.utf8)
        add[kSecAttrAccessible as String] = kSecAttrAccessibleAfterFirstUnlock
        SecItemAdd(add as CFDictionary, nil)
    }
}

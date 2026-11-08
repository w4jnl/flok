// The languages the microphone offers: what the user ticked in Settings › Voice, shown in the
// mic's hold menu by name and on the button by code. Pure, so the tests can pin it.
import Foundation

enum VoiceLanguages {
    /// The menu's languages: the ticked ones, with the one being listened in first among
    /// them; nothing ticked yet, that one alone.
    static func offered(chosen: [String], current: String, phone: Locale = .current) -> [String] {
        let now = current.isEmpty ? phone.identifier(.bcp47) : current
        var out: [String] = []
        for id in [now] + chosen where !id.isEmpty && !out.contains(id) { out.append(id) }
        return out
    }

    /// The code on the button: the language's, in capitals ("nl-NL" → "NL"; "" = the phone's).
    static func code(_ id: String, phone: Locale = .current) -> String {
        let l = id.isEmpty ? phone : Locale(identifier: id)
        return (l.language.languageCode?.identifier ?? id).uppercased()
    }

    /// The language's name as this phone would say it ("Dutch (Netherlands)").
    static func name(_ id: String, phone: Locale = .current) -> String {
        let l = id.isEmpty ? phone : Locale(identifier: id)
        return phone.localizedString(forIdentifier: l.identifier) ?? l.identifier
    }
}

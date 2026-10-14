// The dictated text tidied by Apple's on-device model (Foundation Models, iOS 26 on an iPhone
// 15 Pro or later with Apple Intelligence on): punctuation, casing, filler words out, and
// file names and commands spelled as code, with the app's vocabulary as the spellings to
// prefer. Nothing leaves the phone. On any trouble the raw text is kept.
import Foundation
#if canImport(FoundationModels)
import FoundationModels
#endif

enum Cleanup {
    /// Why the clean-up is not on offer on this phone, or nil when it is.
    static var unavailableReason: String? {
        #if canImport(FoundationModels)
        if #available(iOS 26.0, *) {
            switch SystemLanguageModel.default.availability {
            case .available: return nil
            case .unavailable(.deviceNotEligible): return "this iPhone has no Apple Intelligence"
            case .unavailable(.appleIntelligenceNotEnabled): return "Apple Intelligence is off in Settings"
            case .unavailable(.modelNotReady): return "the model is still downloading"
            case .unavailable: return "not available"
            }
        }
        #endif
        return "needs iOS 26"
    }

    static var available: Bool { unavailableReason == nil }

    /// Under this many words there is nothing to tidy (a "yes", a "2").
    static let minWords = 3
    /// Over this much text the model's window is too small to be trusted with it.
    static let maxCharacters = 2500

    /// The text as the agent should read it; the raw text when the model is unavailable, the
    /// text too short or too long, or the model refuses or fails.
    static func clean(_ raw: String, vocabulary: [String]) async -> String {
        let text = raw.trimmingCharacters(in: .whitespacesAndNewlines)
        guard text.split(whereSeparator: \.isWhitespace).count >= minWords, text.count <= maxCharacters else { return text }
        #if canImport(FoundationModels)
        if #available(iOS 26.0, *), SystemLanguageModel.default.isAvailable {
            let session = LanguageModelSession(instructions: instructions(vocabulary: vocabulary))
            do {
                let out = try await withThrowingTaskGroup(of: String.self) { group in
                    group.addTask {
                        try await session.respond(to: prompt(text), options: GenerationOptions(temperature: 0.1, maximumResponseTokens: 800)).content
                    }
                    group.addTask {
                        try await Task.sleep(for: .seconds(12))
                        throw CancellationError()
                    }
                    let first = try await group.next() ?? text
                    group.cancelAll()
                    return first
                }
                return accept(out, raw: text)
            } catch {
                return text
            }
        }
        #endif
        return text
    }

    /// What the model is told once per session.
    static func instructions(vocabulary: [String]) -> String {
        var s = """
        You tidy text that was dictated to a coding assistant working in a terminal. Return the same message, cleaned: fix punctuation and casing, drop filler words and false starts, and write file names, paths, commands, flags and identifiers the way they appear in code (for example "relay dot go" is relay.go, "dash dash no focus" is --no-focus, "make build" stays make build). Keep the meaning, the order and the language of the message. Do not answer it, do not add anything, do not explain. Reply with the cleaned message only.
        """
        let words = vocabulary.prefix(60)
        if !words.isEmpty {
            s += "\n\nNames that may occur, spelled exactly like this: " + words.joined(separator: ", ") + "."
        }
        return s
    }

    static func prompt(_ text: String) -> String { "Dictated: " + text }

    /// The model's output when it looks like a cleaned version of the text (about the same
    /// length, no commentary), else the raw text.
    static func accept(_ out: String, raw: String) -> String {
        var cleaned = out.trimmingCharacters(in: .whitespacesAndNewlines)
        if cleaned.hasPrefix("\"") && cleaned.hasSuffix("\"") && cleaned.count > 2 { cleaned = String(cleaned.dropFirst().dropLast()) }
        for prefix in ["Cleaned:", "Cleaned message:", "Dictated:"] where cleaned.hasPrefix(prefix) {
            cleaned = cleaned.dropFirst(prefix.count).trimmingCharacters(in: .whitespaces)
        }
        guard !cleaned.isEmpty else { return raw }
        let ratio = Double(cleaned.count) / Double(max(1, raw.count))
        guard ratio > 0.4, ratio < 1.8 else { return raw }
        return cleaned
    }
}

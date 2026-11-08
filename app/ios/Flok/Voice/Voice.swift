// Tap to talk: the microphone into Apple's speech recognition on the phone, with the words the
// app knows (Vocabulary) as context, then the on-device clean-up (Cleanup). On iOS 26 the
// engine is SpeechAnalyzer (on the Neural Engine, live words while you speak) with
// SpeechTranscriber where it has the language and the keyboard's DictationTranscriber where it
// does not (Dutch, Slovak); before that, SFSpeechRecognizer. The audio never goes further than
// Apple's recogniser; with SpeechAnalyzer it does not leave the phone.
import AVFoundation
import Foundation
import Observation
import Speech

enum VoiceError: LocalizedError {
    case microphoneDenied
    case speechDenied
    case noMicrophone
    case noEngine
    case noLocale

    var errorDescription: String? {
        switch self {
        case .microphoneDenied: "allow the microphone for flok in Settings"
        case .speechDenied: "allow speech recognition for flok in Settings"
        case .noMicrophone: "no microphone"
        case .noEngine: "speech recognition is not available right now"
        case .noLocale: "no speech model for this language"
        }
    }
}

@MainActor
@Observable
final class Voice {
    enum Phase: Equatable {
        case idle, starting, listening, finishing, cleaning
    }

    private(set) var phase: Phase = .idle
    /// The words settled so far, and the ones still changing.
    private(set) var finalText = ""
    private(set) var volatileText = ""
    private(set) var lastError: String?
    private var engine: (any SpeechEngine)?

    var listening: Bool { phase == .listening || phase == .starting }
    var busy: Bool { phase == .finishing || phase == .cleaning }
    /// What has been heard so far, as one line for the field.
    var live: String {
        let v = volatileText.trimmingCharacters(in: .whitespaces)
        if v.isEmpty { return finalText }
        return finalText.isEmpty ? v : finalText + " " + v
    }

    /// Which recogniser this phone gets.
    static var engineName: String {
        if #available(iOS 26.0, *), SpeechTranscriber.isAvailable {
            return "Apple SpeechTranscriber, on this phone"
        }
        return "Apple speech recognition"
    }

    /// The languages the recogniser knows: on iOS 26 SpeechTranscriber's and the keyboard's
    /// dictation model's together, each language once.
    static func supportedLocales() async -> [Locale] {
        if #available(iOS 26.0, *), SpeechTranscriber.isAvailable {
            let both = await SpeechTranscriber.supportedLocales + DictationTranscriber.supportedLocales
            var seen = Set<String>()
            return both.filter { seen.insert($0.identifier(.bcp47)).inserted }.sorted { $0.identifier < $1.identifier }
        }
        return SFSpeechRecognizer.supportedLocales().sorted { $0.identifier < $1.identifier }
    }

    /// Whether a language's model is on the phone (iOS 26; the first dictation in a language
    /// downloads it once, the system keeps it for every app). nil where that cannot be known.
    static func modelInstalled(_ locale: Locale) async -> Bool? {
        if #available(iOS 26.0, *), SpeechTranscriber.isAvailable {
            guard let module = await AnalyzerEngine.module(for: locale, exact: true) else { return nil }
            return await AssetInventory.status(forModules: [module]) == .installed
        }
        return nil
    }

    /// A language left the user's list: flok's claim on its model goes (the system frees the
    /// model when no app claims it; an app may claim only a few).
    static func release(_ locale: Locale) async {
        if #available(iOS 26.0, *), SpeechTranscriber.isAvailable {
            for l in await AssetInventory.reservedLocales where l.identifier(.bcp47) == locale.identifier(.bcp47) {
                _ = await AssetInventory.release(reservedLocale: l)
            }
        }
    }

    /// Starts listening; the live text shows up in finalText and volatileText.
    func start(vocabulary: [String], locale: Locale) async {
        guard phase == .idle else { return }
        phase = .starting
        finalText = ""
        volatileText = ""
        lastError = nil
        guard await AVAudioApplication.requestRecordPermission() else {
            fail(VoiceError.microphoneDenied)
            return
        }
        let eng: any SpeechEngine
        if #available(iOS 26.0, *), SpeechTranscriber.isAvailable {
            eng = AnalyzerEngine()
        } else {
            eng = LegacyEngine()
        }
        engine = eng
        do {
            try await eng.start(vocabulary: vocabulary, locale: locale) { [weak self] final, volatile in
                guard let self else { return }
                finalText = final
                volatileText = volatile
            }
            if phase == .starting {
                phase = .listening
            }
        } catch {
            fail(error)
        }
    }

    /// Stops listening and returns what was said, tidied when asked (and the phone can).
    func stop(cleanup: Bool, vocabulary: [String]) async -> String {
        guard let engine, listening else { return "" }
        phase = .finishing
        var text: String
        do {
            text = try await engine.stop()
        } catch {
            lastError = error.localizedDescription
            text = live
        }
        self.engine = nil
        text = text.trimmingCharacters(in: .whitespacesAndNewlines)
        if cleanup && !text.isEmpty && Cleanup.available {
            phase = .cleaning
            text = await Cleanup.clean(text, vocabulary: vocabulary)
        }
        finalText = text
        volatileText = ""
        phase = .idle
        return text
    }

    /// Drops the session without a result.
    func cancel() {
        engine?.cancel()
        engine = nil
        phase = .idle
        volatileText = ""
    }

    private func fail(_ error: Error) {
        lastError = error.localizedDescription
        engine?.cancel()
        engine = nil
        phase = .idle
    }
}

/// A recogniser: start feeds the live text to onUpdate (settled, changing), stop returns the
/// final text.
@MainActor
protocol SpeechEngine: AnyObject {
    func start(vocabulary: [String], locale: Locale, onUpdate: @escaping @MainActor (String, String) -> Void) async throws
    func stop() async throws -> String
    func cancel()
}

// MARK: - iOS 26: SpeechAnalyzer + SpeechTranscriber

@available(iOS 26.0, *)
@MainActor
final class AnalyzerEngine: SpeechEngine {
    private var analyzer: SpeechAnalyzer?
    private var input: AsyncStream<AnalyzerInput>.Continuation?
    private var reader: Task<String, Never>?
    private let audio = AudioCapture()

    /// The locale among `have` for `wanted`: the same language and region, else the same
    /// language with one of the phone's regions, else (unless `exact`) the same language anywhere.
    static func pick(_ wanted: Locale, from have: [Locale], exact: Bool) -> Locale? {
        let want = wanted.identifier(.bcp47)
        if let l = have.first(where: { $0.identifier(.bcp47) == want }) { return l }
        guard let lang = wanted.language.languageCode?.identifier else { return nil }
        let same = have.filter { $0.language.languageCode?.identifier == lang }
        let regions = Locale.preferredLanguages.compactMap { Locale(identifier: $0).region?.identifier }
        if let l = same.first(where: { l in l.region.map { regions.contains($0.identifier) } ?? false }) { return l }
        return exact ? nil : same.first
    }

    /// The model for a language: SpeechTranscriber where it has it, else the keyboard's
    /// DictationTranscriber (Dutch, Slovak); nil where neither does.
    static func module(for wanted: Locale, exact: Bool = false) async -> (any SpeechModule)? {
        if let l = pick(wanted, from: await SpeechTranscriber.supportedLocales, exact: exact) {
            return SpeechTranscriber(locale: l, transcriptionOptions: [], reportingOptions: [.volatileResults, .fastResults], attributeOptions: [])
        }
        if let l = pick(wanted, from: await DictationTranscriber.supportedLocales, exact: exact) {
            return DictationTranscriber(locale: l, contentHints: [], transcriptionOptions: [.punctuation], reportingOptions: [.volatileResults, .frequentFinalization], attributeOptions: [])
        }
        return nil
    }

    func start(vocabulary: [String], locale: Locale, onUpdate: @escaping @MainActor (String, String) -> Void) async throws {
        // the language asked for, else US English (a phone set to a language without a model)
        var found = await Self.module(for: locale)
        if found == nil { found = await Self.module(for: Locale(identifier: "en_US")) }
        guard let module = found else { throw VoiceError.noLocale }
        // the first use downloads the language's model (once; the system keeps it for every app)
        if await AssetInventory.status(forModules: [module]) != .installed,
           let request = try await AssetInventory.assetInstallationRequest(supporting: [module]) {
            try await request.downloadAndInstall()
        }
        let context = AnalysisContext()
        if !vocabulary.isEmpty {
            context.contextualStrings = [.general: vocabulary]
        }
        guard let format = await SpeechAnalyzer.bestAvailableAudioFormat(compatibleWith: [module]) else { throw VoiceError.noEngine }
        let (stream, continuation) = AsyncStream.makeStream(of: AnalyzerInput.self)
        let analyzer = SpeechAnalyzer(inputSequence: stream, modules: [module], analysisContext: context) // starts analysing
        self.analyzer = analyzer
        input = continuation
        reader = Task { @MainActor in
            var settled = ""
            // a result: settled text joins what was said, a volatile one shows beside it
            @MainActor func got(_ attributed: AttributedString, final: Bool) {
                let text = String(attributed.characters).trimmingCharacters(in: .whitespaces)
                if final {
                    if !text.isEmpty {
                        settled += (settled.isEmpty ? "" : " ") + text
                    }
                    onUpdate(settled, "")
                } else {
                    onUpdate(settled, text)
                }
            }
            do {
                if let t = module as? SpeechTranscriber {
                    for try await r in t.results { got(r.text, final: r.isFinal) }
                } else if let t = module as? DictationTranscriber {
                    for try await r in t.results { got(r.text, final: r.isFinal) }
                }
            } catch {
                // the analyzer ended with an error: what settled so far is the result
            }
            return settled
        }
        try audio.start(format: format) { buffer in
            continuation.yield(AnalyzerInput(buffer: buffer))
        }
    }

    func stop() async throws -> String {
        audio.stop()
        input?.finish()
        input = nil
        try? await analyzer?.finalizeAndFinishThroughEndOfInput()
        let text = await reader?.value ?? ""
        analyzer = nil
        reader = nil
        return text
    }

    func cancel() {
        audio.stop()
        input?.finish()
        input = nil
        reader?.cancel()
        reader = nil
        if let analyzer {
            Task { await analyzer.cancelAndFinishNow() }
        }
        analyzer = nil
    }
}

// MARK: - before iOS 26: SFSpeechRecognizer

@MainActor
final class LegacyEngine: SpeechEngine {
    private struct Update: Sendable {
        var text: String?
        var isFinal: Bool
        var failed: Bool
    }

    private var request: SFSpeechAudioBufferRecognitionRequest?
    private var task: SFSpeechRecognitionTask?
    private var reader: Task<String, Never>?
    private var latest = ""
    private let audio = AudioCapture()

    func start(vocabulary: [String], locale: Locale, onUpdate: @escaping @MainActor (String, String) -> Void) async throws {
        let status = await withCheckedContinuation { (c: CheckedContinuation<SFSpeechRecognizerAuthorizationStatus, Never>) in
            SFSpeechRecognizer.requestAuthorization { c.resume(returning: $0) }
        }
        guard status == .authorized else { throw VoiceError.speechDenied }
        guard let recognizer = SFSpeechRecognizer(locale: locale) ?? SFSpeechRecognizer(), recognizer.isAvailable else { throw VoiceError.noEngine }
        let req = SFSpeechAudioBufferRecognitionRequest()
        req.shouldReportPartialResults = true
        req.contextualStrings = Array(vocabulary.prefix(100))
        req.addsPunctuation = true
        req.taskHint = .dictation
        request = req
        let (stream, continuation) = AsyncStream.makeStream(of: Update.self)
        task = recognizer.recognitionTask(with: req) { result, error in
            continuation.yield(Update(text: result?.bestTranscription.formattedString, isFinal: result?.isFinal ?? false, failed: error != nil))
            if result?.isFinal == true || error != nil {
                continuation.finish()
            }
        }
        reader = Task { @MainActor [weak self] in
            var text = ""
            for await u in stream {
                if let t = u.text {
                    text = t
                    self?.latest = t
                }
                onUpdate(u.isFinal ? text : "", u.isFinal ? "" : text)
                if u.isFinal || u.failed { break }
            }
            return text
        }
        try audio.start(format: nil) { buffer in
            req.append(buffer)
        }
    }

    func stop() async throws -> String {
        audio.stop()
        request?.endAudio()
        guard let reader else { return latest }
        // the final result follows endAudio within a moment; a recogniser that stays silent
        // leaves us the last partial one
        let text = await withTaskGroup(of: String?.self) { group in
            group.addTask { await reader.value }
            group.addTask {
                try? await Task.sleep(for: .seconds(6))
                return nil
            }
            let first = await group.next() ?? nil
            group.cancelAll()
            return first
        }
        task?.cancel()
        task = nil
        request = nil
        self.reader = nil
        return text ?? latest
    }

    func cancel() {
        audio.stop()
        task?.cancel()
        task = nil
        request = nil
        reader?.cancel()
        reader = nil
    }
}

// MARK: - the microphone

/// The microphone through AVAudioEngine: buffers to the sink, converted to the recogniser's
/// format when it wants one. Start and stop run on the main actor; the tap runs on the audio
/// thread and touches only the converter.
final class AudioCapture {
    private let engine = AVAudioEngine()
    private var running = false

    func start(format target: AVAudioFormat?, sink: @escaping (AVAudioPCMBuffer) -> Void) throws {
        let session = AVAudioSession.sharedInstance()
        try session.setCategory(.record, mode: .default, options: [.duckOthers, .allowBluetoothHFP])
        try session.setActive(true)
        let input = engine.inputNode
        let inFormat = input.outputFormat(forBus: 0)
        guard inFormat.sampleRate > 0, inFormat.channelCount > 0 else { throw VoiceError.noMicrophone }
        var converter: AVAudioConverter?
        if let target, target != inFormat {
            converter = AVAudioConverter(from: inFormat, to: target)
            converter?.primeMethod = .none
        }
        input.installTap(onBus: 0, bufferSize: 4096, format: inFormat) { buffer, _ in
            if let converter {
                if let out = AudioCapture.convert(buffer, with: converter) {
                    sink(out)
                }
            } else {
                sink(buffer)
            }
        }
        engine.prepare()
        try engine.start()
        running = true
    }

    func stop() {
        guard running else { return }
        running = false
        engine.inputNode.removeTap(onBus: 0)
        engine.stop()
        try? AVAudioSession.sharedInstance().setActive(false, options: [.notifyOthersOnDeactivation])
    }

    /// One buffer in the converter's output format.
    nonisolated static func convert(_ buffer: AVAudioPCMBuffer, with converter: AVAudioConverter) -> AVAudioPCMBuffer? {
        let ratio = converter.outputFormat.sampleRate / converter.inputFormat.sampleRate
        let frames = AVAudioFrameCount((Double(buffer.frameLength) * ratio).rounded(.up))
        guard frames > 0, let out = AVAudioPCMBuffer(pcmFormat: converter.outputFormat, frameCapacity: frames) else { return nil }
        var error: NSError?
        var consumed = false
        let status = converter.convert(to: out, error: &error) { _, statusPointer in
            if consumed {
                statusPointer.pointee = .noDataNow
                return nil
            }
            consumed = true
            statusPointer.pointee = .haveData
            return buffer
        }
        return status == .error ? nil : out
    }
}

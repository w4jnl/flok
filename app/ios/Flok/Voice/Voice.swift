// Hold to talk: the microphone into Apple's speech recognition on the phone, with the words the
// app knows (Vocabulary) as context, then the on-device clean-up (Cleanup). On iOS 26 the
// engine is SpeechTranscriber (SpeechAnalyzer, on the Neural Engine, live words while you
// speak); before that, SFSpeechRecognizer. The audio never goes further than Apple's
// recogniser; with SpeechTranscriber it does not leave the phone.
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

    /// The languages the recogniser knows.
    static func supportedLocales() async -> [Locale] {
        if #available(iOS 26.0, *), SpeechTranscriber.isAvailable {
            return await SpeechTranscriber.supportedLocales.sorted { $0.identifier < $1.identifier }
        }
        return SFSpeechRecognizer.supportedLocales().sorted { $0.identifier < $1.identifier }
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

    /// The locale the transcriber has a model for: the one asked for, else US English.
    static func locale(for wanted: Locale) async -> Locale? {
        if let l = await SpeechTranscriber.supportedLocale(equivalentTo: wanted) { return l }
        return await SpeechTranscriber.supportedLocale(equivalentTo: Locale(identifier: "en_US"))
    }

    func start(vocabulary: [String], locale: Locale, onUpdate: @escaping @MainActor (String, String) -> Void) async throws {
        guard let loc = await Self.locale(for: locale) else { throw VoiceError.noLocale }
        let transcriber = SpeechTranscriber(locale: loc, transcriptionOptions: [], reportingOptions: [.volatileResults, .fastResults], attributeOptions: [])
        // the first use downloads the language's model (once, a few hundred MB)
        if await AssetInventory.status(forModules: [transcriber]) != .installed,
           let request = try await AssetInventory.assetInstallationRequest(supporting: [transcriber]) {
            try await request.downloadAndInstall()
        }
        let context = AnalysisContext()
        if !vocabulary.isEmpty {
            context.contextualStrings = [.general: vocabulary]
        }
        guard let format = await SpeechAnalyzer.bestAvailableAudioFormat(compatibleWith: [transcriber]) else { throw VoiceError.noEngine }
        let (stream, continuation) = AsyncStream.makeStream(of: AnalyzerInput.self)
        let analyzer = SpeechAnalyzer(inputSequence: stream, modules: [transcriber], analysisContext: context) // starts analysing
        self.analyzer = analyzer
        input = continuation
        reader = Task { @MainActor in
            var settled = ""
            do {
                for try await result in transcriber.results {
                    let text = String(result.text.characters).trimmingCharacters(in: .whitespaces)
                    if result.isFinal {
                        if !text.isEmpty {
                            settled += (settled.isEmpty ? "" : " ") + text
                        }
                        onUpdate(settled, "")
                    } else {
                        onUpdate(settled, text)
                    }
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

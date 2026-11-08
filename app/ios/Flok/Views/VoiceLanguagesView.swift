// Settings › Voice › Languages: the languages the microphone's menu offers, ticked from every
// one the phone's recogniser knows. A tick costs nothing: iOS fetches a language's model once,
// at the first dictation in it, and keeps it for every app.
import SwiftUI

struct VoiceLanguagesView: View {
    @Environment(Settings.self) private var settings
    @State private var all: [Locale] = []
    @State private var installed: [String: Bool] = [:] // bcp47 → the model is on the phone
    @State private var search = ""

    private var chosen: [String] { VoiceLanguages.offered(chosen: settings.voiceLanguages, current: settings.voiceLocale) }
    private var rest: [Locale] {
        let picked = Set(chosen)
        return all.filter { !picked.contains($0.identifier(.bcp47)) }
            .filter { search.isEmpty || VoiceLanguages.name($0.identifier(.bcp47)).localizedCaseInsensitiveContains(search) }
    }

    var body: some View {
        List {
            Section {
                ForEach(chosen, id: \.self) { id in
                    row(id, ticked: true)
                }
            } header: {
                Text("Under the microphone")
            } footer: {
                Text("The one it listens in is the first; hold the microphone to change it. The last one cannot be removed.")
            }
            Section("Available") {
                if all.isEmpty {
                    Text("loading…").foregroundStyle(.secondary)
                }
                ForEach(rest, id: \.identifier) { l in
                    row(l.identifier(.bcp47), ticked: false)
                }
            }
        }
        .searchable(text: $search, prompt: "Language")
        .navigationTitle("Languages")
        .navigationBarTitleDisplayMode(.inline)
        .task {
            all = await Voice.supportedLocales()
            for l in all {
                if let on = await Voice.modelInstalled(l) { installed[l.identifier(.bcp47)] = on }
            }
        }
    }

    private func row(_ id: String, ticked: Bool) -> some View {
        Button {
            ticked ? remove(id) : add(id)
        } label: {
            HStack {
                VStack(alignment: .leading, spacing: 2) {
                    Text(VoiceLanguages.name(id)).foregroundStyle(.primary)
                    Text(status(id)).font(.footnote).foregroundStyle(.secondary)
                }
                Spacer()
                if ticked { Image(systemName: "checkmark").fontWeight(.semibold) }
            }
        }
        .disabled(ticked && chosen.count == 1)
    }

    /// Whether the language's model is on the phone, where that can be known.
    private func status(_ id: String) -> String {
        switch installed[id] {
        case true: "model on this phone"
        case false: "model downloaded once, at the first dictation"
        default: "Apple speech recognition"
        }
    }

    private func add(_ id: String) {
        var list = chosen
        list.append(id)
        settings.voiceLanguages = list
        if settings.voiceLocale.isEmpty { settings.voiceLocale = chosen.first ?? id } // the phone's, now by name
    }

    private func remove(_ id: String) {
        var list = chosen
        list.removeAll { $0 == id }
        guard let first = list.first else { return }
        settings.voiceLanguages = list
        if settings.voiceLocale == id || settings.voiceLocale.isEmpty { settings.voiceLocale = first }
        Task { await Voice.release(Locale(identifier: id)) }
    }
}

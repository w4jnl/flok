// The microphone button next to the reply field. A tap listens, a tap stops; held, it offers
// the languages chosen in Settings › Voice, and the one it listens in shows as a code (NL).
import SwiftUI

struct VoiceButton: View {
    let voice: Voice
    /// The language it listens in and the ones the menu offers (locale identifiers).
    let language: String
    let languages: [String]
    let onTap: () -> Void
    let onPick: (String) -> Void
    let onMore: () -> Void
    @State private var taps = 0 // each one bounces the button

    var body: some View {
        Menu {
            ForEach(languages, id: \.self) { id in
                Button { taps += 1; onPick(id) } label: {
                    if id == language {
                        Label(VoiceLanguages.name(id), systemImage: "checkmark")
                    } else {
                        Text(VoiceLanguages.name(id))
                    }
                }
            }
            Divider()
            Button { onMore() } label: { Label("Languages…", systemImage: "globe") }
        } label: {
            face.bounce(on: taps, flash: false) // the tap itself, and a language picked
        } primaryAction: {
            taps += 1
            onTap()
        }
        .disabled(voice.busy)
        .animation(.easeInOut(duration: 0.2), value: voice.listening)
        .accessibilityLabel(voice.listening ? "Stop listening" : "Talk, in \(VoiceLanguages.name(language))")
        .accessibilityHint("Tap and speak, tap again to stop; hold to pick the language")
    }

    private var face: some View {
        HStack(spacing: 3) {
            if voice.busy {
                ProgressView().controlSize(.small)
            } else {
                Image(systemName: voice.listening ? "mic.fill" : "mic")
                    .font(.body.weight(.semibold))
            }
            Text(VoiceLanguages.code(language))
                .font(.system(size: 10, weight: .bold, design: .monospaced))
        }
        .foregroundStyle(voice.listening ? Color.white : Color.accentColor)
        .frame(width: 52, height: 32)
        .background(voice.listening ? Color.red : Color.accentColor.opacity(0.12), in: RoundedRectangle(cornerRadius: 8))
        .contentShape(RoundedRectangle(cornerRadius: 8))
    }
}

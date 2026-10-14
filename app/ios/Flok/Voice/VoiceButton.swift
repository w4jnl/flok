// The microphone button next to the reply field. Hold it and talk, let go to stop; a short tap
// keeps it listening until the next tap (a long dictation without a thumb on the glass).
import SwiftUI

struct VoiceButton: View {
    let voice: Voice
    let onDown: () -> Void
    let onUp: () -> Void
    @State private var pressed = false

    var body: some View {
        ZStack {
            RoundedRectangle(cornerRadius: 8)
                .fill(voice.listening ? Color.red : Color.accentColor.opacity(0.12))
            if voice.busy {
                ProgressView().controlSize(.small)
            } else {
                Image(systemName: voice.listening ? "mic.fill" : "mic")
                    .font(.body.weight(.semibold))
                    .foregroundStyle(voice.listening ? Color.white : Color.accentColor)
            }
        }
        .frame(width: 38, height: 32)
        .scaleEffect(pressed ? 1.12 : 1)
        .animation(.easeOut(duration: 0.12), value: pressed)
        .animation(.easeInOut(duration: 0.2), value: voice.listening)
        .gesture(DragGesture(minimumDistance: 0)
            .onChanged { _ in
                if !pressed {
                    pressed = true
                    onDown()
                }
            }
            .onEnded { _ in
                pressed = false
                onUp()
            })
        .allowsHitTesting(!voice.busy)
        .accessibilityLabel(voice.listening ? "Stop listening" : "Hold to talk")
        .accessibilityHint("Hold and speak; a tap keeps listening until the next tap")
    }
}

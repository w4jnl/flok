// A press you can see. The reply bar's buttons are small, and the keys sit in a scroll view
// that holds the pressed look back until it knows the touch is not a scroll, so a quick tap
// showed nothing. Bounce is a one-shot animation fired by the tap itself: the button dips,
// flashes in the accent colour and springs back past its size. BounceButton is a Button that
// does that and taps back through the haptics; SpringyButton is its look, which also follows
// a finger held on it.
import SwiftUI

/// The bounce: scale and a flash, both animated from the tap.
struct Bounce: ViewModifier {
    var trigger: Int
    var flash = true

    struct Pose {
        var scale = 1.0
        var glow = 0.0
    }

    func body(content: Content) -> some View {
        content.keyframeAnimator(initialValue: Pose(), trigger: trigger) { view, pose in
            view.scaleEffect(pose.scale)
                .overlay {
                    if flash {
                        RoundedRectangle(cornerRadius: 8).fill(Color.accentColor.opacity(pose.glow)).allowsHitTesting(false)
                    }
                }
        } keyframes: { _ in
            KeyframeTrack(\.scale) {
                SpringKeyframe(0.72, duration: 0.07, spring: .snappy)
                SpringKeyframe(1.18, duration: 0.14, spring: .bouncy)
                SpringKeyframe(1.0, duration: 0.24, spring: .bouncy)
            }
            KeyframeTrack(\.glow) {
                LinearKeyframe(0.5, duration: 0.05)
                LinearKeyframe(0.0, duration: 0.4)
            }
        }
    }
}

extension View {
    /// Bounces each time `trigger` changes.
    func bounce(on trigger: Int, flash: Bool = true) -> some View { modifier(Bounce(trigger: trigger, flash: flash)) }
}

/// A Button of the reply bar: the bounce and a haptic tap (a firm one when `firm`) on every press.
struct BounceButton<Label: View>: View {
    var look: SpringyButton.Look = .plain
    var firm = false
    let action: () -> Void
    @ViewBuilder let label: () -> Label
    @State private var taps = 0

    var body: some View {
        Button {
            taps += 1
            firm ? Haptics.press() : Haptics.tap()
            action()
        } label: {
            label()
        }
        .buttonStyle(SpringyButton(look: look))
        .bounce(on: taps, flash: look != .plain)
    }
}

struct SpringyButton: ButtonStyle {
    enum Look { case plain, tinted, prominent }
    var look: Look = .plain

    func makeBody(configuration c: Configuration) -> some View {
        dressed(c.label)
            .scaleEffect(c.isPressed ? 0.8 : 1)
            .animation(.spring(response: 0.25, dampingFraction: 0.5), value: c.isPressed)
    }

    @ViewBuilder private func dressed(_ label: Configuration.Label) -> some View {
        switch look {
        case .plain:
            label
        case .tinted:
            label.foregroundStyle(Color.accentColor)
                .padding(.horizontal, 12).padding(.vertical, 7)
                .background(Color.accentColor.opacity(0.12), in: RoundedRectangle(cornerRadius: 8))
        case .prominent:
            label.foregroundStyle(.white)
                .padding(.horizontal, 12).padding(.vertical, 7)
                .background(Color.accentColor, in: RoundedRectangle(cornerRadius: 8))
        }
    }
}

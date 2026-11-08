// A touch you can feel: the reply bar's buttons are small and their press is easy to miss,
// so each one taps back through the Taptic Engine (nothing where the phone has none).
import UIKit

@MainActor
enum Haptics {
    /// A key or a small button pressed.
    static func tap() { UIImpactFeedbackGenerator(style: .light).impactOccurred() }
    /// Something started: the microphone listens, a reply is on its way.
    static func press() { UIImpactFeedbackGenerator(style: .medium).impactOccurred() }
    /// A reply the agent did not get.
    static func failed() { UINotificationFeedbackGenerator().notificationOccurred(.error) }
}

import Foundation
import Observation

/// Whether a screen that changes under the reader should keep showing its bottom. It does
/// while the reader is there (within `slack` points of the end); it stops when they scroll
/// away, and starts again when they come back, or ask for it. A refresh moves the end without
/// the reader's doing, so a longer distance alone never ends the following: only a change of
/// the scroll offset does, and not one the view made itself (`settle`). Only `following` is
/// observed, so the looks a scroll brings at every frame cost the view nothing until it flips.
@MainActor @Observable final class BottomFollower {
    /// How far above the end still counts as at the end: a couple of rows of text, the bounce.
    static let slack: CGFloat = 32

    private(set) var following = true
    @ObservationIgnored private var offset: CGFloat = 0
    @ObservationIgnored private var settleUntil = Date.distantPast

    /// A look at the geometry after a layout: `distance` is how far the content's end sits
    /// below the viewport's bottom (0 or less: in view), `offset` the scroll offset.
    func look(distance: CGFloat, offset: CGFloat) {
        defer { self.offset = offset }
        if distance <= Self.slack {
            set(true)
        } else if offset != self.offset, Date.now >= settleUntil {
            set(false)
        }
    }

    /// The view is about to scroll to the end by itself: the frames on the way, for the next
    /// `seconds`, are not the reader's doing.
    func settle(for seconds: TimeInterval = 0.3) { settleUntil = Date.now.addingTimeInterval(seconds) }

    /// The reader asked for the bottom.
    func resume() { set(true) }

    private func set(_ on: Bool) {
        if following != on { following = on }
    }
}

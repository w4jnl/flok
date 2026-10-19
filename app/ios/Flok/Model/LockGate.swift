// The app lock: Face ID (the passcode as fallback) before the app shows anything, after it has
// been away longer than the grace the user chose. A UI gate only: the phone's key and the relay
// are untouched, and the relay client keeps streaming behind the cover, so unlocking shows the
// present. The cover is a second window above the alert level, so the Pair, Approve and Settings
// sheets hide under it too. The journal's LockGate, with flok's additions: a recent unlock of the
// phone itself counts (LockPolicy.reuse), and a link that would act (a pairing, a join) waits
// until the lock is off (RootView).
import LocalAuthentication
import SwiftUI
import UIKit

@MainActor
@Observable
final class LockGate {
    /// Nothing shows until the owner authenticates.
    private(set) var locked = false
    /// The content is hidden (the app switcher, a call) without asking for anything.
    private(set) var covered = false
    private(set) var authenticating = false
    /// A flok://pair or flok://link that arrived while locked; RootView opens it after the unlock.
    var pendingURL: URL?
    private var backgroundedAt: ContinuousClock.Instant?
    private var activatedOnce = false
    private var reuse: TimeInterval = 0
    private var window: UIWindow?

    /// The enrolled biometry, for labels; Face ID on most phones.
    var biometry: LABiometryType {
        let c = LAContext()
        _ = c.canEvaluatePolicy(.deviceOwnerAuthenticationWithBiometrics, error: nil)
        return c.biometryType
    }

    /// False on a phone without a passcode: there is nothing to ask, so the lock cannot be turned on.
    var canAuthenticate: Bool { LAContext().canEvaluatePolicy(.deviceOwnerAuthentication, error: nil) }

    /// The unit tests host the app: no lock there, or a simulator where the lock was left on
    /// stalls the test runner behind the prompt ("hung before establishing connection").
    private static let hostingTests = ProcessInfo.processInfo.environment["XCTestConfigurationFilePath"] != nil

    /// The scene's phase changed; the lock follows the user's settings.
    func scene(_ phase: ScenePhase, settings: Settings) {
        guard !Self.hostingTests else { return }
        switch phase {
        case .background:
            backgroundedAt = .now
            covered = settings.lockEnabled
        case .inactive:
            covered = settings.lockEnabled // the switcher's thumbnail; never a prompt from here
        case .active:
            let since = backgroundedAt.map { ContinuousClock.now - $0 }
            let launching = !activatedOnce
            if LockPolicy.shouldLock(since: since, grace: settings.lockGrace, enabled: settings.lockEnabled,
                                     onLaunch: settings.lockOnLaunch, launching: launching) {
                locked = true
            }
            activatedOnce = true
            backgroundedAt = nil
            covered = false
            reuse = LockPolicy.reuse(forGrace: settings.lockGraceSeconds)
            // Asked on a launch and on a return from the background, not after the inactive
            // bounce of the prompt itself: a cancelled prompt would come straight back. The
            // cover's Unlock button asks again.
            if locked, !authenticating, launching || since != nil {
                Task { await unlock() }
            }
        @unknown default:
            break
        }
        show(locked || covered)
    }

    /// Asks the owner; on success the cover comes down.
    func unlock() async {
        guard !authenticating else { return }
        authenticating = true
        defer { authenticating = false }
        if await authenticate(reason: "Unlock flok.", reuse: reuse) {
            locked = false
            show(covered)
        }
    }

    /// Face ID, Touch ID or the passcode, whichever the phone has; true when it has none. `reuse`
    /// is how long ago the phone itself may have been unlocked by biometry for that to count.
    func authenticate(reason: String, reuse: TimeInterval = 0) async -> Bool {
        let ctx = LAContext()
        ctx.touchIDAuthenticationAllowableReuseDuration = reuse
        guard ctx.canEvaluatePolicy(.deviceOwnerAuthentication, error: nil) else { return true }
        return await withCheckedContinuation { k in
            ctx.evaluatePolicy(.deviceOwnerAuthentication, localizedReason: reason) { ok, _ in k.resume(returning: ok) }
        }
    }

    private func show(_ on: Bool) {
        if on {
            guard window == nil else { return }
            let scenes = UIApplication.shared.connectedScenes.compactMap { $0 as? UIWindowScene }
            guard let scene = scenes.first(where: { $0.activationState == .foregroundActive || $0.activationState == .foregroundInactive }) ?? scenes.first else { return }
            let w = UIWindow(windowScene: scene)
            w.windowLevel = .alert + 1
            w.rootViewController = UIHostingController(rootView: LockCover(gate: self))
            w.isHidden = false
            window = w
        } else {
            window?.isHidden = true
            window = nil
        }
    }
}

/// What the cover shows: the brand on the terminal ground, the biometry's glyph on the Unlock
/// button when locked, nothing more when merely covered. No agents, no screens, no status.
struct LockCover: View {
    let gate: LockGate

    var body: some View {
        ZStack(alignment: .top) {
            Brand.ground.ignoresSafeArea()
            Rectangle().fill(Brand.working).frame(height: 3).ignoresSafeArea(edges: .top)
            VStack(spacing: 18) {
                Spacer()
                LiveStyle.mark(Brand.working, size: 48).accessibilityHidden(true)
                Brand.wordmark(24)
                if gate.locked {
                    Button {
                        Task { await gate.unlock() }
                    } label: {
                        Label(gate.authenticating ? "Unlocking…" : "Unlock", systemImage: LockPolicy.symbol(gate.biometry))
                            .font(Brand.mono(15, bold: true))
                            .frame(minWidth: 180, minHeight: 28)
                    }
                    .buttonStyle(.borderedProminent)
                    .tint(Brand.working)
                    .foregroundStyle(Brand.ground)
                    .disabled(gate.authenticating)
                    .padding(.top, 10)
                }
                Spacer()
                Spacer()
            }
        }
        .environment(\.colorScheme, .dark)
    }
}

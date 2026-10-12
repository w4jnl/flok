// Pushes: the categories with their lock-screen actions, the APNs token handed to the relay,
// and what a tapped action does. Approve and Deny answer the agent through the relay's HTTP
// API without opening the app, Mute 1 h silences the agent in the settings the relay filters
// with; a plain tap opens the pane. The expanded view is the content extension's.
import Foundation
import UIKit
import UserNotifications

enum NotificationAction: String, CaseIterable {
    case approve = "APPROVE"
    case deny = "DENY"
    case seen = "SEEN"
    case mute = "MUTE_1H"

    /// What the action types: Enter takes the highlighted choice of a permission prompt (Yes),
    /// Escape dismisses it (No).
    var keys: [String]? {
        switch self {
        case .approve: ["Enter"]
        case .deny: ["Escape"]
        case .seen, .mute: nil
        }
    }
}

enum NotificationCategory: String, CaseIterable {
    case permission = "FLOK_PERMISSION"
    case question = "FLOK_QUESTION"
    case done = "FLOK_DONE"

    var actions: [UNNotificationAction] {
        let mute = UNNotificationAction(identifier: NotificationAction.mute.rawValue, title: "Mute 1 h", options: [], icon: UNNotificationActionIcon(systemImageName: "bell.slash"))
        switch self {
        case .permission:
            return [UNNotificationAction(identifier: NotificationAction.approve.rawValue, title: "Approve", options: [.authenticationRequired], icon: UNNotificationActionIcon(systemImageName: "checkmark")),
                    UNNotificationAction(identifier: NotificationAction.deny.rawValue, title: "Deny", options: [.authenticationRequired, .destructive], icon: UNNotificationActionIcon(systemImageName: "xmark")),
                    mute]
        case .question:
            return [mute]
        case .done:
            return [UNNotificationAction(identifier: NotificationAction.seen.rawValue, title: "Mark seen", options: [], icon: UNNotificationActionIcon(systemImageName: "eye")), mute]
        }
    }

    static var all: Set<UNNotificationCategory> {
        Set(allCases.map { UNNotificationCategory(identifier: $0.rawValue, actions: $0.actions, intentIdentifiers: [], options: []) })
    }
}

@MainActor
final class Notifications: NSObject, UNUserNotificationCenterDelegate {
    static let shared = Notifications()

    var relay: Relay?
    private(set) var authorization: UNAuthorizationStatus = .notDetermined
    private(set) var apnsToken: String?
    private(set) var lastError: String?

    func setup() {
        let center = UNUserNotificationCenter.current()
        center.delegate = self
        center.setNotificationCategories(NotificationCategory.all)
        Task { await refreshAuthorization() }
    }

    func refreshAuthorization() async {
        authorization = await UNUserNotificationCenter.current().notificationSettings().authorizationStatus
        if authorization == .authorized {
            UIApplication.shared.registerForRemoteNotifications()
        }
    }

    /// Asks once; then registers with APNs, whose token goes to the relay.
    func enable() async {
        let center = UNUserNotificationCenter.current()
        _ = try? await center.requestAuthorization(options: [.alert, .sound, .badge])
        await refreshAuthorization()
    }

    func didRegister(deviceToken: Data) {
        let hex = deviceToken.map { String(format: "%02x", $0) }.joined()
        apnsToken = hex
        lastError = nil
        Task { await registerIfNeeded() }
    }

    func didFail(_ error: Error) {
        lastError = error.localizedDescription
    }

    /// Hands the APNs token to the relay once per relay and token.
    func registerIfNeeded() async {
        guard let relay, let apnsToken, relay.settings.isConfigured else { return }
        if relay.settings.registered == "\(relay.settings.relayURL)|\(apnsToken)" { return }
        do {
            try await relay.registerDevice(apnsToken: apnsToken)
        } catch {
            lastError = "registering with the relay: \(error.localizedDescription)"
        }
    }

    var isRegistered: Bool {
        guard let relay, let apnsToken else { return false }
        return relay.settings.registered == "\(relay.settings.relayURL)|\(apnsToken)"
    }

    // MARK: delegate

    // The completion-handler forms on purpose: UIKit asserts that the handler runs on the main
    // thread, and the async forms of these methods call it from the concurrency pool (a tapped
    // notification aborted the app in -[UIApplication _performBlockAfterCATransactionCommitSynchronizes:]).

    nonisolated func userNotificationCenter(_ center: UNUserNotificationCenter, willPresent notification: UNNotification,
                                            withCompletionHandler completionHandler: @escaping (UNNotificationPresentationOptions) -> Void) {
        completionHandler([.banner, .sound, .badge, .list])
    }

    nonisolated func userNotificationCenter(_ center: UNUserNotificationCenter, didReceive response: UNNotificationResponse,
                                            withCompletionHandler completionHandler: @escaping () -> Void) {
        let push = PushPayload(response.notification.request.content.userInfo)
        let action = response.actionIdentifier
        let done = MainThreadCallback(completionHandler)
        Task { @MainActor in
            // An action from the lock screen runs in the background: iOS keeps the app alive
            // until the handler is called, so it is called once the answer went out.
            await Notifications.shared.perform(action: action, push: push)
            done.call()
        }
    }

    func perform(action: String, push: PushPayload?) async {
        guard let push, let relay else { return }
        let target = Target(instance: push.instance, pane: push.pane)
        switch NotificationAction(rawValue: action) {
        case .approve, .deny:
            let keys = NotificationAction(rawValue: action)!.keys
            do { try await relay.answer(target, keys: keys) } catch { lastError = error.localizedDescription }
        case .seen:
            do { try await relay.markSeen(target) } catch { lastError = error.localizedDescription }
        case .mute:
            guard !push.agent.isEmpty else { return }
            await relay.updatePrefsNow { $0.mute(instance: push.instance, host: push.host, agent: push.agent, until: .now.addingTimeInterval(3600)) }
        case nil: // the notification itself, or Open
            relay.pendingOpen = target
        }
    }
}

/// Carries a UIKit completion handler into a main-actor task; it is only ever called there.
private struct MainThreadCallback: @unchecked Sendable {
    let body: () -> Void
    init(_ body: @escaping () -> Void) { self.body = body }
    @MainActor func call() { body() }
}

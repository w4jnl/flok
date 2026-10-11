// Pushes: the categories with their lock-screen actions, the APNs token handed to the relay,
// and what a tapped action does. Approve and Deny answer the agent through the relay's HTTP
// API without opening the app; a plain tap opens the pane.
import Foundation
import UIKit
import UserNotifications

enum NotificationAction: String, CaseIterable {
    case approve = "APPROVE"
    case deny = "DENY"
    case seen = "SEEN"

    /// What the action types: Enter takes the highlighted choice of a permission prompt (Yes),
    /// Escape dismisses it (No).
    var keys: [String]? {
        switch self {
        case .approve: ["Enter"]
        case .deny: ["Escape"]
        case .seen: nil
        }
    }
}

enum NotificationCategory: String, CaseIterable {
    case permission = "FLOK_PERMISSION"
    case question = "FLOK_QUESTION"
    case done = "FLOK_DONE"

    var actions: [UNNotificationAction] {
        switch self {
        case .permission:
            [UNNotificationAction(identifier: NotificationAction.approve.rawValue, title: "Approve", options: [.authenticationRequired]),
             UNNotificationAction(identifier: NotificationAction.deny.rawValue, title: "Deny", options: [.authenticationRequired, .destructive])]
        case .question:
            []
        case .done:
            [UNNotificationAction(identifier: NotificationAction.seen.rawValue, title: "Mark seen", options: [])]
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

    nonisolated func userNotificationCenter(_ center: UNUserNotificationCenter, willPresent notification: UNNotification) async -> UNNotificationPresentationOptions {
        [.banner, .sound, .badge, .list]
    }

    nonisolated func userNotificationCenter(_ center: UNUserNotificationCenter, didReceive response: UNNotificationResponse) async {
        let info = response.notification.request.content.userInfo
        let instance = info["instance"] as? String ?? ""
        let pane = info["pane"] as? String ?? ""
        let action = response.actionIdentifier
        await MainActor.run {
            Task { await Notifications.shared.perform(action: action, instance: instance, pane: pane) }
        }
    }

    func perform(action: String, instance: String, pane: String) async {
        guard !instance.isEmpty, !pane.isEmpty else { return }
        let target = Target(instance: instance, pane: pane)
        guard let relay else { return }
        switch NotificationAction(rawValue: action) {
        case .approve, .deny:
            let keys = NotificationAction(rawValue: action)!.keys
            do { try await relay.answer(target, keys: keys) } catch { lastError = error.localizedDescription }
        case .seen:
            do { try await relay.markSeen(target) } catch { lastError = error.localizedDescription }
        case nil: // the notification itself, or Open
            relay.pendingOpen = target
        }
    }
}

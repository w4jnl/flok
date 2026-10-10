// flok for iPhone: every flok instance in one list, the agents that need you, their screens,
// an answer typed from here or from the lock screen. Talks to flok-relay only (app/API.md).
import SwiftUI
import UIKit

@main
struct FlokApp: App {
    @UIApplicationDelegateAdaptor(AppDelegate.self) private var appDelegate
    @Environment(\.scenePhase) private var phase

    var body: some Scene {
        WindowGroup {
            RootView()
                .environment(Relay.shared)
                .environment(Settings.shared)
        }
        .onChange(of: phase) { _, now in
            switch now {
            case .active:
                Relay.shared.connect()
                Task { await Notifications.shared.refreshAuthorization() }
            case .background:
                Relay.shared.disconnect()
            default:
                break
            }
        }
    }
}

final class AppDelegate: NSObject, UIApplicationDelegate {
    func application(_ application: UIApplication, didFinishLaunchingWithOptions launchOptions: [UIApplication.LaunchOptionsKey: Any]? = nil) -> Bool {
        Notifications.shared.relay = Relay.shared
        Notifications.shared.setup()
        return true
    }

    func application(_ application: UIApplication, didRegisterForRemoteNotificationsWithDeviceToken deviceToken: Data) {
        Notifications.shared.didRegister(deviceToken: deviceToken)
    }

    func application(_ application: UIApplication, didFailToRegisterForRemoteNotificationsWithError error: Error) {
        Notifications.shared.didFail(error)
    }
}

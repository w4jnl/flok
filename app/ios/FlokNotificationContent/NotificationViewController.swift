// The notification content extension: the expanded view of a flok push (long press) is the
// NotificationCard, with the agent's screen fetched from the relay when it opens (GET
// /api/screen; the text never rides in the push) and keys that answer through POST /api/answer.
// The relay's address and the device token come from the keychain group the app shares.
import SwiftUI
import UIKit
import UserNotifications
import UserNotificationsUI

@MainActor
@Observable
final class CardModel {
    var push: PushPayload?
    var screen: CardScreen = .loading
    var status: String?
    private var api: RelayAPI? { RelayAPI.fromKeychain() }

    func load() async {
        guard let push else { return }
        guard let api else {
            screen = .unavailable("open flok once to show the screen here")
            return
        }
        do {
            screen = .text(try await api.screen(instance: push.instance, pane: push.pane))
        } catch {
            screen = .unavailable(error.localizedDescription)
        }
    }

    func send(_ key: CardKey) async {
        guard let push, let api else {
            status = "open flok once to answer from here"
            return
        }
        do {
            try await api.answer(instance: push.instance, pane: push.pane, text: key.text, keys: key.keys)
            try? await api.seen(instance: push.instance, pane: push.pane) // answering is looking at it
            status = "sent \(key.label)"
            try? await Task.sleep(for: .milliseconds(700)) // the agent redraws, then the screen is fetched again
            await load()
        } catch {
            status = error.localizedDescription
        }
    }
}

final class NotificationViewController: UIViewController, UNNotificationContentExtension {
    private let model = CardModel()
    private var host: UIHostingController<CardHost>?

    override func viewDidLoad() {
        super.viewDidLoad()
        view.backgroundColor = UIColor(red: 0x14 / 255.0, green: 0x18 / 255.0, blue: 0x1A / 255.0, alpha: 1)
        let host = UIHostingController(rootView: CardHost(model: model))
        host.view.backgroundColor = .clear
        host.sizingOptions = .preferredContentSize
        addChild(host)
        host.view.translatesAutoresizingMaskIntoConstraints = false
        view.addSubview(host.view)
        NSLayoutConstraint.activate([
            host.view.leadingAnchor.constraint(equalTo: view.leadingAnchor),
            host.view.trailingAnchor.constraint(equalTo: view.trailingAnchor),
            host.view.topAnchor.constraint(equalTo: view.topAnchor),
            host.view.bottomAnchor.constraint(equalTo: view.bottomAnchor),
        ])
        host.didMove(toParent: self)
        self.host = host
    }

    override func preferredContentSizeDidChange(forChildContentContainer container: any UIContentContainer) {
        super.preferredContentSizeDidChange(forChildContentContainer: container)
        preferredContentSize = CGSize(width: view.bounds.width, height: container.preferredContentSize.height)
    }

    func didReceive(_ notification: UNNotification) {
        model.push = PushPayload(notification.request.content.userInfo)
        model.screen = .loading
        model.status = nil
        Task { await model.load() }
    }
}

struct CardHost: View {
    let model: CardModel

    var body: some View {
        if let push = model.push {
            NotificationCard(push: push, screen: model.screen, status: model.status) { key in
                Task { await model.send(key) }
            }
        }
    }
}

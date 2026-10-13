// The notification service extension: before a flok push shows, it gets the state tile as its
// thumbnail (Tiles/, rendered by assets/icons/gen in the brand palette: attention orange for
// anything that wants you, done green), the one place a collapsed notification takes colour.
// The relay marks its pushes mutable-content; anything that goes wrong shows the push as sent.
import UniformTypeIdentifiers
import UserNotifications

final class NotificationService: UNNotificationServiceExtension {
    override func didReceive(_ request: UNNotificationRequest, withContentHandler contentHandler: @escaping (UNNotificationContent) -> Void) {
        guard let content = request.content.mutableCopy() as? UNMutableNotificationContent else {
            contentHandler(request.content)
            return
        }
        if let push = PushPayload(content.userInfo), let tile = Self.attachment(push.tileName) {
            content.attachments = [tile]
        }
        contentHandler(content)
    }

    // Nothing here waits on the network, so the time never runs out before didReceive is done.
    override func serviceExtensionTimeWillExpire() {}

    /// The tile as an attachment: the system moves the file it is given, so it gets a copy.
    static func attachment(_ name: String) -> UNNotificationAttachment? {
        guard let src = Bundle.main.url(forResource: name, withExtension: "png") else { return nil }
        let dst = FileManager.default.temporaryDirectory.appending(path: "\(UUID().uuidString).png")
        do {
            try FileManager.default.copyItem(at: src, to: dst)
            return try UNNotificationAttachment(identifier: "state", url: dst, options: [UNNotificationAttachmentOptionsTypeHintKey: UTType.png.identifier])
        } catch {
            return nil
        }
    }
}

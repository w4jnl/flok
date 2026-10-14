// A file for an answer (app/API.md): the instance saves it next to the agent and types its path,
// which Claude Code and Copilot read as an image or a document. Images are shrunk here first:
// the models downsize anything over about 1568 px on the long side anyway, and a phone
// screenshot is a few MB where 300 KB carry the same picture.
import Foundation
import UIKit
import UniformTypeIdentifiers

struct AttachedFile: Encodable, Sendable, Equatable {
    var name: String
    var mime: String
    var data: Data // base64 on the wire

    /// What the relay takes (answer.MaxFile, answer.MaxFiles).
    static let maxBytes = 4 << 20
    static let maxCount = 6
    /// The long side an image is shrunk to.
    static let maxSide: CGFloat = 1568

    var sizeLabel: String {
        data.count < 1024 * 1024 ? "\(max(1, data.count / 1024)) KB" : String(format: "%.1f MB", Double(data.count) / 1_048_576)
    }

    /// An image as JPEG, no larger than maxSide on the long side and under maxBytes; the name
    /// keeps its stem. nil for an image that cannot be drawn.
    static func image(_ image: UIImage, name: String = "image") -> AttachedFile? {
        var side = maxSide
        var quality: CGFloat = 0.8
        for _ in 0..<6 {
            guard let data = jpeg(image, maxSide: side, quality: quality) else { return nil }
            if data.count <= maxBytes {
                return AttachedFile(name: stem(name) + ".jpg", mime: "image/jpeg", data: data)
            }
            if quality > 0.55 { quality -= 0.15 } else { side *= 0.7 }
        }
        return nil
    }

    /// A PDF as it is, when it fits.
    static func pdf(_ data: Data, name: String = "document") -> AttachedFile? {
        guard !data.isEmpty, data.count <= maxBytes else { return nil }
        return AttachedFile(name: stem(name) + ".pdf", mime: "application/pdf", data: data)
    }

    /// From raw data of a known type: images are decoded and shrunk, PDFs taken whole.
    static func from(data: Data, type: UTType, name: String) -> AttachedFile? {
        if type.conforms(to: .pdf) {
            return pdf(data, name: name)
        }
        if type.conforms(to: .image), let img = UIImage(data: data) {
            return image(img, name: name)
        }
        return nil
    }

    /// Draws the image at most maxSide on its long side, upright, as JPEG.
    static func jpeg(_ image: UIImage, maxSide: CGFloat, quality: CGFloat) -> Data? {
        let size = image.size
        guard size.width > 0, size.height > 0 else { return nil }
        let scale = min(1, maxSide / max(size.width, size.height))
        let target = CGSize(width: (size.width * scale).rounded(.down), height: (size.height * scale).rounded(.down))
        let format = UIGraphicsImageRendererFormat()
        format.scale = 1
        format.opaque = true
        let drawn = UIGraphicsImageRenderer(size: target, format: format).image { _ in
            UIColor.white.setFill()
            UIRectFill(CGRect(origin: .zero, size: target))
            image.draw(in: CGRect(origin: .zero, size: target))
        }
        return drawn.jpegData(compressionQuality: quality)
    }

    /// The name without its extension, or "image" for an empty one.
    static func stem(_ name: String) -> String {
        let base = (name as NSString).deletingPathExtension.trimmingCharacters(in: .whitespacesAndNewlines)
        return base.isEmpty ? "image" : base
    }
}

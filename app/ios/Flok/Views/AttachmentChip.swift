// A file waiting to go with the next reply: its thumbnail, name and size, and a way to drop it.
import SwiftUI

struct AttachmentChip: View {
    let file: AttachedFile
    let onRemove: () -> Void

    var body: some View {
        HStack(spacing: 8) {
            if file.mime.hasPrefix("image/"), let img = UIImage(data: file.data) {
                Image(uiImage: img).resizable().scaledToFill().frame(width: 36, height: 36).clipShape(RoundedRectangle(cornerRadius: 6))
            } else {
                Image(systemName: "doc.richtext").font(.title2).frame(width: 36, height: 36)
            }
            VStack(alignment: .leading, spacing: 1) {
                Text(file.name).font(.footnote).lineLimit(1)
                Text("\(file.sizeLabel) · typed as its path after your words").font(.caption2).foregroundStyle(.secondary).lineLimit(1)
            }
            Spacer()
            Button(action: onRemove) { Image(systemName: "xmark.circle.fill").foregroundStyle(.secondary) }
                .buttonStyle(.borderless)
                .accessibilityLabel("Remove the file")
        }
        .padding(6)
        .background(Color(UIColor.secondarySystemBackground), in: RoundedRectangle(cornerRadius: 8))
    }
}

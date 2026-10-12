// The brand guide (assets/brand/README.md) in code, for what wears it outright: the expanded
// notification. flok's state colours are the brand palette, on the terminal ground; the type is
// JetBrains Mono (bundled, SIL OFL 1.1, Fonts/), else the system's monospaced face.
import CoreText
import SwiftUI
import UIKit

enum Brand {
    static let ground = Color(hex: 0x14181A)
    static let raised = Color(hex: 0x1C2225) // a panel on the ground
    static let teal = Color(hex: 0x12999D) // W4J teal: who made this
    static let working = Color(hex: 0x3FD0D4)
    static let blocked = Color(hex: 0xE8963C) // attention
    static let done = Color(hex: 0x5FC47A)
    static let idle = Color(hex: 0x8A999C) // muted
    static let ink = Color(hex: 0xE4ECED) // text on the ground

    /// JetBrains Mono at a fixed size (700 for bold), registered from the bundle on first use.
    static func mono(_ size: CGFloat, bold: Bool = false) -> Font {
        _ = fontsRegistered
        let name = bold ? "JetBrainsMono-Bold" : "JetBrainsMono-Regular"
        if UIFont(name: name, size: size) != nil {
            return .custom(name, fixedSize: size)
        }
        return .system(size: size, weight: bold ? .bold : .regular, design: .monospaced)
    }

    private static let fontsRegistered: Bool = {
        for name in ["JetBrainsMono-Regular", "JetBrainsMono-Bold"] {
            if let url = Bundle.main.url(forResource: name, withExtension: "ttf") {
                CTFontManagerRegisterFontsForURL(url as CFURL, .process, nil)
            }
        }
        return true
    }()

    /// The [flok] wordmark: the frame in the brand accent, the name in the foreground.
    static func wordmark(_ size: CGFloat) -> Text {
        Text("\(Text("[").foregroundStyle(working))\(Text("flok").foregroundStyle(ink))\(Text("]").foregroundStyle(working))")
            .font(mono(size, bold: true))
    }
}

extension Color {
    init(hex: UInt32) {
        self.init(red: Double(hex >> 16 & 0xFF) / 255, green: Double(hex >> 8 & 0xFF) / 255, blue: Double(hex & 0xFF) / 255)
    }
}

// The camera looking for a flok QR: the pairing one (flok://pair…, from flok phone pair) or a
// Mac's join one (flok://link…, from flok link join). VisionKit's scanner draws its own
// viewfinder and asks for the camera itself. The first code that parse reads ends the scan. Not
// every device has it (no camera, the simulator): canScan says, and the code can be typed
// instead.
import SwiftUI
import VisionKit

struct QRScanner: UIViewControllerRepresentable {
    /// What a scanned link must read as: a pairing by default, a join for the approve sheet.
    var parse: (URL) -> (relay: String, code: String)? = FlokLink.pairing
    var onFound: @MainActor (_ relay: String, _ code: String) -> Void

    static var canScan: Bool { DataScannerViewController.isSupported && DataScannerViewController.isAvailable }

    func makeUIViewController(context: Context) -> DataScannerViewController {
        let vc = DataScannerViewController(recognizedDataTypes: [.barcode(symbologies: [.qr])], qualityLevel: .balanced,
                                           recognizesMultipleItems: false, isHighlightingEnabled: true)
        vc.delegate = context.coordinator
        return vc
    }

    func updateUIViewController(_ vc: DataScannerViewController, context: Context) {
        if !vc.isScanning { try? vc.startScanning() } // once it is on screen; a second update is a no-op
    }

    func makeCoordinator() -> Coordinator { Coordinator(parse: parse, onFound: onFound) }

    @MainActor
    final class Coordinator: NSObject, DataScannerViewControllerDelegate {
        private let parse: (URL) -> (relay: String, code: String)?
        private let onFound: @MainActor (String, String) -> Void
        private var done = false

        init(parse: @escaping (URL) -> (relay: String, code: String)?, onFound: @escaping @MainActor (String, String) -> Void) {
            self.parse = parse
            self.onFound = onFound
        }

        func dataScanner(_ scanner: DataScannerViewController, didAdd added: [RecognizedItem], allItems: [RecognizedItem]) {
            guard !done else { return }
            for item in added {
                if case .barcode(let b) = item, let s = b.payloadStringValue, let url = URL(string: s), let p = parse(url) {
                    done = true
                    scanner.stopScanning()
                    onFound(p.relay, p.code)
                    return
                }
            }
        }
    }
}

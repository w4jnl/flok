// The camera looking for the pairing QR (flok://pair…): VisionKit's scanner, which draws its own
// viewfinder and asks for the camera itself. The first code that reads as a pairing link ends
// the scan. Not every device has it (no camera, the simulator): canScan says, and the code can
// be typed instead.
import SwiftUI
import VisionKit

struct QRScanner: UIViewControllerRepresentable {
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

    func makeCoordinator() -> Coordinator { Coordinator(onFound: onFound) }

    @MainActor
    final class Coordinator: NSObject, DataScannerViewControllerDelegate {
        private let onFound: @MainActor (String, String) -> Void
        private var done = false

        init(onFound: @escaping @MainActor (String, String) -> Void) { self.onFound = onFound }

        func dataScanner(_ scanner: DataScannerViewController, didAdd added: [RecognizedItem], allItems: [RecognizedItem]) {
            guard !done else { return }
            for item in added {
                if case .barcode(let b) = item, let s = b.payloadStringValue, let url = URL(string: s), let p = FlokLink.pairing(url) {
                    done = true
                    scanner.stopScanning()
                    onFound(p.relay, p.code)
                    return
                }
            }
        }
    }
}

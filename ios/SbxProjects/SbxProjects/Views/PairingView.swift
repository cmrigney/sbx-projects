import SwiftUI
import UIKit
internal import Vision
import VisionKit

/// Shown until the iPhone is paired with a Mac.
struct PairingView: View {
    @Environment(AppModel.self) private var app
    @State private var scanning = false
    @State private var picked: MacBrowser.Found?

    var body: some View {
        NavigationStack {
            List {
                Section {
                    VStack(alignment: .leading, spacing: 10) {
                        Image(systemName: "terminal.fill")
                            .font(.system(size: 34))
                            .foregroundStyle(.tint)
                        Text("Connect to your Mac")
                            .font(.title2.bold())
                        Text("On your Mac, run this in a terminal, then scan the code it shows.")
                            .foregroundStyle(.secondary)
                        Text("sbx-projects api --pair")
                            .font(.callout.monospaced())
                            .padding(.horizontal, 10)
                            .padding(.vertical, 6)
                            .background(Color(.tertiarySystemFill), in: RoundedRectangle(cornerRadius: 8))
                            .textSelection(.enabled)
                    }
                    .padding(.vertical, 8)
                }

                Section {
                    Button { scanning = true } label: {
                        Label("Scan pairing code", systemImage: "qrcode.viewfinder")
                    }
                }

                Section {
                    if app.browser.found.isEmpty {
                        HStack(spacing: 10) {
                            ProgressView()
                            Text("Looking for Macs running sbx-projects api").foregroundStyle(.secondary)
                        }
                    }
                    ForEach(app.browser.found) { mac in
                        Button { picked = mac } label: {
                            HStack {
                                Label(mac.name, systemImage: "laptopcomputer")
                                Spacer()
                                Image(systemName: "chevron.right").font(.footnote.weight(.semibold)).foregroundStyle(.tertiary)
                            }
                        }
                        .tint(.primary)
                    }
                } header: {
                    Text("On this network")
                } footer: {
                    if let error = app.browser.error {
                        Text("\(error) Check that Local Network access is on for sbx-projects in Settings.")
                    } else {
                        Text("Pick a Mac to type its code instead of scanning.")
                    }
                }
            }
            .navigationTitle("sbx-projects")
            .navigationBarTitleDisplayMode(.inline)
            .onAppear { app.browser.start() }
            .onDisappear { app.browser.stop() }
            .sheet(isPresented: $scanning) {
                ScanSheet { link in
                    scanning = false
                    Task { await app.pair(link: link) }
                }
            }
            .sheet(item: $picked) { EnterCodeView(mac: $0) }
            .overlay {
                if app.pairing {
                    ProgressView("Pairing")
                        .padding(24)
                        .background(.regularMaterial, in: RoundedRectangle(cornerRadius: 16))
                }
            }
            .alert("Couldn't pair", isPresented: Binding(get: { app.pairingError != nil && picked == nil }, set: { if !$0 { app.pairingError = nil } })) {
                Button("OK", role: .cancel) {}
            } message: {
                Text(app.pairingError ?? "")
            }
        }
    }
}

/// Pairing with a Mac picked from the network. Without the QR code there is
/// no fingerprint to pin in advance, so the person compares the one the
/// Mac sent with the one its terminal shows.
private struct EnterCodeView: View {
    let mac: MacBrowser.Found

    @Environment(AppModel.self) private var app
    @Environment(\.dismiss) private var dismiss
    @State private var address: ServerAddress?
    @State private var fingerprint: String?
    @State private var error: String?
    @State private var code = ""
    @FocusState private var focused: Bool

    private var cleanCode: String {
        code.uppercased().filter { $0.isLetter || $0.isNumber }
    }

    var body: some View {
        NavigationStack {
            Form {
                if let fingerprint, address != nil {
                    Section {
                        Text(formatFingerprint(fingerprint))
                            .font(.callout.monospaced())
                            .textSelection(.enabled)
                    } header: {
                        Text("Certificate")
                    } footer: {
                        Text("This should match the Certificate line in the terminal on \(mac.name). If it doesn't, something else on the network is answering; don't pair.")
                    }
                    Section {
                        TextField("XXXX-XXXX", text: $code)
                            .font(.title3.monospaced())
                            .textInputAutocapitalization(.characters)
                            .autocorrectionDisabled()
                            .focused($focused)
                            .onSubmit(pair)
                    } header: {
                        Text("Pairing code")
                    } footer: {
                        Text("Shown under the QR code. It works once, for 5 minutes.")
                    }
                    if let e = app.pairingError {
                        Section { Text(e).foregroundStyle(.red) }
                    }
                } else if let error {
                    Section { Text(error).foregroundStyle(.red) }
                } else {
                    Section { HStack { ProgressView(); Text("Connecting to \(mac.name)").foregroundStyle(.secondary) } }
                }
            }
            .navigationTitle(mac.name)
            .navigationBarTitleDisplayMode(.inline)
            .toolbar {
                ToolbarItem(placement: .cancellationAction) { Button("Cancel") { dismiss() } }
                ToolbarItem(placement: .confirmationAction) {
                    if app.pairing {
                        ProgressView()
                    } else {
                        Button("Pair", action: pair).disabled(cleanCode.count != 8 || fingerprint == nil)
                    }
                }
            }
            .task { await connect() }
            .onChange(of: app.server) { _, server in
                if server != nil { dismiss() }
            }
        }
    }

    private func connect() async {
        app.pairingError = nil
        do {
            let a = try await MacBrowser.resolve(mac.endpoint)
            let c = APIClient(address: a, fingerprint: nil, token: nil)
            let info = try await c.serverInfo(timeout: 6)
            guard let fp = c.seenFingerprint, fp.hasPrefix(info.id) else {
                error = "\(mac.name) didn't present the certificate it advertises."
                return
            }
            if !info.pairing {
                error = "Pairing isn't open on \(mac.name). Run sbx-projects api --pair there, then try again."
                return
            }
            address = a
            fingerprint = fp
            focused = true
        } catch {
            self.error = "Couldn't connect to \(mac.name): \(error.localizedDescription)"
        }
    }

    private func pair() {
        guard let address, let fingerprint, cleanCode.count == 8 else { return }
        Task { await app.pair(address: address, fingerprint: fingerprint, code: cleanCode) }
    }
}

/// The camera, looking for an sbxp:// QR code.
private struct ScanSheet: View {
    let onLink: (String) -> Void
    @Environment(\.dismiss) private var dismiss

    var body: some View {
        NavigationStack {
            Group {
                if DataScannerViewController.isSupported && DataScannerViewController.isAvailable {
                    QRScanner(onLink: onLink).ignoresSafeArea()
                } else {
                    ContentUnavailableView("Scanning isn't available", systemImage: "camera",
                                           description: Text("Allow camera access for sbx-projects in Settings, or scan the code with the Camera app. It opens sbx-projects."))
                }
            }
            .navigationTitle("Scan pairing code")
            .navigationBarTitleDisplayMode(.inline)
            .toolbar { ToolbarItem(placement: .cancellationAction) { Button("Cancel") { dismiss() } } }
        }
    }
}

private struct QRScanner: UIViewControllerRepresentable {
    let onLink: (String) -> Void

    func makeUIViewController(context: Context) -> DataScannerViewController {
        let vc = DataScannerViewController(
            recognizedDataTypes: [.barcode(symbologies: [.qr])],
            qualityLevel: .balanced,
            recognizesMultipleItems: false,
            isHighFrameRateTrackingEnabled: false,
            isHighlightingEnabled: true)
        vc.delegate = context.coordinator
        return vc
    }

    func updateUIViewController(_ vc: DataScannerViewController, context: Context) {
        if !vc.isScanning {
            try? vc.startScanning()
        }
    }

    static func dismantleUIViewController(_ vc: DataScannerViewController, coordinator: Coordinator) {
        vc.stopScanning()
    }

    func makeCoordinator() -> Coordinator { Coordinator(onLink: onLink) }

    final class Coordinator: NSObject, DataScannerViewControllerDelegate {
        let onLink: (String) -> Void
        private var done = false

        init(onLink: @escaping (String) -> Void) {
            self.onLink = onLink
        }

        func dataScanner(_ dataScanner: DataScannerViewController, didAdd addedItems: [RecognizedItem], allItems: [RecognizedItem]) {
            guard !done else { return }
            for item in addedItems {
                if case .barcode(let code) = item, let s = code.payloadStringValue, s.hasPrefix("sbxp://") {
                    done = true
                    dataScanner.stopScanning()
                    onLink(s)
                    return
                }
            }
        }
    }
}

import SwiftUI
import UIKit

struct SettingsView: View {
    @Environment(AppModel.self) private var app
    @State private var confirmForget = false

    var body: some View {
        NavigationStack {
            Form {
                if let server = app.server {
                    Section("Mac") {
                        LabeledContent("Name", value: server.name)
                        LabeledContent("Address") {
                            Text(server.address.display).font(.body.monospaced())
                        }
                        LabeledContent("Status", value: status)
                        VStack(alignment: .leading, spacing: 4) {
                            Text("Certificate")
                            Text(formatFingerprint(server.fingerprint))
                                .font(.footnote.monospaced())
                                .foregroundStyle(.secondary)
                                .textSelection(.enabled)
                        }
                    }
                    Section {
                        Button("Forget This Mac", role: .destructive) { confirmForget = true }
                    } footer: {
                        Text("To sign this iPhone out on the Mac too, run sbx-projects api devices rm there.")
                    }
                }
                Section("About") {
                    LabeledContent("Version", value: Bundle.main.object(forInfoDictionaryKey: "CFBundleShortVersionString") as? String ?? "")
                    Link("sbx-projects on GitHub", destination: URL(string: "https://github.com/cmrigney/sbx-projects")!)
                }
            }
            .navigationTitle("Settings")
            .confirmationDialog("Forget \(app.server?.name ?? "this Mac")?", isPresented: $confirmForget, titleVisibility: .visible) {
                Button("Forget", role: .destructive) { app.forget() }
            } message: {
                Text("You'll need to pair again to see its sandboxes.")
            }
        }
    }

    private var status: String {
        switch app.connection {
        case .live: "Connected"
        case .connecting: "Connecting"
        case .offline: "Can't connect"
        case .signedOut: "Signed out"
        }
    }
}

import SwiftUI
import UIKit

struct SettingsView: View {
    @Environment(AppModel.self) private var app
    @State private var forgetting: PairedServer?
    @State private var addingMac = false

    var body: some View {
        NavigationStack {
            Form {
                Section {
                    ForEach(app.servers) { s in
                        Button { app.select(s.id) } label: {
                            HStack {
                                VStack(alignment: .leading, spacing: 2) {
                                    Text(s.name)
                                    Text(s.address.display).font(.footnote.monospaced()).foregroundStyle(.secondary)
                                }
                                Spacer()
                                if s.id == app.server?.id {
                                    Image(systemName: "checkmark").fontWeight(.semibold).foregroundStyle(Color.accentColor)
                                }
                            }
                        }
                        .tint(.primary)
                        .swipeActions {
                            Button("Forget", role: .destructive) { forgetting = s }
                        }
                    }
                    Button { addingMac = true } label: { Label("Pair Another Mac", systemImage: "plus") }
                } header: {
                    Text("Macs")
                } footer: {
                    if app.servers.count > 1 {
                        Text("Tap a Mac to see its sandboxes.")
                    }
                }

                if let server = app.server {
                    Section(server.name) {
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
                        Button("Forget This Mac", role: .destructive) { forgetting = server }
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
            .confirmationDialog("Forget \(forgetting?.name ?? "this Mac")?", isPresented: Binding(get: { forgetting != nil }, set: { if !$0 { forgetting = nil } }), titleVisibility: .visible, presenting: forgetting) { s in
                Button("Forget", role: .destructive) { app.forget(s.id) }
            } message: { _ in
                Text("You'll need to pair again to see its sandboxes.")
            }
            .sheet(isPresented: $addingMac) { PairingView(adding: true) }
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

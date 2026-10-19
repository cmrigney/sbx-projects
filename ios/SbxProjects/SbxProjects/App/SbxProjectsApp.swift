import SwiftUI
import UIKit

@main
struct SbxProjectsApp: App {
    @State private var app = AppModel()
    @Environment(\.scenePhase) private var scenePhase

    var body: some Scene {
        WindowGroup {
            RootView()
                .environment(app)
                // The Camera app opens sbxp:// pairing codes here too.
                .onOpenURL { url in
                    guard url.scheme == "sbxp" else { return }
                    Task { await app.pair(link: url.absoluteString) }
                }
                .onChange(of: scenePhase, initial: true) { _, phase in
                    // The stream can't run in the background; reconnecting
                    // starts from a fresh snapshot.
                    if phase == .active {
                        app.connect()
                    } else if phase == .background {
                        app.disconnect()
                    }
                }
        }
    }
}

struct RootView: View {
    @Environment(AppModel.self) private var app

    var body: some View {
        Group {
            if app.server == nil {
                PairingView()
            } else {
                TabView {
                    SandboxListView()
                        .tabItem { Label("Sandboxes", systemImage: "terminal") }
                    SettingsView()
                        .tabItem { Label("Settings", systemImage: "gearshape") }
                }
            }
        }
        .overlay(alignment: .top) { ToastView() }
    }
}

struct ToastView: View {
    @Environment(AppModel.self) private var app

    var body: some View {
        if let toast = app.toast {
            Label(toast.message, systemImage: toast.ok ? "checkmark.circle.fill" : "exclamationmark.triangle.fill")
                .font(.subheadline.weight(.medium))
                .symbolRenderingMode(.multicolor)
                .padding(.horizontal, 16)
                .padding(.vertical, 10)
                .background(.regularMaterial, in: Capsule())
                .shadow(color: .black.opacity(0.12), radius: 12, y: 4)
                .padding(.horizontal)
                .transition(.move(edge: .top).combined(with: .opacity))
                .onTapGesture { app.toast = nil }
                .task(id: toast.id) {
                    try? await Task.sleep(for: .seconds(toast.ok ? 3 : 6))
                    withAnimation { app.toast = nil }
                }
                .id(toast.id)
        }
    }
}

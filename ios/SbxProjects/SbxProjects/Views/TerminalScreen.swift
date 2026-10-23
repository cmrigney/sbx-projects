import SwiftTerm
import SwiftUI
import UIKit

/// The agent's session, as Enter attaches to it in the TUI. Leaving
/// detaches; the agent keeps running.
struct TerminalScreen: View {
    let name: String

    @Environment(AppModel.self) private var app
    @Environment(\.dismiss) private var dismiss
    @Environment(\.scenePhase) private var scenePhase
    @State private var session = TerminalSession()
    /// Detached because the app left the foreground, to attach again when
    /// it's back.
    @State private var paused = false

    var body: some View {
        VStack(spacing: 0) {
            bar
            ZStack {
                TerminalHost(session: session) { view in
                    session.attach(view) { try app.terminal(name, cols: $0, rows: $1) }
                }
                if case .closed(let message, let again) = session.phase {
                    closed(message, again: again)
                }
            }
        }
        .background(SwiftUI.Color(uiColor: TerminalSession.background))
        // The terminal is always dark, as the agents' own UIs expect.
        .environment(\.colorScheme, .dark)
        .onChange(of: scenePhase) { _, phase in
            // A socket can't stay open in the background; tmux redraws the
            // whole screen on attaching again, so nothing is lost.
            if phase == .background {
                paused = session.detach()
            } else if phase == .active, paused {
                paused = false
                session.reattach()
            }
        }
        .onDisappear { session.detach() }
    }

    private var bar: some View {
        HStack(spacing: 12) {
            Button {
                session.detach()
                dismiss()
            } label: {
                Label("Detach", systemImage: "chevron.left")
            }
            .fontWeight(.medium)
            Spacer()
            HStack(spacing: 8) {
                Circle().fill(dot).frame(width: 8, height: 8)
                Text(name).font(.subheadline.monospaced().weight(.semibold)).foregroundStyle(Theme.text)
            }
            Spacer()
            Text("ctrl+b d").font(.mono).foregroundStyle(Theme.dim)
        }
        .padding(.horizontal)
        .padding(.vertical, 10)
        .background(Theme.panel)
        .overlay(alignment: .bottom) { Rectangle().fill(Theme.line).frame(height: 1) }
    }

    private var dot: SwiftUI.Color {
        switch session.phase {
        case .connecting: Theme.needs
        case .open: Theme.ready
        case .closed, .detached: Theme.parked
        }
    }

    private func closed(_ message: String, again: Bool) -> some View {
        VStack(spacing: 16) {
            Text(message)
                .font(.subheadline)
                .foregroundStyle(Theme.text)
                .multilineTextAlignment(.center)
            HStack(spacing: 12) {
                if again {
                    Button {
                        session.reattach()
                    } label: {
                        Label("Attach again", systemImage: "apple.terminal")
                    }
                    .buttonStyle(.borderedProminent)
                    .tint(Theme.accentFill)
                    .foregroundStyle(Theme.accentInk)
                }
                Button("Back to \(name)") { dismiss() }
                    .buttonStyle(.bordered)
            }
        }
        .padding(20)
        .background(Theme.panel, in: RoundedRectangle(cornerRadius: Theme.radius))
        .overlay(RoundedRectangle(cornerRadius: Theme.radius).stroke(Theme.line))
        .padding()
    }
}

/// The agent's terminal over the Mac's WebSocket: output is fed to the
/// view, and what the view sends, keystrokes and resizes, goes back.
@Observable
final class TerminalSession {
    enum Phase: Equatable {
        case connecting
        case open
        case closed(message: String, again: Bool)
        /// Left from this side.
        case detached
    }

    private(set) var phase: Phase = .connecting

    @ObservationIgnored private weak var view: TerminalView?
    @ObservationIgnored private var open: ((Int, Int) throws -> URLSessionWebSocketTask)?
    @ObservationIgnored private var task: URLSessionWebSocketTask?

    /// Attaches the view, once it has its size, to a connection that `open`
    /// makes for that many columns and rows.
    func attach(_ view: TerminalView, open: @escaping (Int, Int) throws -> URLSessionWebSocketTask) {
        self.view = view
        self.open = open
        connect()
    }

    /// Attaches again after the connection closed or was detached.
    func reattach() {
        // Start from a blank screen; tmux redraws all of it.
        view?.feed(text: "\u{1b}c")
        connect()
    }

    /// Leaves the session; the agent keeps running. Returns whether there
    /// was a connection to leave.
    @discardableResult
    func detach() -> Bool {
        guard let t = task else { return false }
        task = nil
        t.cancel(with: .normalClosure, reason: nil)
        phase = .detached
        return true
    }

    private func connect() {
        guard let view, let open else { return }
        task?.cancel(with: .goingAway, reason: nil)
        phase = .connecting
        let terminal = view.getTerminal()
        let t: URLSessionWebSocketTask
        do {
            t = try open(terminal.cols, terminal.rows)
        } catch {
            close(.closed(message: error.localizedDescription, again: false))
            return
        }
        task = t
        t.resume()
        _ = view.becomeFirstResponder()
        Task { await receive(t) }
        Task { await keepAlive(t) }
    }

    private func receive(_ t: URLSessionWebSocketTask) async {
        do {
            while true {
                let message = try await t.receive()
                guard task === t else { return }
                if case .data(let data) = message {
                    phase = .open
                    view?.feed(byteArray: [UInt8](data)[...])
                }
            }
        } catch {
            guard task === t else { return }
            task = nil
            close(Self.phase(closing: t))
        }
    }

    /// The Mac pings every 30 seconds too; pinging from here as well keeps
    /// the request's timeout from ending a quiet terminal.
    private func keepAlive(_ t: URLSessionWebSocketTask) async {
        while true {
            try? await Task.sleep(for: .seconds(25))
            guard task === t else { return }
            t.sendPing { _ in }
        }
    }

    private func close(_ p: Phase) {
        phase = p
        // Out of the way of the buttons to attach again or go back.
        _ = view?.resignFirstResponder()
    }

    /// Why the Mac closed the terminal, from its close codes, whose reasons
    /// are written for people.
    private static func phase(closing t: URLSessionWebSocketTask) -> Phase {
        let reason = t.closeReason.flatMap { String(data: $0, encoding: .utf8) } ?? ""
        switch t.closeCode.rawValue {
        case 1000:
            return .closed(message: reason.isEmpty ? "Detached. The agent keeps running." : reason, again: true)
        case 4000:
            return .closed(message: reason, again: true)
        case 4001:
            return .closed(message: reason, again: false)
        default:
            if (t.response as? HTTPURLResponse)?.statusCode == 401 {
                return .closed(message: "This iPhone isn't paired with the Mac any more. Pair it again in Settings.", again: false)
            }
            return .closed(message: "Lost the connection to the Mac.", again: true)
        }
    }

    private func send(_ message: URLSessionWebSocketTask.Message) {
        // Sent in the order they're called, so keystrokes stay in order.
        task?.send(message) { _ in }
    }

    // MARK: Colours

    // The app's dark colours, as the web app's terminal has them.
    static let background = UIColor(hex: 0x0A141D)
    static let foreground = UIColor(hex: 0xE4EFEC)
    static let caret = UIColor(hex: 0x85E8CD)

    /// The 16 ANSI colours: black, red, green, yellow, blue, magenta, cyan
    /// and white, then their bright versions.
    static let palette: [SwiftTerm.Color] = [
        0x101E2B, 0xFF7B6E, 0x6FDC9A, 0xF5B94A, 0x7CC4FF, 0xD3A6FF, 0x85E8CD, 0xE4EFEC,
        0x5F7787, 0xFF9D93, 0x94E8B5, 0xFFD07A, 0xA5D6FF, 0xE2C2FF, 0xB0F2DF, 0xFFFFFF,
    ].map { (hex: UInt32) in
        SwiftTerm.Color(red8: UInt16((hex >> 16) & 0xFF), green8: UInt16((hex >> 8) & 0xFF), blue8: UInt16(hex & 0xFF))
    }
}

extension TerminalSession: @preconcurrency TerminalViewDelegate {
    func send(source: TerminalView, data: ArraySlice<UInt8>) {
        send(.data(Data(data)))
    }

    func sizeChanged(source: TerminalView, newCols: Int, newRows: Int) {
        send(.string(#"{"type":"resize","cols":\#(newCols),"rows":\#(newRows)}"#))
    }

    func requestOpenLink(source: TerminalView, link: String, params: [String: String]) {
        if let url = URL(string: link) {
            UIApplication.shared.open(url)
        }
    }

    /// Agents copy with OSC 52, which lands on the iPhone's clipboard.
    func clipboardCopy(source: TerminalView, content: Data) {
        if let text = String(data: content, encoding: .utf8) {
            UIPasteboard.general.string = text
        }
    }

    func setTerminalTitle(source: TerminalView, title: String) {}
    func hostCurrentDirectoryUpdate(source: TerminalView, directory: String?) {}
    func scrolled(source: TerminalView, position: Double) {}
    func rangeChanged(source: TerminalView, startY: Int, endY: Int) {}
}

/// SwiftTerm's view, with its key bar for Ctrl, Esc, Tab and the arrows
/// above the keyboard. It's handed over once it has a size, so the first
/// attach asks for the right number of columns and rows.
private struct TerminalHost: UIViewRepresentable {
    let session: TerminalSession
    let ready: (TerminalView) -> Void

    func makeUIView(context: Context) -> SizedTerminalView {
        let size: CGFloat = UIDevice.current.userInterfaceIdiom == .pad ? 14 : 12
        let view = SizedTerminalView(frame: .zero, font: .monospacedSystemFont(ofSize: size, weight: .regular))
        view.terminalDelegate = session
        view.installColors(TerminalSession.palette)
        view.nativeBackgroundColor = TerminalSession.background
        view.nativeForegroundColor = TerminalSession.foreground
        view.caretColor = TerminalSession.caret
        view.onSized = ready
        return view
    }

    func updateUIView(_ view: SizedTerminalView, context: Context) {}
}

private final class SizedTerminalView: TerminalView {
    var onSized: ((TerminalView) -> Void)?

    override func layoutSubviews() {
        super.layoutSubviews()
        guard bounds.width > 0, bounds.height > 0, let onSized else { return }
        self.onSized = nil
        onSized(self)
    }
}

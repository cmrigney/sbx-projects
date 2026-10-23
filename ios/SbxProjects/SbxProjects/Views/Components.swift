import SwiftUI
import UIKit

/// "Sandbox terminal", as the web app has it: the app icon's navy and mint,
/// with a terminal's mono type for anything a machine says. Dark is the icon
/// itself; light is the same ink on paper.
enum Theme {
    static let bg = adaptive(light: 0xEEF3F1, dark: 0x0A141D)
    static let panel = adaptive(light: 0xFFFFFF, dark: 0x101E2B)
    static let panel2 = adaptive(light: 0xF4F8F6, dark: 0x152738)
    static let line = adaptive(light: 0xD3DFDB, dark: 0x213749)
    static let text = adaptive(light: 0x101E2B, dark: 0xE4EFEC)
    static let muted = adaptive(light: 0x4B6474, dark: 0x93A9B5)
    static let dim = adaptive(light: 0x7A909D, dark: 0x5F7787)
    /// Links, selection and the active tab: deep teal on paper, mint on navy.
    static let accent = adaptive(light: 0x17806F, dark: 0x85E8CD)
    /// The filled mint of primary buttons, the same in both.
    static let accentFill = Color(hex: 0x85E8CD)
    /// Text on accentFill.
    static let accentInk = Color(hex: 0x101E2B)

    static let needs = adaptive(light: 0xB86E00, dark: 0xF5B94A)
    static let fail = adaptive(light: 0xC8372B, dark: 0xFF7B6E)
    static let work = adaptive(light: 0x1F6FB2, dark: 0x7CC4FF)
    static let ready = adaptive(light: 0x17806F, dark: 0x85E8CD)
    static let parked = adaptive(light: 0x6F8593, dark: 0x6F8797)
    static let add = adaptive(light: 0x1D8A4A, dark: 0x6FDC9A)
    static let del = adaptive(light: 0xC8372B, dark: 0xFF7B6E)

    static let radius: CGFloat = 12

    private static func adaptive(light: UInt32, dark: UInt32) -> Color {
        Color(UIColor { $0.userInterfaceStyle == .dark ? UIColor(hex: dark) : UIColor(hex: light) })
    }
}

extension UIColor {
    convenience init(hex: UInt32) {
        self.init(red: CGFloat((hex >> 16) & 0xFF) / 255, green: CGFloat((hex >> 8) & 0xFF) / 255,
                  blue: CGFloat(hex & 0xFF) / 255, alpha: 1)
    }
}

extension Color {
    init(hex: UInt32) { self.init(uiColor: UIColor(hex: hex)) }
}

extension SandboxState {
    var color: Color {
        switch self {
        case .needsYou: Theme.needs
        case .working, .starting: Theme.work
        case .ready: Theme.ready
        case .failed: Theme.fail
        case .idle, .stopped: Theme.parked
        }
    }

    /// The state as a terminal would print it.
    var word: String {
        switch self {
        case .needsYou: "needs you"
        case .failed: "failed"
        case .working: "working"
        case .starting: "starting"
        case .ready: "ready"
        case .idle: "idle"
        case .stopped: "stopped"
        }
    }
}

/// A sandbox's state as a tinted square, like the sandbox in the app icon.
/// A working agent shows a blinking cursor.
struct StatusBadge: View {
    let state: SandboxState
    var size: CGFloat = 26

    var body: some View {
        let color = state.color
        let shape = RoundedRectangle(cornerRadius: size * 0.27, style: .continuous)
        Group {
            switch state {
            case .working, .starting:
                BlinkingCursor(width: (size * 0.3).rounded(), height: (size * 0.55).rounded())
            case .idle:
                Circle().frame(width: size * 0.22, height: size * 0.22)
            default:
                Image(systemName: symbol).font(.system(size: size * 0.42, weight: .heavy))
            }
        }
        .foregroundStyle(color)
        .frame(width: size, height: size)
        .background(color.opacity(0.15), in: shape)
        .overlay(shape.strokeBorder(color.opacity(0.32), lineWidth: 1))
        .accessibilityElement()
        .accessibilityLabel(StatusGroup(state).label)
    }

    private var symbol: String {
        switch state {
        case .needsYou: "questionmark"
        case .ready: "checkmark"
        case .failed: "xmark"
        default: "pause.fill"
        }
    }
}

/// A terminal's block cursor, blinking unless motion is reduced.
struct BlinkingCursor: View {
    let width: CGFloat
    let height: CGFloat

    @Environment(\.accessibilityReduceMotion) private var reduceMotion

    var body: some View {
        TimelineView(.periodic(from: .now, by: 0.525)) { ctx in
            let on = reduceMotion || Int(ctx.date.timeIntervalSinceReferenceDate / 0.525) % 2 == 0
            RoundedRectangle(cornerRadius: 1)
                .frame(width: width, height: height)
                .opacity(on ? 1 : 0)
        }
    }
}

/// The state in words beside its mark: "▍WORKING".
struct StatusTag: View {
    let state: SandboxState

    var body: some View {
        HStack(spacing: 7) {
            StatusBadge(state: state, size: 18)
            Text(state.word.uppercased())
                .font(.system(size: 12, weight: .semibold, design: .monospaced))
                .tracking(0.7)
        }
        .foregroundStyle(state.color)
    }
}

/// Lines added and removed, in green and red.
struct ChangeCounts: View {
    let added: Int
    let removed: Int

    var body: some View {
        HStack(spacing: 4) {
            Text("+\(added)").foregroundStyle(Theme.add)
            Text("−\(removed)").foregroundStyle(Theme.del)
        }
        .fontDesign(.monospaced)
        .monospacedDigit()
    }
}

/// A branch name after git's branch mark.
struct BranchLabel: View {
    let branch: String

    var body: some View {
        HStack(spacing: 4) {
            Image(systemName: "arrow.triangle.branch").imageScale(.small)
            Text(branch).lineLimit(1).truncationMode(.middle)
        }
    }
}

/// A bordered panel, for content that isn't a List.
struct Card<Content: View>: View {
    @ViewBuilder var content: Content

    var body: some View {
        VStack(alignment: .leading, spacing: 0) { content }
            .frame(maxWidth: .infinity, alignment: .leading)
            .background(Theme.panel, in: RoundedRectangle(cornerRadius: Theme.radius, style: .continuous))
            .overlay(RoundedRectangle(cornerRadius: Theme.radius, style: .continuous).strokeBorder(Theme.line, lineWidth: 1))
    }
}

/// A hairline between a Card's rows.
struct RowDivider: View {
    var leading: CGFloat = 0

    var body: some View {
        Rectangle().fill(Theme.line).frame(height: 1).padding(.leading, leading)
    }
}

/// A small caps heading over a card, as a terminal prints a section.
struct SectionLabel: View {
    let text: String

    var body: some View {
        Text(text.uppercased())
            .font(.system(size: 11.5, weight: .medium, design: .monospaced))
            .tracking(0.7)
            .foregroundStyle(Theme.dim)
    }
}

/// The mint button for the one thing a screen is for.
struct PrimaryButtonStyle: ButtonStyle {
    @Environment(\.isEnabled) private var isEnabled

    func makeBody(configuration: Configuration) -> some View {
        configuration.label
            .font(.body.weight(.semibold))
            .foregroundStyle(Theme.accentInk)
            .padding(.horizontal, 14)
            .frame(minHeight: 48)
            .background(Theme.accentFill, in: RoundedRectangle(cornerRadius: Theme.radius, style: .continuous))
            .opacity(isEnabled ? (configuration.isPressed ? 0.75 : 1) : 0.4)
    }
}

/// A bordered panel button beside a PrimaryButtonStyle one.
struct SecondaryButtonStyle: ButtonStyle {
    @Environment(\.isEnabled) private var isEnabled

    func makeBody(configuration: Configuration) -> some View {
        configuration.label
            .font(.body.weight(.semibold))
            .foregroundStyle(Theme.text)
            .padding(.horizontal, 14)
            .frame(minHeight: 48)
            .background(configuration.isPressed ? Theme.panel2 : Theme.panel, in: RoundedRectangle(cornerRadius: Theme.radius, style: .continuous))
            .overlay(RoundedRectangle(cornerRadius: Theme.radius, style: .continuous).strokeBorder(Theme.line, lineWidth: 1))
            .opacity(isEnabled ? 1 : 0.4)
    }
}

extension View {
    /// Puts a List or Form on the app's background, with its rows on panels.
    func themedList() -> some View {
        scrollContentBackground(.hidden)
            .background(Theme.bg)
    }
}

/// "now", "4m", "3h", "2d": how long ago, as short as the TUI shows it.
func shortAgo(_ date: Date, now: Date = .now) -> String {
    let s = Int(now.timeIntervalSince(date))
    switch s {
    case ..<60: return "now"
    case ..<3600: return "\(s / 60)m"
    case ..<86400: return "\(s / 3600)h"
    default: return "\(s / 86400)d"
    }
}

/// A gigabyte figure for a MiB count: 8192 → "8 GB".
func memoryLabel(_ mib: Int64) -> String {
    let gb = Double(mib) / 1024
    return gb.rounded() == gb ? "\(Int(gb)) GB" : String(format: "%.1f GB", gb)
}

extension Font {
    static let mono = Font.system(.footnote, design: .monospaced)
}

/// A panel with a state's rail down its leading edge, behind sandbox rows,
/// banners and callouts.
struct RailBackground: View {
    let rail: Color
    /// How much of the rail's color tints the panel.
    var tint: Double = 0

    var body: some View {
        let shape = RoundedRectangle(cornerRadius: Theme.radius, style: .continuous)
        shape.fill(Theme.panel)
            .overlay(shape.fill(rail.opacity(tint)))
            .overlay(alignment: .leading) { Rectangle().fill(rail.opacity(0.85)).frame(width: 3) }
            .clipShape(shape)
            .overlay(shape.strokeBorder(tint > 0 ? rail.opacity(0.35) : Theme.line, lineWidth: 1))
    }
}

/// The mint "NEW REPLY" tag in front of a reply's first line.
struct NewTag: View {
    var body: some View {
        Text("NEW REPLY")
            .font(.system(size: 10.5, weight: .semibold, design: .monospaced))
            .tracking(0.4)
            .foregroundStyle(Theme.accentInk)
            .padding(.horizontal, 6)
            .padding(.vertical, 2)
            .background(Theme.accentFill, in: RoundedRectangle(cornerRadius: 4, style: .continuous))
    }
}

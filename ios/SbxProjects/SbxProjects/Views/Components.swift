import SwiftUI
import UIKit

extension SandboxState {
    var color: Color {
        switch self {
        case .needsYou: .orange
        case .working, .starting: .teal
        case .ready: .green
        case .failed: .red
        case .idle, .stopped: .gray
        }
    }
}

/// The round status mark in front of each sandbox.
struct StatusBadge: View {
    let state: SandboxState
    var size: CGFloat = 26

    var body: some View {
        Group {
            switch state {
            case .working, .starting:
                ProgressView()
                    .controlSize(.small)
                    .tint(state.color)
            default:
                Image(systemName: symbol)
                    .font(.system(size: size * 0.46, weight: .bold))
                    .foregroundStyle(.white)
                    .frame(width: size, height: size)
                    .background(state.color, in: Circle())
            }
        }
        .frame(width: size, height: size)
        .accessibilityLabel(StatusGroup(state).label)
    }

    private var symbol: String {
        switch state {
        case .needsYou: "questionmark"
        case .ready: "checkmark"
        case .failed: "exclamationmark"
        case .stopped: "pause.fill"
        default: "power"
        }
    }
}

/// Lines added and removed, in green and red.
struct ChangeCounts: View {
    let added: Int
    let removed: Int

    var body: some View {
        HStack(spacing: 4) {
            Text("+\(added)").foregroundStyle(.green)
            Text("−\(removed)").foregroundStyle(.red)
        }
        .monospacedDigit()
    }
}

/// A grouped-background card, for content that isn't a List.
struct Card<Content: View>: View {
    @ViewBuilder var content: Content

    var body: some View {
        VStack(alignment: .leading, spacing: 0) { content }
            .frame(maxWidth: .infinity, alignment: .leading)
            .background(Color(.secondarySystemGroupedBackground), in: RoundedRectangle(cornerRadius: 12, style: .continuous))
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

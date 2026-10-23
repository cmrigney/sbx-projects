import Foundation
import SwiftUI
import UIKit

struct ActivityTab: View {
    let sandbox: Sandbox
    let detail: SandboxDetail?

    var body: some View {
        if let detail, !detail.activity.isEmpty {
            Card {
                VStack(alignment: .leading, spacing: 0) {
                    ForEach(Array(detail.activity.enumerated()), id: \.offset) { _, item in
                        FeedRow(item: item)
                    }
                }
                .padding(.vertical, 6)
                // The log's thread, through the glyphs.
                .background(alignment: .leading) {
                    Line()
                        .stroke(Theme.line, style: StrokeStyle(lineWidth: 1, dash: [3, 3]))
                        .frame(width: 1)
                        .padding(.vertical, 16)
                        .padding(.leading, FeedRow.glyphCenter)
                }
            }
            if let at = detail.probedAt {
                Text("Last seen \(at.formatted(.relative(presentation: .named))), while the sandbox was running.")
                    .font(.footnote)
                    .foregroundStyle(Theme.muted)
            }
        } else if detail != nil {
            Text(sandbox.reportsActivity
                 ? "No activity yet. It appears once the agent starts working."
                 : "\(sandbox.agentLabel) doesn't report what it is doing.")
                .font(.subheadline)
                .foregroundStyle(Theme.muted)
                .frame(maxWidth: .infinity)
                .padding(.vertical, 24)
        }
    }
}

/// A vertical line down the middle of its frame.
private struct Line: Shape {
    func path(in rect: CGRect) -> Path {
        Path { p in
            p.move(to: CGPoint(x: rect.midX, y: rect.minY))
            p.addLine(to: CGPoint(x: rect.midX, y: rect.maxY))
        }
    }
}

/// One line of the activity log: when, a glyph for its kind, and what.
private struct FeedRow: View {
    let item: FeedItem

    /// Where the glyphs' centers sit, for the line through them.
    static let glyphCenter: CGFloat = 8 + 40 + 8 + 10

    var body: some View {
        HStack(alignment: .top, spacing: 8) {
            Text(shortAgo(item.at))
                .font(.caption.monospaced())
                .foregroundStyle(Theme.dim)
                .frame(width: 40, alignment: .trailing)
                .padding(.top, 2)
            glyph
            Group {
                if let path = item.path, !path.isEmpty {
                    Text("\(Text(item.text)) \(Text(path).font(.mono))")
                } else {
                    Text(item.text)
                }
            }
            .font(.subheadline)
            .foregroundStyle(color)
            .frame(maxWidth: .infinity, alignment: .leading)
        }
        .padding(.leading, 8)
        .padding(.trailing, 14)
        .padding(.vertical, 7)
    }

    private var glyph: some View {
        let shape = RoundedRectangle(cornerRadius: 5, style: .continuous)
        let tinted = item.kind != .plain
        return Group {
            switch item.kind {
            case .you: Image(systemName: "chevron.right").font(.system(size: 10, weight: .heavy))
            case .good: Image(systemName: "checkmark").font(.system(size: 10, weight: .heavy))
            case .warn: Image(systemName: "questionmark").font(.system(size: 10, weight: .heavy))
            case .bad: Image(systemName: "xmark").font(.system(size: 10, weight: .heavy))
            case .plain: Circle().frame(width: 4, height: 4)
            }
        }
        .foregroundStyle(tinted ? color : Theme.dim)
        .frame(width: 20, height: 20)
        .background(Theme.panel, in: shape)
        .background(tinted ? color.opacity(0.14) : .clear, in: shape)
        .overlay(shape.strokeBorder(tinted ? color.opacity(0.35) : Theme.line, lineWidth: 1))
        .accessibilityHidden(true)
    }

    private var color: Color {
        switch item.kind {
        case .you: Theme.work
        case .good: Theme.ready
        case .warn: Theme.needs
        case .bad: Theme.fail
        case .plain: Theme.text
        }
    }
}

struct ChangesTab: View {
    let sandbox: Sandbox
    let detail: SandboxDetail?
    let openDiff: (String?) -> Void

    @Environment(AppModel.self) private var app

    var body: some View {
        if let detail {
            if detail.changes.isEmpty {
                Text("No changes against \(detail.base) yet.")
                    .font(.subheadline)
                    .foregroundStyle(Theme.muted)
                    .frame(maxWidth: .infinity)
                    .padding(.vertical, 24)
            } else {
                summary(detail)
                Card {
                    ForEach(Array(detail.changes.enumerated()), id: \.offset) { i, f in
                        if i > 0 { RowDivider() }
                        Button { openDiff(f.path) } label: { FileRow(file: f) }
                            .buttonStyle(.plain)
                            .disabled(!sandbox.running && sandbox.clone)
                    }
                }
                if !sandbox.running && sandbox.clone {
                    Text("Resume the sandbox to see the full diff. These are the changes from when it last ran.")
                        .font(.footnote)
                        .foregroundStyle(Theme.muted)
                }
            }
            if !detail.commits.isEmpty {
                SectionLabel(text: "Commits").padding(.top, 4)
                Card {
                    ForEach(Array(detail.commits.enumerated()), id: \.offset) { i, c in
                        if i > 0 { RowDivider() }
                        HStack(alignment: .firstTextBaseline, spacing: 10) {
                            Text(c.hash).font(.caption.monospaced()).foregroundStyle(Theme.accent)
                            Text(c.subject).font(.subheadline).foregroundStyle(Theme.text).lineLimit(2)
                            Spacer(minLength: 4)
                            Text(shortAgo(c.at)).font(.caption.monospaced()).foregroundStyle(Theme.dim)
                        }
                        .padding(.horizontal, 14)
                        .padding(.vertical, 10)
                    }
                }
            }
            if sandbox.canPR || sandbox.canCheckout {
                HStack(spacing: 10) {
                    if sandbox.canPR {
                        Button { Task { await app.openPR(sandbox.name) } } label: {
                            Label("Open draft PR", systemImage: "arrow.triangle.pull").frame(maxWidth: .infinity)
                        }
                        .buttonStyle(PrimaryButtonStyle())
                    }
                    if sandbox.canCheckout {
                        Button { Task { await app.checkout(sandbox.name) } } label: {
                            Label("Check out on Mac", systemImage: "laptopcomputer").frame(maxWidth: .infinity)
                        }
                        .buttonStyle(SecondaryButtonStyle())
                    }
                }
                .labelStyle(.titleAndIcon)
                .padding(.top, 4)
            }
        }
    }

    private func summary(_ d: SandboxDetail) -> some View {
        HStack(alignment: .firstTextBaseline) {
            Text("\(d.changes.count) \(d.changes.count == 1 ? "FILE" : "FILES") AGAINST \(Text(d.base).foregroundStyle(Theme.text))")
                .font(.system(size: 11.5, weight: .medium, design: .monospaced))
                .tracking(0.7)
                .foregroundStyle(Theme.dim)
            Spacer()
            ChangeCounts(added: sandbox.added, removed: sandbox.removed).font(.subheadline.weight(.semibold))
        }
    }
}

private struct FileRow: View {
    let file: FileChange

    private var dir: String {
        guard let i = file.path.lastIndex(of: "/") else { return "" }
        return String(file.path[...i])
    }

    private var base: String {
        guard let i = file.path.lastIndex(of: "/") else { return file.path }
        return String(file.path[file.path.index(after: i)...])
    }

    var body: some View {
        HStack(alignment: .firstTextBaseline, spacing: 10) {
            Text("\(Text(dir).foregroundStyle(Theme.dim))\(Text(base).foregroundStyle(Theme.text))")
                .font(.mono)
                .lineLimit(1)
                .truncationMode(.head)
            Spacer(minLength: 8)
            if file.binary == true {
                Text("binary").font(.caption.monospaced()).foregroundStyle(Theme.dim)
            } else {
                ChangeCounts(added: file.added, removed: file.removed).font(.caption)
                DiffStat(added: file.added, removed: file.removed)
            }
        }
        .padding(.horizontal, 14)
        .padding(.vertical, 13)
        .contentShape(Rectangle())
    }
}

/// Five blocks split by added and removed lines, as git's --stat draws them.
private struct DiffStat: View {
    let added: Int
    let removed: Int

    var body: some View {
        let total = added + removed
        let adds = total == 0 ? 0 : Int((5 * Double(added) / Double(total)).rounded())
        HStack(spacing: 2) {
            ForEach(0..<5, id: \.self) { i in
                RoundedRectangle(cornerRadius: 1.5)
                    .fill(total == 0 ? Theme.line : i < adds ? Theme.add : Theme.del)
                    .frame(width: 7, height: 7)
            }
        }
        .accessibilityHidden(true)
    }
}

struct SetupTab: View {
    let sandbox: Sandbox
    let detail: SandboxDetail?

    var body: some View {
        Card {
            row("Agent", sandbox.agentLabel)
            if let model = sandbox.modelLabel { row("Model", model) }
            row("Workspace", sandbox.clone ? "Private clone" : "Your working tree")
            if let branch = sandbox.branch { row("Branch", branch, mono: true) }
            row("Kits", sandbox.kits.isEmpty ? "None" : sandbox.kits.joined(separator: "\n"), mono: !sandbox.kits.isEmpty)
            row("MCP servers", sandbox.mcps.isEmpty ? "None" : sandbox.mcps.joined(separator: ", "))
            if let cpus = sandbox.cpus, let mem = sandbox.memoryMiB, cpus > 0 {
                row("Resources", "\(cpus) CPUs, \(memoryLabel(mem))")
            }
            if let ws = detail?.workspace { row("Path", ws, mono: true) }
            row("Sandbox", sandbox.name, mono: true)
            row("Created", sandbox.createdAt.formatted(date: .abbreviated, time: .shortened), last: true)
        }
        Text("Kits and MCP servers are fixed when a sandbox is created.")
            .font(.footnote)
            .foregroundStyle(Theme.muted)
        PortsSection(sandbox: sandbox)
    }

    private func row(_ label: String, _ value: String, mono: Bool = false, last: Bool = false) -> some View {
        VStack(spacing: 0) {
            HStack(alignment: .firstTextBaseline) {
                Text(label).foregroundStyle(Theme.text)
                Spacer(minLength: 16)
                Text(value)
                    .font(mono ? .mono : .body)
                    .foregroundStyle(Theme.muted)
                    .multilineTextAlignment(.trailing)
                    .textSelection(.enabled)
            }
            .padding(.horizontal, 14)
            .padding(.vertical, 11)
            if !last { RowDivider() }
        }
    }
}

/// Ports published on the Mac with sbx ports, and a form to publish another.
private struct PortsSection: View {
    let sandbox: Sandbox

    @Environment(AppModel.self) private var app
    @State private var sandboxPort = ""
    @State private var hostPort = ""

    private func number(_ s: String) -> Int? {
        guard let n = Int(s.trimmingCharacters(in: .whitespaces)), (1...65535).contains(n) else { return nil }
        return n
    }

    private var canPublish: Bool {
        number(sandboxPort) != nil && (hostPort.trimmingCharacters(in: .whitespaces).isEmpty || number(hostPort) != nil)
            && !sandbox.busy
    }

    var body: some View {
        SectionLabel(text: "Ports").padding(.top, 8)
        Card {
            ForEach(sandbox.ports, id: \.self) { port in
                HStack {
                    Text(port.label).font(.mono).foregroundStyle(Theme.text).textSelection(.enabled)
                    Spacer(minLength: 16)
                    Button("Unpublish") { Task { await app.unpublishPort(port, on: sandbox.name) } }
                        .font(.subheadline)
                        .disabled(sandbox.busy)
                }
                .padding(.horizontal, 14)
                .padding(.vertical, 11)
                RowDivider()
            }
            field("Sandbox port", text: $sandboxPort, placeholder: "3000")
            RowDivider()
            field("Mac port", text: $hostPort, placeholder: sandboxPort.isEmpty ? "Same" : sandboxPort)
            RowDivider()
            Button(action: publish) { Text("Publish").frame(maxWidth: .infinity) }
                .buttonStyle(PrimaryButtonStyle())
                .disabled(!canPublish)
                .padding(14)
        }
        Text("Forwards a port on the Mac to the sandbox, as sbx ports --publish does.")
            .font(.footnote)
            .foregroundStyle(Theme.muted)
    }

    private func field(_ label: String, text: Binding<String>, placeholder: String) -> some View {
        HStack {
            Text(label).foregroundStyle(Theme.text)
            Spacer(minLength: 16)
            TextField(placeholder, text: text)
                .font(.body.monospaced())
                .keyboardType(.numberPad)
                .multilineTextAlignment(.trailing)
        }
        .padding(.horizontal, 14)
        .padding(.vertical, 11)
    }

    private func publish() {
        guard let port = number(sandboxPort) else { return }
        let r = PublishPortRequest(sandboxPort: port, hostPort: number(hostPort))
        Task {
            if await app.publishPort(r, on: sandbox.name) {
                sandboxPort = ""
                hostPort = ""
            }
        }
    }
}

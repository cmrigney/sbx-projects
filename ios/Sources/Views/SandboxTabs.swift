import SwiftUI

struct ActivityTab: View {
    let sandbox: Sandbox
    let detail: SandboxDetail?

    var body: some View {
        if let detail, !detail.activity.isEmpty {
            Card {
                ForEach(Array(detail.activity.enumerated()), id: \.offset) { i, item in
                    if i > 0 { Divider().padding(.leading, 60) }
                    FeedRow(item: item)
                }
            }
            if let at = detail.probedAt {
                Text("Last seen \(at.formatted(.relative(presentation: .named))), while the sandbox was running.")
                    .font(.footnote)
                    .foregroundStyle(.secondary)
            }
        } else if detail != nil {
            Text(sandbox.reportsActivity
                 ? "No activity yet. It appears once the agent starts working."
                 : "\(sandbox.agentLabel) doesn't report what it is doing.")
                .font(.subheadline)
                .foregroundStyle(.secondary)
                .frame(maxWidth: .infinity)
                .padding(.vertical, 24)
        }
    }
}

private struct FeedRow: View {
    let item: FeedItem

    var body: some View {
        HStack(alignment: .firstTextBaseline, spacing: 10) {
            Text(shortAgo(item.at))
                .font(.caption)
                .monospacedDigit()
                .foregroundStyle(.secondary)
                .frame(width: 38, alignment: .trailing)
            if let path = item.path, !path.isEmpty {
                Text("\(Text(item.text)) \(Text(path).font(.mono))")
                    .foregroundStyle(color)
            } else {
                Text(item.text).foregroundStyle(color)
            }
        }
        .font(.subheadline)
        .padding(.horizontal, 14)
        .padding(.vertical, 9)
        .frame(maxWidth: .infinity, alignment: .leading)
    }

    private var color: Color {
        switch item.kind {
        case .you: .blue
        case .good: .green
        case .warn: .orange
        case .bad: .red
        case .plain: .primary
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
                    .foregroundStyle(.secondary)
                    .frame(maxWidth: .infinity)
                    .padding(.vertical, 24)
            } else {
                summary(detail)
                Card {
                    ForEach(Array(detail.changes.enumerated()), id: \.offset) { i, f in
                        if i > 0 { Divider().padding(.leading, 14) }
                        Button { openDiff(f.path) } label: { FileRow(file: f) }
                            .buttonStyle(.plain)
                            .disabled(!sandbox.running && sandbox.clone)
                    }
                }
                if !sandbox.running && sandbox.clone {
                    Text("Resume the sandbox to see the full diff. These are the changes from when it last ran.")
                        .font(.footnote)
                        .foregroundStyle(.secondary)
                }
            }
            if !detail.commits.isEmpty {
                Text("Commits").font(.footnote).textCase(.uppercase).foregroundStyle(.secondary).padding(.leading, 16)
                Card {
                    ForEach(Array(detail.commits.enumerated()), id: \.offset) { i, c in
                        if i > 0 { Divider().padding(.leading, 14) }
                        HStack(alignment: .firstTextBaseline, spacing: 10) {
                            Text(c.hash).font(.caption.monospaced()).foregroundStyle(.secondary)
                            Text(c.subject).font(.subheadline).lineLimit(2)
                            Spacer(minLength: 4)
                            Text(shortAgo(c.at)).font(.caption).foregroundStyle(.secondary)
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
                            Text("Open draft PR").frame(maxWidth: .infinity)
                        }
                        .buttonStyle(.borderedProminent)
                    }
                    if sandbox.canCheckout {
                        Button { Task { await app.checkout(sandbox.name) } } label: {
                            Text("Check out on Mac").frame(maxWidth: .infinity)
                        }
                        .buttonStyle(.bordered)
                    }
                }
                .controlSize(.large)
            }
        }
    }

    private func summary(_ d: SandboxDetail) -> some View {
        HStack(alignment: .firstTextBaseline) {
            Text("\(d.changes.count) \(d.changes.count == 1 ? "file" : "files") against \(Text(d.base).font(.mono))")
            Spacer()
            ChangeCounts(added: sandbox.added, removed: sandbox.removed).fontWeight(.semibold)
        }
        .font(.subheadline)
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
        VStack(alignment: .leading, spacing: 6) {
            HStack(alignment: .firstTextBaseline) {
                Text("\(Text(dir).foregroundStyle(.secondary))\(Text(base))")
                    .font(.mono)
                    .lineLimit(1)
                    .truncationMode(.head)
                Spacer(minLength: 8)
                if file.binary == true {
                    Text("binary").font(.caption).foregroundStyle(.secondary)
                } else {
                    ChangeCounts(added: file.added, removed: file.removed).font(.caption)
                }
            }
            ChangeBar(added: file.added, removed: file.removed)
        }
        .padding(.horizontal, 14)
        .padding(.vertical, 10)
        .contentShape(Rectangle())
    }
}

/// A file's added and removed lines as one green and red bar.
private struct ChangeBar: View {
    let added: Int
    let removed: Int

    var body: some View {
        let total = max(added + removed, 1)
        GeometryReader { geo in
            HStack(spacing: 0) {
                Rectangle().fill(.green).frame(width: geo.size.width * CGFloat(added) / CGFloat(total))
                Rectangle().fill(.red)
            }
        }
        .frame(height: 4)
        .clipShape(Capsule())
        .opacity(added + removed == 0 ? 0.2 : 1)
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
            if !sandbox.ports.isEmpty {
                row("Ports", sandbox.ports.map { "\($0.hostPort) → \($0.sandboxPort)/\($0.proto)" }.joined(separator: "\n"), mono: true)
            }
            if let ws = detail?.workspace { row("Path", ws, mono: true) }
            row("Sandbox", sandbox.name, mono: true)
            row("Created", sandbox.createdAt.formatted(date: .abbreviated, time: .shortened), last: true)
        }
        Text("Kits and MCP servers are fixed when a sandbox is created.")
            .font(.footnote)
            .foregroundStyle(.secondary)
            .padding(.leading, 16)
    }

    private func row(_ label: String, _ value: String, mono: Bool = false, last: Bool = false) -> some View {
        VStack(spacing: 0) {
            HStack(alignment: .firstTextBaseline) {
                Text(label)
                Spacer(minLength: 16)
                Text(value)
                    .font(mono ? .mono : .body)
                    .foregroundStyle(.secondary)
                    .multilineTextAlignment(.trailing)
                    .textSelection(.enabled)
            }
            .padding(.horizontal, 14)
            .padding(.vertical, 11)
            if !last { Divider().padding(.leading, 14) }
        }
    }
}

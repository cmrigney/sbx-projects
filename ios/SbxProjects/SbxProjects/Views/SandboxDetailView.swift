import SwiftUI
import UIKit

struct SandboxDetailView: View {
    let name: String

    @Environment(AppModel.self) private var app
    @Environment(\.dismiss) private var dismiss
    @State private var detail: SandboxDetail?
    @State private var loadError: String?
    @State private var tab: Tab = .activity
    @State private var showReply = false
    @State private var diffPath: DiffTarget?
    @State private var confirmRemove = false
    @FocusState private var composing: Bool

    enum Tab: String, CaseIterable {
        case activity = "Activity", changes = "Changes", setup = "Setup"
    }

    /// The list's view is the freshest; the detail fills in the rest.
    private var sandbox: Sandbox? { app.sandboxes[name] ?? detail?.sandbox }

    var body: some View {
        Group {
            if let sandbox {
                content(sandbox)
            } else if let loadError {
                ContentUnavailableView("Couldn't load \(name)", systemImage: "exclamationmark.triangle", description: Text(loadError))
            } else {
                ProgressView()
            }
        }
        .navigationTitle(sandbox?.name ?? name)
        .navigationBarTitleDisplayMode(.inline)
        .background(Theme.bg)
        // Every change to the sandbox reloads its tabs.
        .task(id: app.sandboxes[name]) { await load() }
        .onChange(of: app.sandboxes[name] == nil) { _, gone in
            if gone && detail != nil { dismiss() }
        }
        .sheet(isPresented: $showReply) {
            if let sandbox { ReplySheet(sandbox: sandbox) }
        }
        .sheet(item: $diffPath) { DiffView(name: name, jumpTo: $0.path) }
    }

    private func load() async {
        do {
            detail = try await app.detail(name)
            loadError = nil
        } catch is CancellationError {
        } catch {
            loadError = error.localizedDescription
        }
    }

    private func content(_ s: Sandbox) -> some View {
        ScrollView {
            VStack(alignment: .leading, spacing: 16) {
                header(s)
                if s.state == .needsYou {
                    Callout(color: Theme.needs, icon: "apple.terminal", head: "\(s.agentLabel) is asking", at: s.activeAt,
                            text: s.statusText, link: "Answer below") { composing = true }
                } else if s.unread && s.hasReply {
                    Callout(color: Theme.accent, icon: "text.bubble", head: "New reply", at: s.activeAt,
                            text: s.statusText, link: "Read it") { showReply = true }
                }
                Picker("Tab", selection: $tab) {
                    ForEach(Tab.allCases, id: \.self) { Text($0.rawValue) }
                }
                .pickerStyle(.segmented)
                switch tab {
                case .activity: ActivityTab(sandbox: s, detail: detail)
                case .changes: ChangesTab(sandbox: s, detail: detail, openDiff: { diffPath = DiffTarget(path: $0) })
                case .setup: SetupTab(sandbox: s, detail: detail)
                }
            }
            .padding(.horizontal)
            .padding(.bottom, 24)
        }
        .scrollDismissesKeyboard(.interactively)
        .safeAreaInset(edge: .bottom) { Composer(sandbox: s, focused: $composing) }
        .toolbar { ToolbarItem(placement: .topBarTrailing) { actions(s) } }
        .confirmationDialog("Remove \(s.name)?", isPresented: $confirmRemove, titleVisibility: .visible) {
            Button("Remove", role: .destructive) { Task { await app.remove(s.name) } }
        } message: {
            Text(removeMessage(s))
        }
    }

    private func header(_ s: Sandbox) -> some View {
        VStack(alignment: .leading, spacing: 8) {
            HStack(spacing: 10) {
                StatusTag(state: s.state)
                if let at = s.activeAt {
                    Text(shortAgo(at)).font(.caption.monospaced()).foregroundStyle(Theme.dim)
                }
            }
            Text(s.title)
                .font(.title2.bold())
                .foregroundStyle(Theme.text)
            if let branch = s.branch {
                BranchLabel(branch: branch).font(.mono).foregroundStyle(Theme.muted)
            }
            // The callout below says it in full.
            if s.state != .needsYou && !(s.unread && s.hasReply) {
                Text(s.statusText)
                    .font(.subheadline)
                    .foregroundStyle(s.state == .failed ? Theme.fail : Theme.muted)
                    .lineLimit(2)
            }
            FlowTags(tags: tags(s))
        }
        .padding(.top, 8)
    }

    private func tags(_ s: Sandbox) -> [String] {
        var t = [s.modelLabel.map { "\(s.agentLabel) · \($0)" } ?? s.agentLabel]
        if let cpus = s.cpus, let mem = s.memoryMiB, cpus > 0 {
            t.append("\(cpus) cpu · \(memoryLabel(mem))")
        }
        t.append(s.clone ? "private clone" : "your working tree")
        return t
    }

    private func actions(_ s: Sandbox) -> some View {
        Menu {
            Button { showReply = true } label: { Label("Last reply", systemImage: "text.bubble") }
                .disabled(!s.hasReply)
            Button { Task { await app.openPR(s.name) } } label: { Label("Open draft PR", systemImage: "arrow.triangle.pull") }
                .disabled(!s.canPR)
            Button { Task { await app.checkout(s.name) } } label: { Label("Check out on Mac", systemImage: "laptopcomputer.and.arrow.down") }
                .disabled(!s.canCheckout)
            Divider()
            if s.running {
                Button { Task { await app.stop(s.name) } } label: { Label("Stop", systemImage: "stop.circle") }
            } else {
                Button { Task { await app.resume(s.name) } } label: { Label("Resume", systemImage: "play.circle") }
            }
            Button(role: .destructive) { confirmRemove = true } label: { Label("Remove…", systemImage: "trash") }
        } label: {
            Image(systemName: "ellipsis.circle")
        }
        .disabled(s.busy)
    }

    private func removeMessage(_ s: Sandbox) -> String {
        var m = s.clone
            ? "This deletes the sandbox, its private clone and agent history. Commits that were not pushed are lost."
            : "This deletes the sandbox and its agent history."
        if s.running { m += " The sandbox is stopped first." }
        return m
    }
}

struct DiffTarget: Identifiable {
    var id: String { path ?? "" }
    let path: String?
}

/// The agent's question, or its unread reply, above the tabs.
private struct Callout: View {
    let color: Color
    let icon: String
    let head: String
    let at: Date?
    let text: String
    let link: String
    let action: () -> Void

    var body: some View {
        Button(action: action) {
            VStack(alignment: .leading, spacing: 8) {
                HStack {
                    Label(head.uppercased(), systemImage: icon)
                        .font(.system(size: 11.5, weight: .semibold, design: .monospaced))
                        .tracking(0.6)
                        .foregroundStyle(color)
                    Spacer()
                    if let at {
                        Text("\(shortAgo(at)) ago").font(.caption.monospaced()).foregroundStyle(Theme.dim)
                    }
                }
                Text(text)
                    .font(.body.weight(.medium))
                    .foregroundStyle(Theme.text)
                    .lineLimit(3)
                    .multilineTextAlignment(.leading)
                Text(link)
                    .font(.subheadline.weight(.semibold))
                    .foregroundStyle(color)
                    .padding(.top, 2)
            }
            .padding(.leading, 17)
            .padding([.trailing, .vertical], 14)
            .frame(maxWidth: .infinity, alignment: .leading)
            .background(RailBackground(rail: color, tint: 0.09))
        }
        .buttonStyle(.plain)
    }
}

/// Small mono tags that wrap onto more lines when they don't fit.
private struct FlowTags: View {
    let tags: [String]

    var body: some View {
        ViewThatFits(in: .horizontal) {
            HStack(spacing: 6) { ForEach(tags, id: \.self) { tag($0) } }
            VStack(alignment: .leading, spacing: 6) { ForEach(tags, id: \.self) { tag($0) } }
        }
    }

    private func tag(_ t: String) -> some View {
        Text(t)
            .font(.system(size: 11.5, weight: .medium, design: .monospaced))
            .foregroundStyle(Theme.muted)
            .padding(.horizontal, 8)
            .padding(.vertical, 5)
            .background(Theme.panel, in: RoundedRectangle(cornerRadius: 6, style: .continuous))
            .overlay(RoundedRectangle(cornerRadius: 6, style: .continuous).strokeBorder(Theme.line, lineWidth: 1))
    }
}

/// Types a message into the agent's session. A stopped sandbox is started,
/// and an agent that isn't running is started with the message.
struct Composer: View {
    let sandbox: Sandbox
    var placeholder: String?
    var focused: FocusState<Bool>.Binding
    var onSent: () -> Void = {}

    @Environment(AppModel.self) private var app
    @State private var text = ""
    @State private var sending = false

    private var trimmed: String { text.trimmingCharacters(in: .whitespacesAndNewlines) }
    private var canSend: Bool { !trimmed.isEmpty && !sending && !sandbox.busy }

    var body: some View {
        let shape = RoundedRectangle(cornerRadius: 14, style: .continuous)
        HStack(alignment: .bottom, spacing: 10) {
            Text(">_")
                .font(.callout.monospaced().weight(.bold))
                .foregroundStyle(Theme.accent)
                .padding(.bottom, 8)
                .accessibilityHidden(true)
            TextField(placeholder ?? "Message \(sandbox.agentLabel)", text: $text, axis: .vertical)
                .lineLimit(1...6)
                .focused(focused)
                .padding(.vertical, 8)
            Button(action: send) {
                Group {
                    if sending {
                        ProgressView().controlSize(.small).tint(Theme.accentInk)
                    } else {
                        Image(systemName: "arrow.up").font(.body.weight(.bold))
                    }
                }
                .frame(width: 34, height: 34)
                .foregroundStyle(canSend || sending ? Theme.accentInk : Theme.dim)
                .background(canSend || sending ? Theme.accentFill : Theme.panel2, in: RoundedRectangle(cornerRadius: 9, style: .continuous))
            }
            .disabled(!canSend)
            .accessibilityLabel("Send")
        }
        .padding(.leading, 14)
        .padding(.trailing, 5)
        .padding(.vertical, 5)
        .background(Theme.panel, in: shape)
        .overlay(shape.strokeBorder(focused.wrappedValue ? Theme.accent.opacity(0.7) : Theme.line, lineWidth: 1))
        .padding(.horizontal)
        .padding(.vertical, 8)
        .background(.bar)
    }

    private func send() {
        let message = trimmed
        sending = true
        Task {
            if await app.send(message, to: sandbox.name) {
                text = ""
                onSent()
            }
            sending = false
        }
    }
}

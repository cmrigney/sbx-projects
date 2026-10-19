import SwiftUI

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
        .background(Color(.systemGroupedBackground))
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
                    AskCard(sandbox: s)
                } else if s.unread && s.hasReply {
                    Button { showReply = true } label: { NewReplyCard(sandbox: s) }.buttonStyle(.plain)
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
        .safeAreaInset(edge: .bottom) { Composer(sandbox: s) }
        .toolbar { ToolbarItem(placement: .topBarTrailing) { actions(s) } }
        .confirmationDialog("Remove \(s.name)?", isPresented: $confirmRemove, titleVisibility: .visible) {
            Button("Remove", role: .destructive) { Task { await app.remove(s.name) } }
        } message: {
            Text(removeMessage(s))
        }
    }

    private func header(_ s: Sandbox) -> some View {
        VStack(alignment: .leading, spacing: 8) {
            Text(s.title)
                .font(.title2.bold())
            if let branch = s.branch {
                Text(branch).font(.mono).foregroundStyle(.secondary)
            }
            HStack(spacing: 8) {
                StatusBadge(state: s.state, size: 18)
                Text(s.statusText)
                    .font(.subheadline)
                    .foregroundStyle(s.state == .failed ? .red : .secondary)
                    .lineLimit(2)
            }
            FlowTags(tags: tags(s))
        }
        .padding(.top, 8)
    }

    private func tags(_ s: Sandbox) -> [String] {
        var t = [s.modelLabel.map { "\(s.agentLabel) · \($0)" } ?? s.agentLabel]
        if let cpus = s.cpus, let mem = s.memoryMiB, cpus > 0 {
            t.append("\(cpus) CPU · \(memoryLabel(mem))")
        }
        t.append(s.clone ? "Private clone" : "Your working tree")
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

/// The agent's question, when it is waiting on one.
private struct AskCard: View {
    let sandbox: Sandbox

    var body: some View {
        VStack(alignment: .leading, spacing: 6) {
            HStack {
                Text("\(sandbox.agentLabel) is asking")
                    .font(.caption.weight(.semibold))
                    .textCase(.uppercase)
                    .foregroundStyle(.orange)
                Spacer()
                if let at = sandbox.activeAt {
                    Text(shortAgo(at)).font(.caption).foregroundStyle(.secondary)
                }
            }
            Text(sandbox.statusText)
                .font(.body.weight(.medium))
        }
        .padding(14)
        .frame(maxWidth: .infinity, alignment: .leading)
        .background(Color.orange.opacity(0.14), in: RoundedRectangle(cornerRadius: 14, style: .continuous))
    }
}

private struct NewReplyCard: View {
    let sandbox: Sandbox

    var body: some View {
        HStack(alignment: .top, spacing: 10) {
            Circle().fill(.blue).frame(width: 9, height: 9).padding(.top, 6)
            VStack(alignment: .leading, spacing: 4) {
                Text("New reply").font(.caption.weight(.semibold)).textCase(.uppercase).foregroundStyle(.blue)
                Text(sandbox.statusText).font(.body).lineLimit(3).multilineTextAlignment(.leading)
                Text("Read it").font(.subheadline.weight(.semibold)).foregroundStyle(.blue)
            }
            Spacer(minLength: 0)
        }
        .padding(14)
        .background(Color.blue.opacity(0.1), in: RoundedRectangle(cornerRadius: 14, style: .continuous))
    }
}

/// Small grey tags that wrap onto more lines when they don't fit.
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
            .font(.caption.weight(.medium))
            .foregroundStyle(.secondary)
            .padding(.horizontal, 8)
            .padding(.vertical, 3)
            .background(Color(.tertiarySystemFill), in: RoundedRectangle(cornerRadius: 6, style: .continuous))
    }
}

/// Types a message into the agent's session. A stopped sandbox is started,
/// and an agent that isn't running is started with the message.
struct Composer: View {
    let sandbox: Sandbox
    var placeholder: String?
    var onSent: () -> Void = {}

    @Environment(AppModel.self) private var app
    @State private var text = ""
    @State private var sending = false

    private var trimmed: String { text.trimmingCharacters(in: .whitespacesAndNewlines) }

    var body: some View {
        HStack(alignment: .bottom, spacing: 8) {
            TextField(placeholder ?? "Message \(sandbox.agentLabel)", text: $text, axis: .vertical)
                .lineLimit(1...6)
                .padding(.leading, 14)
                .padding(.trailing, 6)
                .padding(.vertical, 8)
                .background(Color(.secondarySystemGroupedBackground), in: RoundedRectangle(cornerRadius: 18, style: .continuous))
                .overlay(RoundedRectangle(cornerRadius: 18, style: .continuous).stroke(Color(.separator), lineWidth: 0.5))
            Button(action: send) {
                Image(systemName: sending ? "ellipsis.circle.fill" : "arrow.up.circle.fill")
                    .font(.system(size: 32))
                    .symbolRenderingMode(.hierarchical)
            }
            .disabled(trimmed.isEmpty || sending || sandbox.busy)
            .accessibilityLabel("Send")
        }
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

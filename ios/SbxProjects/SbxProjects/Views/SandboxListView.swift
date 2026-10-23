import SwiftUI
import UIKit

struct SandboxListView: View {
    @Environment(AppModel.self) private var app
    /// Nil shows every sandbox but the parked ones, which have their own chip.
    @State private var filter: StatusGroup?
    @State private var query = ""
    @State private var showNew = false
    @State private var addingMac = false
    @State private var path: [String] = []

    private var visibleCount: Int {
        app.projects.reduce(0) { $0 + app.sandboxes(in: $1, group: filter, matching: query).count }
    }

    var body: some View {
        NavigationStack(path: $path) {
            List {
                ConnectionBanner { addingMac = true }
                FilterChips(filter: $filter)
                    .listRowInsets(EdgeInsets(top: 4, leading: 0, bottom: 8, trailing: 0))
                    .listRowBackground(Color.clear)
                    .listRowSeparator(.hidden)

                ForEach(app.projects) { project in
                    let rows = app.sandboxes(in: project, group: filter, matching: query)
                    if !rows.isEmpty {
                        Section {
                            ForEach(rows) { s in
                                // The card is the link, without a chevron, as on the web.
                                SandboxRow(sandbox: s)
                                    .background { NavigationLink(value: s.name) { EmptyView() }.opacity(0) }
                                    .listRowInsets(EdgeInsets(top: 16, leading: 32, bottom: 16, trailing: 30))
                                    .listRowBackground(RailBackground(rail: s.state.color).padding(.horizontal, 16).padding(.vertical, 4))
                                    .listRowSeparator(.hidden)
                            }
                        } header: {
                            ProjectHeader(project: project)
                        }
                    }
                }
            }
            .listStyle(.plain)
            .themedList()
            .animation(.default, value: filter)
            .navigationTitle("Sandboxes")
            .searchable(text: $query, prompt: "Title, branch or name")
            .refreshable { await app.refresh() }
            .navigationDestination(for: String.self) { SandboxDetailView(name: $0) }
            .toolbar {
                ToolbarItem(placement: .topBarLeading) { HostPill { addingMac = true } }
                ToolbarItem(placement: .topBarTrailing) {
                    Button { showNew = true } label: { Image(systemName: "plus") }
                        .accessibilityLabel("New sandbox")
                        .disabled(app.projects.isEmpty)
                }
            }
            .sheet(isPresented: $showNew) {
                NewSandboxView { path.append($0.name) }
            }
            .sheet(isPresented: $addingMac) { PairingView(adding: true) }
            // The open sandbox belongs to the Mac shown before.
            .onChange(of: app.server?.id) {
                path = []
                showNew = false
            }
            .overlay { EmptyList(visible: visibleCount, filter: filter, query: query) }
        }
    }
}

private struct EmptyList: View {
    @Environment(AppModel.self) private var app
    /// How many rows the filter and search leave.
    let visible: Int
    let filter: StatusGroup?
    let query: String

    var body: some View {
        if app.connection != .live {
            EmptyView()
        } else if app.projects.isEmpty {
            ContentUnavailableView("No projects yet", systemImage: "folder",
                                   description: Text("Run sbx-projects in a git repository on your Mac to add it as a project."))
        } else if app.sandboxes.isEmpty {
            ContentUnavailableView("No sandboxes", systemImage: "terminal",
                                   description: Text("Tap + to start an agent on a new branch."))
        } else if visible == 0 && filter == nil && query.isEmpty {
            ContentUnavailableView("Nothing active", systemImage: "moon.zzz",
                                   description: Text("Every sandbox is parked. Tap Parked to see them."))
        } else if visible == 0 {
            ContentUnavailableView("Nothing matches", systemImage: "magnifyingglass",
                                   description: Text("Try another filter or search."))
        }
    }
}

/// Which Mac this is, and whether it's connected. Tapping it switches to
/// another paired Mac or pairs a new one.
private struct HostPill: View {
    let addMac: () -> Void

    @Environment(AppModel.self) private var app

    var body: some View {
        Menu {
            Picker("Mac", selection: Binding(get: { app.server?.id ?? "" }, set: { app.select($0) })) {
                ForEach(app.servers) { s in
                    Label(s.name, systemImage: "laptopcomputer").tag(s.id)
                }
            }
            Button(action: addMac) { Label("Pair Another Mac", systemImage: "plus") }
        } label: {
            HStack(spacing: 6) {
                RoundedRectangle(cornerRadius: 2.5)
                    .fill(color)
                    .frame(width: 8, height: 8)
                Text(app.server?.name ?? "")
                    .font(.subheadline.weight(.medium).monospaced())
                    .foregroundStyle(Theme.text)
            }
            .padding(.horizontal, 6)
            // Toolbar items are sized to fit only what they must show, which
            // truncates the name to an ellipsis.
            .fixedSize()
        }
        .accessibilityLabel("Mac: \(app.server?.name ?? "")")
    }

    private var color: Color {
        switch app.connection {
        case .live: Theme.ready
        case .connecting: Theme.needs
        case .offline, .signedOut: Theme.fail
        }
    }
}

/// Says why the list may be stale, and what to do about it.
struct ConnectionBanner: View {
    /// Opens pairing, to pair again with a Mac that signed this iPhone out.
    let pairAgain: () -> Void

    @Environment(AppModel.self) private var app

    var body: some View {
        switch app.connection {
        case .offline(let message):
            banner("Can't reach \(app.server?.name ?? "the Mac")",
                   "Check that sbx-projects api is running and you're on the same network. \(message)",
                   icon: "wifi.exclamationmark", color: Theme.needs)
        case .signedOut(let message):
            banner("Signed out", message, icon: "lock.slash", color: Theme.fail, action: ("Pair again", pairAgain))
        default:
            if let error = app.listError {
                banner("Can't list sandboxes on \(app.server?.name ?? "the Mac")",
                       "The states below may be out of date. \(error)",
                       icon: "exclamationmark.triangle", color: Theme.needs)
            }
        }
    }

    private func banner(_ title: String, _ message: String, icon: String, color: Color,
                        action: (String, () -> Void)? = nil) -> some View {
        HStack(alignment: .top, spacing: 12) {
            Image(systemName: icon).foregroundStyle(color).frame(width: 22)
            VStack(alignment: .leading, spacing: 3) {
                Text(title).font(.subheadline.weight(.semibold)).foregroundStyle(Theme.text)
                Text(message).font(.footnote).foregroundStyle(Theme.muted)
                if let action {
                    Button(action.0, action: action.1)
                        .font(.subheadline.weight(.semibold))
                        .foregroundStyle(color)
                        .padding(.top, 4)
                }
            }
        }
        .listRowInsets(EdgeInsets(top: 16, leading: 32, bottom: 16, trailing: 28))
        .listRowBackground(RailBackground(rail: color).padding(.horizontal, 16).padding(.vertical, 4))
        .listRowSeparator(.hidden)
    }
}

/// A project's name over its sandboxes, with where it's pushed.
private struct ProjectHeader: View {
    let project: Project

    var body: some View {
        HStack(alignment: .firstTextBaseline, spacing: 8) {
            Image(systemName: "terminal.fill")
                .font(.caption)
                .foregroundStyle(Theme.accent)
            Text(project.name)
                .font(.subheadline.weight(.semibold))
                .foregroundStyle(Theme.text)
            Spacer(minLength: 8)
            if let remote = project.remote {
                Text(remote.replacingOccurrences(of: "github.com/", with: ""))
                    .font(.caption.monospaced())
                    .foregroundStyle(Theme.dim)
                    .lineLimit(1)
                    .truncationMode(.head)
            }
        }
        .textCase(nil)
        .padding(.top, 6)
    }
}

private struct FilterChips: View {
    @Environment(AppModel.self) private var app
    @Binding var filter: StatusGroup?

    var body: some View {
        ScrollView(.horizontal, showsIndicators: false) {
            HStack(spacing: 8) {
                chip("Active", nil)
                ForEach(StatusGroup.allCases, id: \.self) { chip($0.label, $0) }
            }
            .padding(.horizontal, 16)
            .padding(.vertical, 4)
        }
    }

    private func chip(_ label: String, _ group: StatusGroup?) -> some View {
        let on = filter == group
        let shape = Capsule()
        return Button {
            filter = group
        } label: {
            HStack(spacing: 6) {
                Text(label).fontWeight(.medium)
                Text("\(app.count(group))")
                    .font(.caption.monospaced())
                    .foregroundStyle(on ? Theme.accent : Theme.dim)
            }
            .font(.subheadline)
            .padding(.horizontal, 13)
            .padding(.vertical, 7)
            .foregroundStyle(on ? Theme.text : Theme.muted)
            .background(on ? Theme.accent.opacity(0.14) : Theme.panel, in: shape)
            .overlay(shape.strokeBorder(on ? Theme.accent.opacity(0.55) : Theme.line, lineWidth: 1))
        }
        .buttonStyle(.plain)
        .accessibilityAddTraits(on ? .isSelected : [])
    }
}

struct SandboxRow: View {
    let sandbox: Sandbox

    var body: some View {
        HStack(alignment: .top, spacing: 12) {
            StatusBadge(state: sandbox.state)
            VStack(alignment: .leading, spacing: 3) {
                HStack(alignment: .firstTextBaseline, spacing: 8) {
                    Text(sandbox.title)
                        .fontWeight(sandbox.unread ? .bold : .medium)
                        .foregroundStyle(Theme.text)
                        .lineLimit(1)
                    Spacer(minLength: 0)
                    if let at = sandbox.activeAt {
                        Text(shortAgo(at)).font(.caption.monospaced()).foregroundStyle(Theme.dim)
                    }
                }
                HStack(spacing: 6) {
                    if sandbox.unread && sandbox.state == .ready {
                        NewTag().accessibilityLabel("New reply:")
                    }
                    Text(sandbox.statusText)
                        .foregroundStyle(statusColor)
                        .lineLimit(1)
                }
                .font(.subheadline)
                HStack(spacing: 10) {
                    if let branch = sandbox.branch {
                        BranchLabel(branch: branch)
                    }
                    if sandbox.files > 0 {
                        ChangeCounts(added: sandbox.added, removed: sandbox.removed).layoutPriority(1)
                    }
                }
                .font(.caption.monospaced())
                .foregroundStyle(Theme.dim)
                .padding(.top, 2)
            }
        }
    }

    private var statusColor: Color {
        switch sandbox.state {
        case .needsYou, .failed: sandbox.state.color
        default: Theme.muted
        }
    }
}

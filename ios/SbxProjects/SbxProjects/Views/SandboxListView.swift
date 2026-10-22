import SwiftUI
import UIKit

struct SandboxListView: View {
    @Environment(AppModel.self) private var app
    @State private var filter: StatusGroup?
    @State private var query = ""
    @State private var showNew = false
    @State private var path: [String] = []

    private var visibleCount: Int {
        app.projects.reduce(0) { $0 + app.sandboxes(in: $1, group: filter, matching: query).count }
    }

    var body: some View {
        NavigationStack(path: $path) {
            List {
                ConnectionBanner()
                Section {
                    FilterChips(filter: $filter)
                }
                .listRowInsets(EdgeInsets())
                .listRowBackground(Color.clear)

                ForEach(app.projects) { project in
                    let rows = app.sandboxes(in: project, group: filter, matching: query)
                    if !rows.isEmpty {
                        Section {
                            ForEach(rows) { s in
                                NavigationLink(value: s.name) { SandboxRow(sandbox: s) }
                            }
                        } header: {
                            HStack {
                                Text(project.name)
                                Spacer()
                                if let remote = project.remote {
                                    Text(remote.replacingOccurrences(of: "github.com/", with: ""))
                                }
                            }
                        }
                    }
                }
            }
            .listStyle(.insetGrouped)
            .animation(.default, value: filter)
            .navigationTitle("Sandboxes")
            .searchable(text: $query, prompt: "Title, branch or name")
            .refreshable { await app.refresh() }
            .navigationDestination(for: String.self) { SandboxDetailView(name: $0) }
            .toolbar {
                ToolbarItem(placement: .topBarLeading) { HostPill() }
                ToolbarItem(placement: .topBarTrailing) {
                    Button { showNew = true } label: { Image(systemName: "plus") }
                        .accessibilityLabel("New sandbox")
                        .disabled(app.projects.isEmpty)
                }
            }
            .sheet(isPresented: $showNew) {
                NewSandboxView { path.append($0.name) }
            }
            .overlay { EmptyList(visible: visibleCount) }
        }
    }
}

private struct EmptyList: View {
    @Environment(AppModel.self) private var app
    /// How many rows the filter and search leave.
    let visible: Int

    var body: some View {
        if app.connection != .live {
            EmptyView()
        } else if app.projects.isEmpty {
            ContentUnavailableView("No projects yet", systemImage: "folder",
                                   description: Text("Run sbx-projects in a git repository on your Mac to add it as a project."))
        } else if app.sandboxes.isEmpty {
            ContentUnavailableView("No sandboxes", systemImage: "terminal",
                                   description: Text("Tap + to start an agent on a new branch."))
        } else if visible == 0 {
            ContentUnavailableView("Nothing matches", systemImage: "magnifyingglass",
                                   description: Text("Try another filter or search."))
        }
    }
}

/// Which Mac this is, and whether it's connected.
private struct HostPill: View {
    @Environment(AppModel.self) private var app

    var body: some View {
        HStack(spacing: 6) {
            Circle()
                .fill(color)
                .frame(width: 8, height: 8)
            Text(app.server?.name ?? "")
                .font(.subheadline)
                .foregroundStyle(.secondary)
        }
        .padding(.horizontal, 6)
        // Toolbar items are sized to fit only what they must show, which
        // truncates the name to an ellipsis.
        .fixedSize()
        .accessibilityElement(children: .combine)
    }

    private var color: Color {
        switch app.connection {
        case .live: .green
        case .connecting: .yellow
        case .offline, .signedOut: .red
        }
    }
}

/// Says why the list may be stale, and what to do about it.
struct ConnectionBanner: View {
    @Environment(AppModel.self) private var app

    var body: some View {
        switch app.connection {
        case .offline(let message):
            Section {
                Label {
                    VStack(alignment: .leading, spacing: 2) {
                        Text("Can't reach \(app.server?.name ?? "the Mac")").font(.subheadline.weight(.semibold))
                        Text("Check that sbx-projects api is running and you're on the same network. \(message)")
                            .font(.footnote)
                            .foregroundStyle(.secondary)
                    }
                } icon: {
                    Image(systemName: "wifi.exclamationmark").foregroundStyle(.orange)
                }
            }
        case .signedOut(let message):
            Section {
                Label {
                    VStack(alignment: .leading, spacing: 6) {
                        Text("Signed out").font(.subheadline.weight(.semibold))
                        Text(message).font(.footnote).foregroundStyle(.secondary)
                        Button("Pair again") { app.forget() }.font(.subheadline.weight(.semibold))
                    }
                } icon: {
                    Image(systemName: "lock.slash").foregroundStyle(.red)
                }
            }
        default:
            if let error = app.listError {
                Section {
                    Label {
                        VStack(alignment: .leading, spacing: 2) {
                            Text("Can't list sandboxes on \(app.server?.name ?? "the Mac")").font(.subheadline.weight(.semibold))
                            Text("The states below may be out of date. \(error)")
                                .font(.footnote)
                                .foregroundStyle(.secondary)
                        }
                    } icon: {
                        Image(systemName: "exclamationmark.triangle").foregroundStyle(.orange)
                    }
                }
            }
        }
    }
}

private struct FilterChips: View {
    @Environment(AppModel.self) private var app
    @Binding var filter: StatusGroup?

    var body: some View {
        ScrollView(.horizontal, showsIndicators: false) {
            HStack(spacing: 8) {
                chip("All", nil)
                ForEach(StatusGroup.allCases, id: \.self) { chip($0.label, $0) }
            }
            .padding(.vertical, 4)
        }
    }

    private func chip(_ label: String, _ group: StatusGroup?) -> some View {
        let on = filter == group
        return Button {
            filter = group
        } label: {
            HStack(spacing: 5) {
                Text(label).fontWeight(.medium)
                Text("\(app.count(group))").foregroundStyle(on ? Color(.systemBackground).opacity(0.7) : .secondary)
            }
            .font(.subheadline)
            .padding(.horizontal, 12)
            .padding(.vertical, 6)
            .background(on ? Color.primary : Color(.secondarySystemGroupedBackground), in: Capsule())
            .foregroundStyle(on ? Color(.systemBackground) : .primary)
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
                .overlay(alignment: .leading) {
                    if sandbox.unread {
                        Circle()
                            .fill(.blue)
                            .frame(width: 9, height: 9)
                            .offset(x: -15)
                            .accessibilityLabel("New reply")
                    }
                }
            VStack(alignment: .leading, spacing: 2) {
                Text(sandbox.title)
                    .fontWeight(sandbox.unread ? .semibold : .regular)
                    .lineLimit(1)
                Text(sandbox.unread && sandbox.state == .ready ? "New reply: \(sandbox.statusText)" : sandbox.statusText)
                    .font(.subheadline)
                    .foregroundStyle(statusColor)
                    .lineLimit(1)
                HStack(spacing: 8) {
                    if let branch = sandbox.branch {
                        Text(branch).font(.caption.monospaced()).lineLimit(1).truncationMode(.middle)
                    }
                    if sandbox.files > 0 {
                        ChangeCounts(added: sandbox.added, removed: sandbox.removed).font(.caption)
                    }
                    if let at = sandbox.activeAt {
                        Text(shortAgo(at)).font(.caption)
                    }
                }
                .foregroundStyle(.secondary)
            }
        }
        .padding(.vertical, 2)
    }

    private var statusColor: Color {
        switch sandbox.state {
        case .needsYou: .orange
        case .failed: .red
        default: .secondary
        }
    }
}

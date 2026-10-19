import SwiftUI

/// The full diff behind the Changes tab, scrolled to one file.
struct DiffView: View {
    let name: String
    let jumpTo: String?

    @Environment(AppModel.self) private var app
    @Environment(\.dismiss) private var dismiss
    @State private var diff: Diff?
    @State private var error: String?
    @State private var width: CGFloat = 0

    var body: some View {
        NavigationStack {
            Group {
                if let diff {
                    files(diff)
                } else if let error {
                    ContentUnavailableView("Couldn't read the diff", systemImage: "exclamationmark.triangle", description: Text(error))
                } else {
                    ProgressView("Reading the diff")
                }
            }
            .navigationTitle("Changes")
            .navigationBarTitleDisplayMode(.inline)
            .toolbar {
                ToolbarItem(placement: .topBarTrailing) { Button("Done") { dismiss() }.fontWeight(.semibold) }
            }
        }
        .task {
            do {
                diff = try await app.diff(name)
            } catch {
                self.error = error.localizedDescription
            }
        }
    }

    private func files(_ diff: Diff) -> some View {
        ScrollViewReader { proxy in
            ScrollView {
                LazyVStack(alignment: .leading, spacing: 0, pinnedViews: [.sectionHeaders]) {
                    if diff.truncated == true {
                        Text("This diff is too long to show in full. Files past the limit show only their names.")
                            .font(.footnote)
                            .foregroundStyle(.secondary)
                            .padding()
                    }
                    ForEach(diff.files) { file in
                        Section {
                            FileHunks(file: file, minWidth: width)
                        } header: {
                            header(file)
                        }
                        .id(file.path)
                    }
                }
            }
            .onGeometryChange(for: CGFloat.self) { $0.size.width } action: { width = $0 }
            .onAppear {
                if let jumpTo { proxy.scrollTo(jumpTo, anchor: .top) }
            }
        }
    }

    private func header(_ file: Diff.File) -> some View {
        HStack {
            Text(file.path).font(.mono.weight(.semibold)).lineLimit(1).truncationMode(.head)
            Spacer()
            if let note = file.note {
                Text(note).font(.caption).foregroundStyle(.secondary)
            }
        }
        .padding(.horizontal)
        .padding(.vertical, 8)
        .background(.bar)
    }
}

/// One file's hunks. Long lines scroll sideways rather than wrap, so the
/// columns of the code stay lined up.
private struct FileHunks: View {
    let file: Diff.File
    /// The screen's width, so short files' rows still reach the edge.
    let minWidth: CGFloat

    var body: some View {
        if file.lines.isEmpty {
            Text(file.note == nil ? "No lines to show" : "Nothing to show for a \(file.note!) file")
                .font(.footnote)
                .foregroundStyle(.secondary)
                .padding()
        } else {
            ScrollView(.horizontal, showsIndicators: false) {
                VStack(alignment: .leading, spacing: 0) {
                    // Holds the rows to at least the screen's width.
                    Color.clear.frame(width: minWidth, height: 0)
                    ForEach(Array(file.lines.enumerated()), id: \.offset) { _, line in
                        Text(line.isEmpty ? " " : line)
                            .font(.system(size: 12, design: .monospaced))
                            .foregroundStyle(line.hasPrefix("@@") ? Color.secondary : Color.primary)
                            .lineLimit(1)
                            .padding(.horizontal, 12)
                            .frame(maxWidth: .infinity, alignment: .leading)
                            .background(background(line))
                    }
                }
                // As wide as the longest line, so every row's colour spans it.
                .fixedSize(horizontal: true, vertical: false)
                .padding(.vertical, 4)
            }
        }
    }

    private func background(_ line: String) -> Color {
        if line.hasPrefix("@@") { return Color(.tertiarySystemFill) }
        if line.hasPrefix("+") { return .green.opacity(0.15) }
        if line.hasPrefix("-") { return .red.opacity(0.13) }
        return .clear
    }
}

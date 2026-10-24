import SwiftUI

/// Makes a project on the Mac: an empty repo, or a clone. The Mac picks
/// the folder, so the form only asks for a name. With a first instruction,
/// an agent starts on it straight away.
struct NewProjectView: View {
    /// Called with the project and, when one was started, its sandbox.
    let onCreated: (Project, Sandbox?) -> Void

    @Environment(AppModel.self) private var app
    @Environment(\.dismiss) private var dismiss

    @State private var options: ProjectOptions?
    @State private var clone = false
    @State private var url = ""
    @State private var name = ""
    @State private var publish = false
    @State private var task = ""
    @State private var creating = false
    @State private var error: String?

    private var trimmedURL: String { url.trimmingCharacters(in: .whitespaces) }

    /// The folder the project goes in, as the Mac will name it.
    private var folder: String {
        let n = name.trimmingCharacters(in: .whitespaces)
        return n.isEmpty && clone ? Self.repoName(trimmedURL) : n
    }

    private var nameError: String? {
        // As internal/core checks it: one plain folder name.
        let plain = { (c: Character) in c.isASCII && (c.isLetter || c.isNumber) }
        guard let first = folder.first,
              !plain(first) || !folder.allSatisfy({ plain($0) || ".-_".contains($0) })
        else { return nil }
        return "Use letters, digits, '.', '-' and '_', starting with a letter or digit."
    }

    private var canCreate: Bool {
        !creating && !folder.isEmpty && nameError == nil && (!clone || !trimmedURL.isEmpty)
    }

    var body: some View {
        NavigationStack {
            Form {
                Group { fields }.listRowBackground(Theme.panel)
            }
            .themedList()
            .navigationTitle("New Project")
            .navigationBarTitleDisplayMode(.inline)
            .toolbar {
                ToolbarItem(placement: .cancellationAction) { Button("Cancel") { dismiss() }.disabled(creating) }
                ToolbarItem(placement: .confirmationAction) {
                    if creating {
                        ProgressView()
                    } else {
                        Button("Create", action: create).disabled(!canCreate)
                    }
                }
            }
            .task { options = try? await app.projectOptions() }
        }
        .interactiveDismissDisabled(creating)
    }

    @ViewBuilder
    private var fields: some View {
        Section {
            Picker("Start from", selection: $clone) {
                Text("Empty repo").tag(false)
                Text("Clone a repo").tag(true)
            }
            .pickerStyle(.segmented)
            .labelsHidden()
            if clone {
                TextField("https://github.com/owner/repo", text: $url)
                    .font(.body.monospaced())
                    .textInputAutocapitalization(.never)
                    .autocorrectionDisabled()
                    .keyboardType(.URL)
            }
            LabeledContent("Name") {
                TextField(clone ? (Self.repoName(trimmedURL).isEmpty ? "From the URL" : Self.repoName(trimmedURL)) : "my-app", text: $name)
                    .font(.body.monospaced())
                    .textInputAutocapitalization(.never)
                    .autocorrectionDisabled()
            }
            if !clone, options?.canPublish == true {
                Toggle("Publish to GitHub as a private repo", isOn: $publish)
            }
        } header: {
            Text("Start from")
        } footer: {
            Text(footer).foregroundStyle(nameError == nil ? Theme.muted : Theme.fail)
        }

        Section {
            TextField("What should the agent build? (optional)", text: $task, axis: .vertical)
                .lineLimit(3...8)
        } footer: {
            Text("Starts Claude Code on a private clone. Leave it empty to choose the agent and its setup next.")
        }

        if let error {
            Section { Text(error).foregroundStyle(Theme.fail) }
        }
    }

    private var footer: String {
        if let nameError { return nameError }
        let mac = app.server?.name ?? "the Mac"
        var s = options.map { "Made in \($0.dir)/\(folder.isEmpty ? "…" : folder) on \(mac)." } ?? "Made in your projects folder on \(mac)."
        if !clone {
            s += " It starts with a README on main, so agents have a branch to start from."
            if options?.canPublish == false {
                s += " To publish to GitHub from here, install gh on the Mac and sign in."
            }
        }
        return s
    }

    private func create() {
        let r = NewProjectRequest(
            name: name.trimmingCharacters(in: .whitespaces),
            cloneUrl: clone ? trimmedURL : "",
            publish: !clone && publish,
            task: task.trimmingCharacters(in: .whitespacesAndNewlines))
        creating = true
        error = nil
        Task {
            do {
                let res = try await app.newProject(r)
                onCreated(res.project, res.sandbox)
            } catch {
                self.error = error.localizedDescription
            }
            creating = false
        }
    }

    /// The folder a clone of url goes in, as the Mac names it.
    static func repoName(_ url: String) -> String {
        var u = url
        while u.hasSuffix("/") { u.removeLast() }
        if u.hasSuffix(".git") { u.removeLast(4) }
        guard let i = u.lastIndex(where: { $0 == "/" || $0 == ":" }) else { return u }
        return String(u[u.index(after: i)...])
    }
}

import SwiftUI
import UIKit

/// The TUI's New sandbox form. It starts with what the project remembers:
/// its kits, and the model and MCP servers last picked.
struct NewSandboxView: View {
    let onCreated: (Sandbox) -> Void

    @Environment(AppModel.self) private var app
    @Environment(\.dismiss) private var dismiss

    @State private var projectID: Int64?
    @State private var options: Options?
    @State private var optionsError: String?
    @State private var task = ""
    @State private var source = "branch"
    @State private var value = ""
    @State private var clone = true
    @State private var agent = "claude"
    @State private var models: [String: String] = [:]
    @State private var kits: [String] = []
    @State private var pickedKits: Set<String> = []
    @State private var newKit = ""
    @State private var mcps: Set<String> = []
    @State private var creating = false
    @State private var error: String?

    private var agentOption: Options.Agent? { options?.agents.first { $0.id == agent } }

    private var canCreate: Bool {
        !creating && options != nil && !value.trimmingCharacters(in: .whitespaces).isEmpty
    }

    var body: some View {
        NavigationStack {
            Form {
                if app.projects.count > 1 {
                    Picker("Project", selection: $projectID) {
                        ForEach(app.projects) { Text($0.name).tag(Optional($0.id)) }
                    }
                }
                Section {
                    TextField("What should the agent do?", text: $task, axis: .vertical)
                        .lineLimit(3...8)
                } footer: {
                    Text("The agent's first instruction, and the sandbox's title.")
                }

                Section("Start from") {
                    Picker("Start from", selection: $source) {
                        Text("New branch").tag("branch")
                        Text("Pull request").tag("pr")
                    }
                    .pickerStyle(.segmented)
                    .labelsHidden()
                    TextField(source == "branch" ? "Branch, like feat/policy-log-json" : "PR number or URL", text: $value)
                        .font(.body.monospaced())
                        .textInputAutocapitalization(.never)
                        .autocorrectionDisabled()
                        .keyboardType(source == "pr" ? .numbersAndPunctuation : .asciiCapable)
                    if source == "branch" {
                        Toggle("Private clone", isOn: $clone)
                    }
                }

                if let options {
                    agentSection(options)
                    kitsSection
                    mcpSection(options)
                } else if let optionsError {
                    Section { Label(optionsError, systemImage: "exclamationmark.triangle").foregroundStyle(.secondary) }
                } else {
                    Section { HStack { ProgressView(); Text("Loading the Mac's options").foregroundStyle(.secondary) } }
                }

                if let error {
                    Section { Text(error).foregroundStyle(.red) }
                }
            }
            .navigationTitle("New Sandbox")
            .navigationBarTitleDisplayMode(.inline)
            .toolbar {
                ToolbarItem(placement: .cancellationAction) { Button("Cancel") { dismiss() } }
                ToolbarItem(placement: .confirmationAction) {
                    if creating {
                        ProgressView()
                    } else {
                        Button("Create", action: create).disabled(!canCreate)
                    }
                }
            }
            .task(id: projectID) { await loadOptions() }
            .onAppear {
                if projectID == nil { projectID = app.projects.first?.id }
            }
        }
        .interactiveDismissDisabled(creating)
    }

    @ViewBuilder
    private func agentSection(_ options: Options) -> some View {
        Section("Agent") {
            Picker("Agent", selection: $agent) {
                ForEach(options.agents) { Text($0.label).tag($0.id) }
            }
            if let a = agentOption, !a.models.isEmpty {
                Picker("Model", selection: Binding(get: { models[agent] ?? "" }, set: { models[agent] = $0 })) {
                    ForEach(a.models) { Text($0.label).tag($0.id) }
                }
            }
        }
    }

    private var kitsSection: some View {
        Section {
            ForEach(kits, id: \.self) { kit in
                Toggle(isOn: Binding(get: { pickedKits.contains(kit) }, set: { on in
                    if on { pickedKits.insert(kit) } else { pickedKits.remove(kit) }
                })) {
                    Text(kit).font(.subheadline.monospaced()).lineLimit(1).truncationMode(.middle)
                }
            }
            HStack {
                TextField("Add a kit reference", text: $newKit)
                    .font(.subheadline.monospaced())
                    .textInputAutocapitalization(.never)
                    .autocorrectionDisabled()
                    .onSubmit(addKit)
                Button("Add", action: addKit)
                    .disabled(newKit.trimmingCharacters(in: .whitespaces).isEmpty)
            }
        } header: {
            Text("Kits")
        } footer: {
            Text("The project's defaults come first. Each kit is a directory, ZIP, git URL or OCI image on the Mac.")
        }
    }

    @ViewBuilder
    private func mcpSection(_ options: Options) -> some View {
        Section {
            if let servers = options.mcpServers {
                if servers.isEmpty {
                    Text("No MCP servers are registered. Add them with sbx mcp add on your Mac.")
                        .foregroundStyle(.secondary)
                }
                ForEach(servers) { server in
                    Toggle(isOn: Binding(get: { mcps.contains(server.name) }, set: { on in
                        if on { mcps.insert(server.name) } else { mcps.remove(server.name) }
                    })) {
                        VStack(alignment: .leading, spacing: 2) {
                            Text(server.name)
                            if !server.ready {
                                Text(server.nextStep.map { "Needs setup. Run \($0) on your Mac" } ?? server.detail ?? server.status)
                                    .font(.footnote)
                                    .foregroundStyle(.orange)
                            }
                        }
                    }
                    .disabled(!server.ready && !mcps.contains(server.name))
                }
            } else {
                Text(options.mcpError ?? "Couldn't list the MCP servers.").foregroundStyle(.secondary)
            }
        } header: {
            Text("MCP servers")
        } footer: {
            Text("Fixed when the sandbox is created.")
        }
    }

    private func loadOptions() async {
        guard let projectID else { return }
        options = nil
        optionsError = nil
        do {
            let o = try await app.options(project: projectID)
            options = o
            agent = o.defaults.agent
            models = o.defaults.models
            clone = o.defaults.clone
            kits = o.defaults.kits
            pickedKits = Set(o.defaults.kits)
            mcps = Set(o.defaults.mcps)
        } catch is CancellationError {
        } catch {
            optionsError = error.localizedDescription
        }
    }

    private func addKit() {
        let k = newKit.trimmingCharacters(in: .whitespaces)
        guard !k.isEmpty else { return }
        if !kits.contains(k) { kits.append(k) }
        pickedKits.insert(k)
        newKit = ""
    }

    private func create() {
        guard let projectID, let options else { return }
        let hasModels = !(agentOption?.models.isEmpty ?? true)
        let servers = options.mcpServers ?? []
        let r = CreateRequest(
            projectId: projectID, agent: agent, source: source,
            value: value.trimmingCharacters(in: .whitespaces),
            task: task.trimmingCharacters(in: .whitespacesAndNewlines),
            clone: source == "pr" || clone,
            kits: kits.filter(pickedKits.contains),
            model: hasModels ? models[agent] ?? "" : "",
            mcps: servers.map(\.name).filter(mcps.contains))
        creating = true
        error = nil
        Task {
            do {
                let s = try await app.create(r)
                dismiss()
                onCreated(s)
            } catch {
                self.error = error.localizedDescription
            }
            creating = false
        }
    }
}

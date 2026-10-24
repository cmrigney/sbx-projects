import Foundation

// These mirror the JSON in internal/api/views.go. Times are Unix seconds,
// decoded with .secondsSince1970; zero times are left out of the JSON and
// arrive as nil.

nonisolated enum SandboxState: String, Codable, Sendable {
    case stopped, starting, working, needsYou, failed, ready, idle

    init(from decoder: Decoder) throws {
        let raw = try decoder.singleValueContainer().decode(String.self)
        self = SandboxState(rawValue: raw) ?? .idle
    }
}

/// The TUI's groups, in the order it lists them.
nonisolated enum StatusGroup: String, CaseIterable, Sendable {
    case needsYou, working, ready, parked

    var label: String {
        switch self {
        case .needsYou: "Needs you"
        case .working: "Working"
        case .ready: "Ready"
        case .parked: "Parked"
        }
    }

    init(_ state: SandboxState) {
        switch state {
        case .needsYou, .failed: self = .needsYou
        case .working, .starting: self = .working
        case .ready: self = .ready
        case .idle, .stopped: self = .parked
        }
    }
}

nonisolated struct Project: Codable, Sendable, Hashable, Identifiable {
    let id: Int64
    let name: String
    let path: String
    let remote: String?
    let defaultBranch: String
    let layout: String
    let kits: [String]
}

nonisolated struct Port: Codable, Sendable, Hashable {
    var hostIP: String?
    let hostPort: Int
    let sandboxPort: Int
    let proto: String

    nonisolated enum CodingKeys: String, CodingKey {
        case hostIP, hostPort, sandboxPort
        case proto = "protocol"
    }

    /// The port as host → sandbox, as the TUI shows it.
    var label: String {
        var host = "\(hostPort)"
        if let ip = hostIP, !ip.isEmpty {
            host = (ip.contains(":") ? "[\(ip)]" : ip) + ":" + host
        }
        return "\(host) → \(sandboxPort)" + (proto.isEmpty ? "" : "/\(proto)")
    }
}

/// A port to publish. Without a host port, the sandbox's port number is
/// used on the Mac too. Without a host IP, sbx's default address is used.
nonisolated struct PublishPortRequest: Codable, Sendable {
    let sandboxPort: Int
    let hostPort: Int?
    var hostIP: String?
}

nonisolated struct Sandbox: Codable, Sendable, Hashable, Identifiable {
    var id: String { name }

    let name: String
    let projectId: Int64
    let title: String
    let branch: String?
    let agent: String
    let agentLabel: String
    let model: String?
    let modelLabel: String?
    let clone: Bool
    let source: String

    let running: Bool
    let agentUp: Bool
    let state: SandboxState
    let statusText: String
    let busy: Bool
    let unread: Bool
    let hasReply: Bool
    let reportsActivity: Bool
    let canPR: Bool
    let canCheckout: Bool

    let added: Int
    let removed: Int
    let files: Int

    let cpus: Int?
    let memoryMiB: Int64?
    let ports: [Port]
    let kits: [String]
    let mcps: [String]

    let createdAt: Date
    let activeAt: Date?

    var group: StatusGroup { StatusGroup(state) }
}

nonisolated struct FeedItem: Codable, Sendable, Hashable {
    nonisolated enum Kind: String, Codable, Sendable {
        case plain, you, good, warn, bad

        init(from decoder: Decoder) throws {
            let raw = try decoder.singleValueContainer().decode(String.self)
            self = Kind(rawValue: raw) ?? .plain
        }
    }

    let at: Date
    let text: String
    let path: String?
    let kind: Kind
}

nonisolated struct FileChange: Codable, Sendable, Hashable {
    let path: String
    let added: Int
    let removed: Int
    let binary: Bool?
}

nonisolated struct Commit: Codable, Sendable, Hashable {
    let hash: String
    let at: Date
    let subject: String
}

/// A sandbox with its Activity, Changes and Setup tabs. The JSON is the
/// sandbox's own fields with these alongside.
nonisolated struct SandboxDetail: Decodable, Sendable, Hashable {
    let sandbox: Sandbox
    let activity: [FeedItem]
    let changes: [FileChange]
    let commits: [Commit]
    let base: String
    let workspace: String
    /// When a stopped sandbox's activity and changes were last seen.
    let probedAt: Date?

    nonisolated enum CodingKeys: String, CodingKey {
        case activity, changes, commits, base, workspace, probedAt
    }

    init(from decoder: Decoder) throws {
        sandbox = try Sandbox(from: decoder)
        let c = try decoder.container(keyedBy: CodingKeys.self)
        activity = try c.decode([FeedItem].self, forKey: .activity)
        changes = try c.decode([FileChange].self, forKey: .changes)
        commits = try c.decode([Commit].self, forKey: .commits)
        base = try c.decode(String.self, forKey: .base)
        workspace = try c.decode(String.self, forKey: .workspace)
        probedAt = try c.decodeIfPresent(Date.self, forKey: .probedAt)
    }
}

nonisolated struct Reply: Codable, Sendable {
    let at: Date
    let markdown: String
}

nonisolated struct Diff: Codable, Sendable {
    nonisolated struct File: Codable, Sendable, Identifiable {
        var id: String { path }
        let path: String
        let note: String?
        let lines: [String]
    }

    let files: [File]
    let truncated: Bool?
}

nonisolated struct Options: Codable, Sendable {
    nonisolated struct Agent: Codable, Sendable, Identifiable, Hashable {
        let id: String
        let label: String
        let models: [Model]
        let reportsActivity: Bool
    }

    nonisolated struct Model: Codable, Sendable, Identifiable, Hashable {
        let id: String
        let label: String
    }

    nonisolated struct MCPServer: Codable, Sendable, Identifiable, Hashable {
        var id: String { name }
        let name: String
        let ready: Bool
        let status: String
        let detail: String?
        /// The command, run on the Mac, that makes the server ready.
        let nextStep: String?
    }

    nonisolated struct Defaults: Codable, Sendable {
        let agent: String
        let clone: Bool
        let kits: [String]
        let mcps: [String]
        let models: [String: String]
    }

    let agents: [Agent]
    /// nil when sbx couldn't list them; mcpError says why.
    let mcpServers: [MCPServer]?
    let mcpError: String?
    let defaults: Defaults
}

nonisolated struct CreateRequest: Codable, Sendable {
    let projectId: Int64
    let agent: String
    /// "branch" or "pr".
    let source: String
    let value: String
    let task: String
    let clone: Bool
    let kits: [String]
    let model: String
    let mcps: [String]
}

nonisolated struct NewProjectRequest: Codable, Sendable {
    /// The project's folder, made in the Mac's projects folder. Left empty,
    /// a clone is named after its URL.
    let name: String
    let cloneUrl: String
    /// Creates a private GitHub repo for a new project.
    let publish: Bool
    /// When set, starts a first sandbox with it.
    let task: String
}

nonisolated struct NewProjectResult: Codable, Sendable {
    let project: Project
    let sandbox: Sandbox?
    /// A step that failed after the project was added, such as publishing it.
    let warning: String?
}

nonisolated struct ProjectOptions: Codable, Sendable {
    /// Where new projects go on the Mac.
    let dir: String
    /// Whether gh is installed and signed in on the Mac.
    let canPublish: Bool
}

/// One line of /v1/events.
nonisolated struct ServerEvent: Codable, Sendable {
    let type: String
    let projects: [Project]?
    let sandboxes: [Sandbox]?
    let listError: String?
    let sandbox: Sandbox?
    let name: String?
    let ok: Bool?
    let message: String?
}

nonisolated struct ServerInfo: Codable, Sendable {
    let id: String
    let name: String
    let version: Int
    let pairing: Bool
}

nonisolated struct PairResponse: Codable, Sendable {
    nonisolated struct Server: Codable, Sendable {
        let id: String
        let name: String
    }

    let token: String
    let deviceId: String
    let server: Server
}

import Foundation
import Network
import Observation
import UIKit

/// Everything the app shows comes from here. While the app is in the
/// foreground it keeps the Mac's event stream open and applies each change
/// as it arrives.
@MainActor @Observable
final class AppModel {
    enum Connection: Equatable {
        case connecting
        case live
        case offline(String)
        /// The Mac no longer accepts this device's token.
        case signedOut(String)
    }

    struct Toast: Identifiable, Equatable {
        let id = UUID()
        let message: String
        let ok: Bool
    }

    /// Every Mac this iPhone is paired with.
    private(set) var servers: [PairedServer] = []
    /// The one the app shows.
    private(set) var server: PairedServer?
    private(set) var connection: Connection = .connecting
    private(set) var projects: [Project] = []
    private(set) var sandboxes: [String: Sandbox] = [:]
    /// Why sbx couldn't list sandboxes on the Mac, if it couldn't.
    private(set) var listError: String?
    private(set) var pairing = false
    var pairingError: String?
    var toast: Toast?

    let browser = MacBrowser()

    private var client: APIClient?
    private var streamTask: Task<Void, Never>?
    /// Sandboxes with an action started from this device, whose outcome is
    /// shown as a toast.
    private var pending: Set<String> = []
    /// Creations sent from this device that the Mac hasn't answered yet.
    private var creating = 0
    /// Outcomes of actions on sandboxes not in pending, kept while a
    /// creation is unanswered: it can finish before its response arrives.
    private var finishedEarly: [String: Toast] = [:]
    /// Counts the stream's updates, so a response to a request sent before
    /// one of them doesn't replace what it said.
    private var streamSeq = 0
    private var snapshotAt = 0
    /// The update that last changed each sandbox, removals included.
    private var updatedAt: [String: Int] = [:]

    init() {
        let saved = PairedServers.load()
        servers = saved.servers
        server = servers.first { $0.id == saved.current } ?? servers.first
        makeClient()
    }

    private func makeClient() {
        guard let server, let token = Keychain.token(for: server.id) else {
            client = nil
            return
        }
        client = APIClient(address: server.address, fingerprint: server.fingerprint, token: token)
    }

    // MARK: Connection

    func connect() {
        guard client != nil, streamTask == nil else { return }
        streamTask = Task { await runStream() }
    }

    func disconnect() {
        streamTask?.cancel()
        streamTask = nil
    }

    /// Reconnects, which starts again from a fresh snapshot.
    func refresh() async {
        disconnect()
        connection = .connecting
        connect()
        // Give pull to refresh something to wait on.
        for _ in 0..<20 where connection != .live {
            try? await Task.sleep(for: .milliseconds(100))
        }
    }

    private func runStream() async {
        var failures = 0
        while !Task.isCancelled, let client {
            if connection != .live {
                connection = .connecting
            }
            do {
                for try await event in client.events() {
                    // A stream from the Mac shown before a switch.
                    if Task.isCancelled { return }
                    apply(event)
                    failures = 0
                }
            } catch APIError.signedOut(let message) {
                if Task.isCancelled { return }
                connection = .signedOut(message)
                streamTask = nil
                return
            } catch {
                if Task.isCancelled { return }
                failures += 1
                connection = .offline(error.localizedDescription)
                // The Mac may have a new address on the network.
                if failures >= 2, await rediscover() {
                    continue
                }
            }
            if Task.isCancelled { return }
            try? await Task.sleep(for: .seconds(min(2 * max(failures, 1), 10)))
        }
    }

    private func apply(_ event: ServerEvent) {
        switch event.type {
        case "snapshot":
            streamSeq += 1
            snapshotAt = streamSeq
            updatedAt = [:]
            projects = event.projects ?? []
            sandboxes = Dictionary((event.sandboxes ?? []).map { ($0.name, $0) }, uniquingKeysWith: { _, new in new })
            listError = event.listError
            connection = .live
        case "sandbox":
            if let s = event.sandbox {
                streamSeq += 1
                updatedAt[s.name] = streamSeq
                sandboxes[s.name] = s
            }
        case "removed":
            if let name = event.name {
                streamSeq += 1
                updatedAt[name] = streamSeq
                sandboxes[name] = nil
            }
        case "projects":
            projects = event.projects ?? projects
        case "status":
            // Sent when listing on the Mac starts or stops failing; until it
            // clears, the states shown are the last ones seen.
            listError = event.listError
        case "action":
            guard let name = event.name else { break }
            let t = Toast(message: event.message ?? "", ok: event.ok == true)
            if pending.remove(name) != nil {
                toast = t
            } else {
                if !t.ok {
                    toast = t
                }
                if creating > 0 {
                    finishedEarly[name] = t
                }
            }
        default:
            break
        }
    }

    /// Looks for the paired Mac on the network and switches to its current
    /// address. Returns whether the address changed.
    private func rediscover() async -> Bool {
        guard var server else { return false }
        // Its own browser, so it doesn't stop the one the pairing screen
        // shows while another Mac is being paired.
        let browser = MacBrowser()
        browser.start()
        defer { browser.stop() }
        for _ in 0..<10 {
            if let mac = browser.found.first(where: { $0.serverID == server.id }) {
                guard let address = try? await MacBrowser.resolve(mac.endpoint), address != server.address else { return false }
                guard !Task.isCancelled, self.server?.id == server.id else { return false }
                server.address = address
                self.server = server
                servers = servers.map { $0.id == server.id ? server : $0 }
                save()
                makeClient()
                return true
            }
            try? await Task.sleep(for: .milliseconds(500))
        }
        return false
    }

    // MARK: Pairing

    var deviceName: String { UIDevice.current.name }

    /// Pairs with the Mac whose QR code was scanned.
    func pair(link: String) async {
        guard let p = PairingLink(string: link) else {
            pairingError = "That isn't a pairing code from sbx-projects api."
            return
        }
        pairing = true
        defer { pairing = false }
        pairingError = nil
        for host in p.hosts {
            let address = ServerAddress(host: host, port: p.port)
            let c = APIClient(address: address, fingerprint: p.fingerprint, token: nil)
            guard (try? await c.serverInfo(timeout: 4)) != nil else { continue }
            await pair(client: c, fingerprint: p.fingerprint, code: p.code)
            return
        }
        pairingError = "Couldn't reach \(p.name). Check that your iPhone is on the same Wi-Fi network as the Mac, and that Local Network access is on for sbx-projects in Settings."
    }

    /// Pairs with a Mac found on the network, using the code shown in its
    /// terminal. The fingerprint is the one the person was shown.
    func pair(address: ServerAddress, fingerprint: String, code: String) async {
        pairing = true
        defer { pairing = false }
        pairingError = nil
        await pair(client: APIClient(address: address, fingerprint: fingerprint, token: nil), fingerprint: fingerprint, code: code)
    }

    private func pair(client c: APIClient, fingerprint: String, code: String) async {
        do {
            let r = try await c.pair(code: code, device: deviceName)
            guard fingerprint.hasPrefix(r.server.id) else { throw APIError.wrongCertificate }
            Keychain.setToken(r.token, for: r.server.id)
            let s = PairedServer(id: r.server.id, name: r.server.name, fingerprint: fingerprint, address: c.address)
            // Pairing again with a Mac replaces it where it was in the list.
            if let i = servers.firstIndex(where: { $0.id == s.id }) {
                servers[i] = s
            } else {
                servers.append(s)
            }
            show(s)
        } catch {
            pairingError = error.localizedDescription
        }
    }

    /// Switches to another paired Mac.
    func select(_ id: String) {
        guard id != server?.id, let s = servers.first(where: { $0.id == id }) else { return }
        show(s)
    }

    /// Forgets a Mac on this device, and shows the next one if it was the
    /// one shown. The Mac keeps the device listed until
    /// sbx-projects api devices rm removes it.
    func forget(_ id: String) {
        Keychain.deleteToken(for: id)
        servers.removeAll { $0.id == id }
        if server?.id == id {
            show(servers.first)
        } else {
            save()
        }
    }

    /// Shows a Mac, starting again from its snapshot, or nothing.
    private func show(_ s: PairedServer?) {
        disconnect()
        server = s
        projects = []
        sandboxes = [:]
        listError = nil
        pending = []
        finishedEarly = [:]
        toast = nil
        connection = .connecting
        save()
        makeClient()
        connect()
    }

    private func save() {
        PairedServers.save(servers, current: server?.id)
    }

    // MARK: Reads

    private func api() throws -> APIClient {
        guard let client else { throw APIError.signedOut("This iPhone isn't paired with a Mac.") }
        return client
    }

    func detail(_ name: String) async throws -> SandboxDetail {
        try await api().sandbox(name)
    }

    func reply(_ name: String) async throws -> Reply {
        try await api().reply(name)
    }

    func diff(_ name: String) async throws -> Diff {
        try await api().diff(name)
    }

    func options(project: Int64) async throws -> Options {
        try await api().options(project: project)
    }

    func projectOptions() async throws -> ProjectOptions {
        try await api().projectOptions()
    }

    func terminal(_ name: String, cols: Int, rows: Int, shell: Bool = false) throws -> URLSessionWebSocketTask {
        try api().terminal(name, cols: cols, rows: rows, shell: shell)
    }

    func markSeen(_ name: String) async {
        let mark = streamSeq
        if let s = try? await api().markSeen(name) {
            applyResponse(s, since: mark)
        }
    }

    /// Shows a sandbox a request returned, unless the stream has said
    /// something newer about it since mark, when the request was sent.
    private func applyResponse(_ s: Sandbox, since mark: Int) {
        guard snapshotAt <= mark, (updatedAt[s.name] ?? 0) <= mark else { return }
        sandboxes[s.name] = s
    }

    // MARK: Actions

    /// Runs an action, showing its result in the list right away. Returns
    /// whether the Mac accepted it; how it ends arrives as a toast.
    @discardableResult
    private func run(_ name: String, _ fn: (APIClient) async throws -> Sandbox) async -> Bool {
        let mark = streamSeq
        do {
            pending.insert(name)
            let s = try await fn(api())
            applyResponse(s, since: mark)
            return true
        } catch {
            pending.remove(name)
            toast = Toast(message: error.localizedDescription, ok: false)
            return false
        }
    }

    func send(_ text: String, files: [Attachment] = [], to name: String) async -> Bool {
        await run(name) { try await $0.send(text, files: files, to: name) }
    }

    func openPR(_ name: String) async {
        await run(name) { try await $0.act("pr", on: name) }
    }

    func stop(_ name: String) async {
        await run(name) { try await $0.act("stop", on: name) }
    }

    func resume(_ name: String) async {
        await run(name) { try await $0.act("resume", on: name) }
    }

    func checkout(_ name: String) async {
        await run(name) { try await $0.act("checkout", on: name) }
    }

    @discardableResult
    func publishPort(_ r: PublishPortRequest, on name: String) async -> Bool {
        await run(name) { try await $0.publishPort(r, on: name) }
    }

    func unpublishPort(_ port: Port, on name: String) async {
        await run(name) { try await $0.unpublishPort(port, on: name) }
    }

    func remove(_ name: String) async {
        await run(name) { try await $0.remove(name) }
    }

    func create(_ r: CreateRequest) async throws -> Sandbox {
        let mark = streamSeq
        creating += 1
        defer {
            creating -= 1
            if creating == 0 {
                finishedEarly = [:]
            }
        }
        let s = try await api().create(r)
        if let t = finishedEarly.removeValue(forKey: s.name) {
            // A failure was shown when it arrived.
            if t.ok {
                toast = t
            }
        } else {
            pending.insert(s.name)
        }
        applyResponse(s, since: mark)
        return s
    }

    /// Makes a project, and its first sandbox when given a task. Returns
    /// once the project exists.
    func newProject(_ r: NewProjectRequest) async throws -> NewProjectResult {
        let mark = streamSeq
        creating += 1
        defer {
            creating -= 1
            if creating == 0 {
                finishedEarly = [:]
            }
        }
        let res = try await api().newProject(r)
        // The stream sends the projects too; this shows the new one without waiting.
        if !projects.contains(where: { $0.id == res.project.id }) {
            projects = (projects + [res.project]).sorted { $0.name.localizedCaseInsensitiveCompare($1.name) == .orderedAscending }
        }
        if let s = res.sandbox {
            if let t = finishedEarly.removeValue(forKey: s.name) {
                if t.ok {
                    toast = t
                }
            } else {
                pending.insert(s.name)
            }
            applyResponse(s, since: mark)
        }
        if let w = res.warning {
            toast = Toast(message: w, ok: false)
        }
        return res
    }

    // MARK: Lists

    func project(_ id: Int64) -> Project? {
        projects.first { $0.id == id }
    }

    /// The project's sandboxes in the TUI's order: by group, then newest.
    /// A nil group is every sandbox that isn't parked.
    func sandboxes(in project: Project, group: StatusGroup?, matching query: String) -> [Sandbox] {
        let q = query.trimmingCharacters(in: .whitespaces)
        return sandboxes.values
            .filter { $0.projectId == project.id }
            .filter { Self.shows(group, $0) }
            .filter { q.isEmpty || $0.title.localizedCaseInsensitiveContains(q) || $0.name.localizedCaseInsensitiveContains(q) || ($0.branch ?? "").localizedCaseInsensitiveContains(q) }
            .sorted { a, b in
                let ga = StatusGroup.allCases.firstIndex(of: a.group)!, gb = StatusGroup.allCases.firstIndex(of: b.group)!
                if ga != gb { return ga < gb }
                if a.createdAt != b.createdAt { return a.createdAt > b.createdAt }
                return a.name < b.name
            }
    }

    func count(_ group: StatusGroup?) -> Int {
        sandboxes.values.filter { Self.shows(group, $0) }.count
    }

    private static func shows(_ group: StatusGroup?, _ sandbox: Sandbox) -> Bool {
        group == nil ? sandbox.group != .parked : sandbox.group == group
    }
}

import PhotosUI
import SwiftUI
import UIKit
import UniformTypeIdentifiers

/// Types a message into the agent's session. A stopped sandbox is started,
/// and an agent that isn't running is started with the message. Attached
/// photos and files are copied into the sandbox and the message names
/// their paths there.
struct Composer: View {
    let sandbox: Sandbox
    var placeholder: String?
    var focused: FocusState<Bool>.Binding
    var onSent: () -> Void = {}

    @Environment(AppModel.self) private var app
    @State private var text = ""
    @State private var files: [Attachment] = []
    @State private var sending = false
    @State private var loading = false
    @State private var choosingPhotos = false
    @State private var choosingFiles = false
    @State private var photos: [PhotosPickerItem] = []
    @State private var attachError: String?

    private var trimmed: String { text.trimmingCharacters(in: .whitespacesAndNewlines) }
    private var canSend: Bool { (!trimmed.isEmpty || !files.isEmpty) && !sending && !loading && !sandbox.busy }

    var body: some View {
        let shape = RoundedRectangle(cornerRadius: 14, style: .continuous)
        VStack(alignment: .leading, spacing: 0) {
            if !files.isEmpty || loading {
                attachments
            }
            HStack(alignment: .bottom, spacing: 10) {
                attachMenu
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
            .padding(.leading, 6)
            .padding(.trailing, 5)
            .padding(.vertical, 5)
        }
        .background(Theme.panel, in: shape)
        .overlay(shape.strokeBorder(focused.wrappedValue ? Theme.accent.opacity(0.7) : Theme.line, lineWidth: 1))
        .padding(.horizontal)
        .padding(.vertical, 8)
        .background(.bar)
        .photosPicker(isPresented: $choosingPhotos, selection: $photos, maxSelectionCount: 10, matching: .images)
        .onChange(of: photos) { _, items in
            guard !items.isEmpty else { return }
            photos = []
            Task { await addPhotos(items) }
        }
        .fileImporter(isPresented: $choosingFiles, allowedContentTypes: [.item], allowsMultipleSelection: true) { result in
            switch result {
            case .success(let urls): addFiles(urls)
            case .failure(let error): attachError = error.localizedDescription
            }
        }
        .alert("Couldn't attach", isPresented: Binding(get: { attachError != nil }, set: { if !$0 { attachError = nil } })) {
            Button("OK", role: .cancel) {}
        } message: {
            Text(attachError ?? "")
        }
    }

    private var attachMenu: some View {
        Menu {
            Button("Photo Library", systemImage: "photo.on.rectangle") { choosingPhotos = true }
            Button("Choose File", systemImage: "folder") { choosingFiles = true }
        } label: {
            Image(systemName: "paperclip")
                .font(.body.weight(.semibold))
                .foregroundStyle(Theme.accent)
                .frame(width: 34, height: 34)
        }
        .disabled(sending)
        .accessibilityLabel("Attach")
    }

    /// Tapping a file adds its name to the message, so the message can say
    /// which file it means.
    private var attachments: some View {
        ScrollView(.horizontal, showsIndicators: false) {
            HStack(spacing: 6) {
                ForEach(files) { f in
                    HStack(spacing: 4) {
                        Button {
                            mention(f)
                        } label: {
                            HStack(spacing: 5) {
                                Image(systemName: f.mimeType.hasPrefix("image/") ? "photo" : "doc")
                                Text(f.name).lineLimit(1).truncationMode(.middle)
                                Text(ByteCountFormatter.string(fromByteCount: Int64(f.data.count), countStyle: .file))
                                    .foregroundStyle(Theme.dim)
                            }
                        }
                        .accessibilityHint("Mentions it in the message")
                        Button {
                            files.removeAll { $0.id == f.id }
                        } label: {
                            Image(systemName: "xmark").font(.caption2.weight(.bold)).foregroundStyle(Theme.muted)
                        }
                        .accessibilityLabel("Remove \(f.name)")
                    }
                    .font(.caption.monospaced())
                    .foregroundStyle(Theme.text)
                    .frame(maxWidth: 220)
                    .padding(.horizontal, 8)
                    .padding(.vertical, 6)
                    .background(Theme.panel2, in: RoundedRectangle(cornerRadius: 8, style: .continuous))
                    .overlay(RoundedRectangle(cornerRadius: 8, style: .continuous).strokeBorder(Theme.line, lineWidth: 1))
                }
                if loading {
                    ProgressView().controlSize(.small).padding(.horizontal, 8)
                }
            }
            .padding(.horizontal, 8)
            .padding(.top, 8)
        }
    }

    private func mention(_ f: Attachment) {
        let gap = text.isEmpty || text.last?.isWhitespace == true ? "" : " "
        text += gap + f.name + " "
        focused.wrappedValue = true
    }

    private func add(_ name: String, _ data: Data, _ mimeType: String) {
        let unique = Attachment.uniqueName(name, among: files.map(\.name))
        files.append(Attachment(name: unique, data: data, mimeType: mimeType))
    }

    /// Photos arrive as JPEG, since agents can't read HEIC.
    private func addPhotos(_ items: [PhotosPickerItem]) async {
        loading = true
        defer { loading = false }
        for item in items {
            guard let data = try? await item.loadTransferable(type: Data.self) else {
                attachError = "A photo couldn't be loaded. If it's in iCloud, try again once it has downloaded."
                continue
            }
            let isPNG = item.supportedContentTypes.contains(.png)
            if isPNG {
                add("photo.png", data, "image/png")
            } else if let jpeg = UIImage(data: data)?.jpegData(compressionQuality: 0.85) {
                add("photo.jpg", jpeg, "image/jpeg")
            } else {
                attachError = "A photo isn't in a format that can be sent."
            }
        }
    }

    private func addFiles(_ urls: [URL]) {
        for url in urls {
            let scoped = url.startAccessingSecurityScopedResource()
            defer { if scoped { url.stopAccessingSecurityScopedResource() } }
            do {
                let data = try Data(contentsOf: url)
                let type = UTType(filenameExtension: url.pathExtension)?.preferredMIMEType ?? "application/octet-stream"
                add(url.lastPathComponent, data, type)
            } catch {
                attachError = "\(url.lastPathComponent) couldn't be read: \(error.localizedDescription)"
            }
        }
    }

    private func send() {
        let message = trimmed
        let sent = files
        sending = true
        Task {
            if await app.send(message, files: sent, to: sandbox.name) {
                text = ""
                files = []
                onSent()
            }
            sending = false
        }
    }
}

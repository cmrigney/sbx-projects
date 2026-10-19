import SwiftUI
import UIKit

/// The agent's last reply in full, as the TUI's t shows it. Opening it
/// clears the unread mark.
struct ReplySheet: View {
    let sandbox: Sandbox

    @Environment(AppModel.self) private var app
    @Environment(\.dismiss) private var dismiss
    @State private var reply: Reply?
    @State private var error: String?

    var body: some View {
        NavigationStack {
            ScrollView {
                if let reply {
                    MarkdownView(markdown: reply.markdown)
                        .padding()
                        .textSelection(.enabled)
                } else if let error {
                    ContentUnavailableView("No reply", systemImage: "text.bubble", description: Text(error))
                        .padding(.top, 60)
                } else {
                    ProgressView().padding(.top, 60)
                }
            }
            .navigationTitle("Last reply")
            .navigationBarTitleDisplayMode(.inline)
            .toolbar {
                ToolbarItem(placement: .principal) {
                    VStack(spacing: 0) {
                        Text("Last reply").font(.headline)
                        if let reply {
                            Text("\(sandbox.agentLabel) · \(shortAgo(reply.at)) ago").font(.caption).foregroundStyle(.secondary)
                        }
                    }
                }
                ToolbarItem(placement: .topBarLeading) {
                    Button("Copy") { UIPasteboard.general.string = reply?.markdown }
                        .disabled(reply == nil)
                }
                ToolbarItem(placement: .topBarTrailing) {
                    Button("Done") { dismiss() }.fontWeight(.semibold)
                }
            }
            .safeAreaInset(edge: .bottom) {
                Composer(sandbox: sandbox, placeholder: "Reply to \(sandbox.agentLabel)") { dismiss() }
            }
        }
        .presentationDetents([.large, .medium])
        .presentationDragIndicator(.visible)
        .task {
            do {
                reply = try await app.reply(sandbox.name)
                await app.markSeen(sandbox.name)
            } catch {
                self.error = error.localizedDescription
            }
        }
    }
}

/// Renders the markdown agents write: headings, paragraphs, lists, quotes,
/// fenced code and tables, with inline styles and links. Anything else
/// shows as plain text.
struct MarkdownView: View {
    let markdown: String

    var body: some View {
        VStack(alignment: .leading, spacing: 12) {
            ForEach(Array(MarkdownBlock.parse(markdown).enumerated()), id: \.offset) { _, block in
                view(for: block)
            }
        }
        .frame(maxWidth: .infinity, alignment: .leading)
    }

    @ViewBuilder
    private func view(for block: MarkdownBlock) -> some View {
        switch block {
        case .heading(let level, let text):
            Text(inline(text))
                .font(level == 1 ? .title2.bold() : level == 2 ? .title3.bold() : .headline)
                .padding(.top, 4)
        case .paragraph(let text):
            Text(inline(text))
        case .list(let items):
            VStack(alignment: .leading, spacing: 6) {
                ForEach(Array(items.enumerated()), id: \.offset) { _, item in
                    HStack(alignment: .firstTextBaseline, spacing: 8) {
                        Text(item.marker).monospacedDigit().foregroundStyle(.secondary)
                        Text(inline(item.text))
                    }
                    .padding(.leading, CGFloat(item.depth) * 18)
                }
            }
        case .quote(let text):
            HStack(spacing: 10) {
                RoundedRectangle(cornerRadius: 1.5).fill(Color(.separator)).frame(width: 3)
                Text(inline(text)).foregroundStyle(.secondary)
            }
        case .code(let text):
            ScrollView(.horizontal, showsIndicators: false) {
                Text(text)
                    .font(.system(.footnote, design: .monospaced))
                    .padding(12)
            }
            .background(Color(.tertiarySystemFill), in: RoundedRectangle(cornerRadius: 10, style: .continuous))
        case .table(let header, let rows):
            ScrollView(.horizontal, showsIndicators: false) {
                Grid(alignment: .leading, horizontalSpacing: 0, verticalSpacing: 0) {
                    GridRow {
                        ForEach(Array(header.enumerated()), id: \.offset) { _, cell in
                            Text(inline(cell)).font(.subheadline.weight(.semibold)).padding(8)
                        }
                    }
                    .background(Color(.tertiarySystemFill))
                    ForEach(Array(rows.enumerated()), id: \.offset) { _, row in
                        Divider()
                        GridRow {
                            ForEach(Array(row.enumerated()), id: \.offset) { _, cell in
                                Text(inline(cell)).font(.subheadline).padding(8)
                            }
                        }
                    }
                }
                .overlay(RoundedRectangle(cornerRadius: 10, style: .continuous).stroke(Color(.separator), lineWidth: 0.5))
                .clipShape(RoundedRectangle(cornerRadius: 10, style: .continuous))
            }
        case .rule:
            Divider()
        }
    }

    private func inline(_ text: String) -> AttributedString {
        let opts = AttributedString.MarkdownParsingOptions(interpretedSyntax: .inlineOnlyPreservingWhitespace)
        return (try? AttributedString(markdown: text, options: opts)) ?? AttributedString(text)
    }
}

nonisolated enum MarkdownBlock: Equatable {
    nonisolated struct Item: Equatable {
        let depth: Int
        let marker: String
        let text: String
    }

    case heading(Int, String)
    case paragraph(String)
    case list([Item])
    case quote(String)
    case code(String)
    case table(header: [String], rows: [[String]])
    case rule

    static func parse(_ source: String) -> [MarkdownBlock] {
        let lines = source.replacingOccurrences(of: "\r\n", with: "\n").components(separatedBy: "\n")
        var blocks: [MarkdownBlock] = []
        var para: [String] = []
        var i = 0

        func flush() {
            if !para.isEmpty {
                blocks.append(.paragraph(para.joined(separator: " ")))
                para = []
            }
        }

        while i < lines.count {
            let line = lines[i]
            let t = line.trimmingCharacters(in: .whitespaces)

            if t.isEmpty {
                flush()
                i += 1
            } else if t.hasPrefix("```") || t.hasPrefix("~~~") {
                flush()
                let fence = String(t.prefix(3))
                var code: [String] = []
                i += 1
                while i < lines.count, !lines[i].trimmingCharacters(in: .whitespaces).hasPrefix(fence) {
                    code.append(lines[i])
                    i += 1
                }
                blocks.append(.code(code.joined(separator: "\n")))
                i += 1
            } else if let h = parseHeading(t) {
                flush()
                blocks.append(.heading(h.level, h.text))
                i += 1
            } else if t.allSatisfy({ $0 == "-" || $0 == "*" || $0 == "_" || $0 == " " }), t.filter({ $0 != " " }).count >= 3 {
                flush()
                blocks.append(.rule)
                i += 1
            } else if t.hasPrefix("|"), i + 1 < lines.count, isTableRule(lines[i + 1]) {
                flush()
                let header = cells(t)
                var rows: [[String]] = []
                i += 2
                while i < lines.count, lines[i].trimmingCharacters(in: .whitespaces).hasPrefix("|") {
                    var row = cells(lines[i].trimmingCharacters(in: .whitespaces))
                    // Grid needs every row as wide as the header.
                    row += Array(repeating: "", count: max(0, header.count - row.count))
                    rows.append(Array(row.prefix(header.count)))
                    i += 1
                }
                blocks.append(.table(header: header, rows: rows))
            } else if listItem(line) != nil {
                flush()
                var items: [Item] = []
                while i < lines.count, let item = listItem(lines[i]) {
                    items.append(item)
                    i += 1
                    // A wrapped line continues the item above it.
                    while i < lines.count, listItem(lines[i]) == nil, lines[i].hasPrefix("  "),
                          !lines[i].trimmingCharacters(in: .whitespaces).isEmpty
                    {
                        let last = items.removeLast()
                        items.append(Item(depth: last.depth, marker: last.marker,
                                          text: last.text + " " + lines[i].trimmingCharacters(in: .whitespaces)))
                        i += 1
                    }
                }
                blocks.append(.list(items))
            } else if t.hasPrefix(">") {
                flush()
                var quote: [String] = []
                while i < lines.count, lines[i].trimmingCharacters(in: .whitespaces).hasPrefix(">") {
                    quote.append(String(lines[i].trimmingCharacters(in: .whitespaces).dropFirst()).trimmingCharacters(in: .whitespaces))
                    i += 1
                }
                blocks.append(.quote(quote.joined(separator: " ")))
            } else {
                para.append(t)
                i += 1
            }
        }
        flush()
        return blocks
    }

    private static func parseHeading(_ t: String) -> (level: Int, text: String)? {
        let hashes = t.prefix { $0 == "#" }.count
        guard (1...6).contains(hashes), t.dropFirst(hashes).first == " " else { return nil }
        return (hashes, t.dropFirst(hashes).trimmingCharacters(in: .whitespaces))
    }

    private static func listItem(_ line: String) -> Item? {
        let indent = line.prefix { $0 == " " }.count
        let t = line.dropFirst(indent)
        if let first = t.first, "-*+".contains(first), t.dropFirst().first == " " {
            return Item(depth: indent / 2, marker: "•", text: t.dropFirst(2).trimmingCharacters(in: .whitespaces))
        }
        let digits = t.prefix { $0.isNumber }
        if !digits.isEmpty, digits.count <= 3 {
            let rest = t.dropFirst(digits.count)
            if let dot = rest.first, dot == "." || dot == ")", rest.dropFirst().first == " " {
                return Item(depth: indent / 2, marker: "\(digits).", text: rest.dropFirst(2).trimmingCharacters(in: .whitespaces))
            }
        }
        return nil
    }

    private static func isTableRule(_ line: String) -> Bool {
        let t = line.trimmingCharacters(in: .whitespaces)
        return t.hasPrefix("|") && t.contains("-") && t.allSatisfy { "|-: ".contains($0) }
    }

    private static func cells(_ row: String) -> [String] {
        var r = Substring(row)
        if r.hasPrefix("|") { r = r.dropFirst() }
        if r.hasSuffix("|") { r = r.dropLast() }
        return r.split(separator: "|", omittingEmptySubsequences: false).map { $0.trimmingCharacters(in: .whitespaces) }
    }
}

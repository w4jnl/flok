// An agent's feed: what the user asked, what the agent wrote, and one line per tool it ran.
// The readable view of a session; the screen (ScreenView) is the terminal itself.
import SwiftUI

struct FeedView: View {
    let state: FeedState?
    var onTap: () -> Void = {}
    @State private var follow = BottomFollower()
    @State private var waitedLong = false

    var body: some View {
        ScrollViewReader { proxy in
            ScrollView(.vertical) {
                LazyVStack(alignment: .leading, spacing: 10) {
                    ForEach(state?.items ?? []) { item in
                        FeedRow(item: item)
                    }
                    Color.clear.frame(width: 1, height: 1).id("bottom")
                }
                .padding(.horizontal, 14).padding(.vertical, 10)
                .frame(maxWidth: .infinity, alignment: .leading)
            }
            .background(Color(UIColor.systemBackground))
            .scrollDismissesKeyboard(.immediately)
            .onTapGesture { onTap() }
            .modifier(FollowWatch(follow: follow))
            .onChange(of: state?.items.last?.id) { _, _ in if follow.following { toBottom(proxy) } }
            .onAppear { toBottom(proxy) }
            .overlay(alignment: .bottomTrailing) {
                if !follow.following, !(state?.items.isEmpty ?? true) {
                    Button {
                        follow.resume()
                        toBottom(proxy)
                    } label: {
                        Image(systemName: "arrow.down").fontWeight(.semibold).padding(10)
                    }
                    .background(.thinMaterial, in: Circle())
                    .padding(12)
                    .accessibilityLabel("Back to the latest, following the feed again")
                    .transition(.opacity)
                }
            }
            .animation(.easeInOut(duration: 0.15), value: follow.following)
            .overlay { placeholder }
        }
    }

    /// Nothing to show yet, or nothing to come: a word why, never a blank page.
    @ViewBuilder private var placeholder: some View {
        if let state {
            if state.items.isEmpty {
                VStack(spacing: 8) {
                    if let note = state.note {
                        Image(systemName: "text.page.slash").font(.title2).foregroundStyle(.secondary)
                        Text(note).font(.footnote).foregroundStyle(.secondary).multilineTextAlignment(.center)
                    } else {
                        Text("Nothing in this session yet.").font(.footnote).foregroundStyle(.secondary)
                    }
                }
                .padding(.horizontal, 24)
            }
        } else {
            VStack(spacing: 8) {
                ProgressView("waiting for the feed…").font(.footnote)
                if waitedLong {
                    Text("Nothing yet. The instance may be offline; the screen is behind the switch at the top.")
                        .font(.footnote).foregroundStyle(.secondary).multilineTextAlignment(.center).padding(.horizontal, 24)
                }
            }
            .task {
                try? await Task.sleep(for: .seconds(6))
                waitedLong = true
            }
        }
    }

    private func toBottom(_ proxy: ScrollViewProxy) {
        follow.settle()
        withAnimation(.linear(duration: 0.1)) { proxy.scrollTo("bottom", anchor: .bottom) }
    }
}

/// One item: a prompt in its own box, the agent's text as blocks, a tool call as a quiet line
/// with the tool's glyph, a note centred.
struct FeedRow: View {
    let item: FeedItem

    var body: some View {
        switch item.kind {
        case FeedItem.Kind.prompt: prompt
        case FeedItem.Kind.tool: tool
        case FeedItem.Kind.note: note
        default: MarkdownText(text: item.text)
        }
    }

    private var prompt: some View {
        VStack(alignment: .leading, spacing: 4) {
            Text("You").font(.caption2.weight(.semibold)).foregroundStyle(Palette.cyan)
            Text(item.text).font(.callout).textSelection(.enabled)
        }
        .padding(10)
        .frame(maxWidth: .infinity, alignment: .leading)
        .background(Palette.cyan.opacity(0.1), in: RoundedRectangle(cornerRadius: 10))
        .padding(.top, 6)
    }

    private var tool: some View {
        HStack(alignment: .firstTextBaseline, spacing: 8) {
            Image(systemName: Self.symbol(for: item.tool)).font(.caption).frame(width: 16)
            Text(item.text).font(.footnote).lineLimit(2)
        }
        .foregroundStyle(.secondary)
        .padding(.leading, 2)
    }

    private var note: some View {
        Text(item.text).font(.caption).foregroundStyle(.tertiary)
            .frame(maxWidth: .infinity, alignment: .center).padding(.vertical, 4)
    }

    /// A glyph per tool: the terminal for Bash, a page for a read, a pencil for an edit, a
    /// magnifier for a search, two people for an agent, a globe for the web, a question mark
    /// for a question; a wrench for the rest.
    nonisolated static func symbol(for tool: String?) -> String {
        switch tool ?? "" {
        case "Bash": return "terminal"
        case "Read": return "doc.text"
        case "Edit", "MultiEdit", "NotebookEdit", "Write": return "pencil.line"
        case "Grep", "Glob": return "magnifyingglass"
        case "Agent", "Task": return "person.2"
        case "WebFetch", "WebSearch": return "globe"
        case "AskUserQuestion": return "questionmark.circle"
        case "Skill": return "sparkles"
        case "TodoWrite": return "checklist"
        default: return "wrench"
        }
    }
}

/// The agent's text, block by block (FeedMarkdown): headings, lists, code in a monospaced
/// strip that scrolls sideways, quotes, rules, tables.
struct MarkdownText: View {
    let text: String

    var body: some View {
        VStack(alignment: .leading, spacing: 6) {
            ForEach(FeedMarkdown.blocks(of: text)) { block in
                switch block.kind {
                case .paragraph:
                    Text(FeedMarkdown.inline(block.text)).font(.callout).textSelection(.enabled)
                case .heading(let level):
                    Text(FeedMarkdown.inline(block.text)).font(level <= 2 ? .headline : .subheadline.weight(.semibold)).padding(.top, 4)
                case .bullet(let indent):
                    HStack(alignment: .firstTextBaseline, spacing: 6) {
                        Text("•").font(.callout)
                        Text(FeedMarkdown.inline(block.text)).font(.callout).textSelection(.enabled)
                    }
                    .padding(.leading, CGFloat(indent) * 14 + 4)
                case .numbered(let indent, let label):
                    HStack(alignment: .firstTextBaseline, spacing: 6) {
                        Text(label).font(.callout).monospacedDigit()
                        Text(FeedMarkdown.inline(block.text)).font(.callout).textSelection(.enabled)
                    }
                    .padding(.leading, CGFloat(indent) * 14 + 4)
                case .code, .table:
                    ScrollView(.horizontal, showsIndicators: false) {
                        Text(block.text).font(.system(.footnote, design: .monospaced)).textSelection(.enabled).padding(8)
                    }
                    .background(Color(UIColor.secondarySystemBackground), in: RoundedRectangle(cornerRadius: 8))
                case .quote:
                    HStack(spacing: 8) {
                        RoundedRectangle(cornerRadius: 1).fill(Color.secondary.opacity(0.4)).frame(width: 2)
                        Text(FeedMarkdown.inline(block.text)).font(.callout).foregroundStyle(.secondary)
                    }
                case .rule:
                    Divider()
                }
            }
        }
    }
}

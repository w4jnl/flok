import Foundation
import Testing
@testable import Flok

struct ScreenLinesTests {
    @Test func sortsLinesByWhatTheyAre() {
        #expect(ScreenLines.kind(of: "So the two most recent trading days were fetched from the feed.") == .prose)
        #expect(ScreenLines.kind(of: "") == .prose)
        #expect(ScreenLines.kind(of: String(repeating: "─", count: 120)) == .rule)
        #expect(ScreenLines.kind(of: "──────── trading-journal (2) " + String(repeating: "─", count: 90)) == .rule)
        #expect(ScreenLines.kind(of: "──────── trading-journal (2) ─") == .prose) // short: nothing to cut
        #expect(ScreenLines.kind(of: "=========================") == .rule)
        #expect(ScreenLines.kind(of: "   ----") == .rule)
        // the thick edges of a TUI's box, block elements, are rules too, and are cut like one
        #expect(ScreenLines.kind(of: String(repeating: "▀", count: 116)) == .rule)
        #expect(ScreenLines.kind(of: "│" + String(repeating: "▁", count: 116)) == .rule)
        #expect(ScreenLines.fit(String(repeating: "▀", count: 116), columns: 49).count == 49)
        #expect(ScreenLines.kind(of: "│ ES=F day source, Oct 13 and earlier │ yahoo │") == .boxed)
        #expect(ScreenLines.kind(of: "├──────────────┼───────┤") == .boxed)
        #expect(ScreenLines.kind(of: "╭──────────────────────╮") == .boxed)
        #expect(ScreenLines.kind(of: "| name | value |") == .boxed)
        #expect(ScreenLines.kind(of: "+------+-------+") == .boxed)
        // one gutter bar or one pipe is still text, and so is a dash in it
        #expect(ScreenLines.kind(of: "│ a quoted line of output that should wrap like any other") == .prose)
        #expect(ScreenLines.kind(of: "cat relay.go | grep deflate") == .prose)
        #expect(ScreenLines.kind(of: "--- servers panel ---") == .prose)
        #expect(ScreenLines.kind(of: "x = a + b + c") == .prose)
    }

    @Test func cutsARuleOutOfItsLongestRun() {
        let bare = String(repeating: "─", count: 150)
        #expect(ScreenLines.fit(bare, columns: 40).count == 40)
        let labelled = "──────── trading-journal (2) " + String(repeating: "─", count: 100)
        let cut = ScreenLines.fit(labelled, columns: 40)
        #expect(cut.count == 40 && cut.hasPrefix("──────── trading-journal (2) ─"))
        #expect(ScreenLines.fit("── short ──", columns: 40) == "── short ──")
        // a label wider than the screen is cut at the end
        #expect(ScreenLines.fit("── a label that is far too wide for the phone to show at all ──", columns: 20).count == 20)
    }

    @Test func dropsTheRightEdgeOfABoxWiderThanThePhone() {
        let pad = String(repeating: " ", count: 60)
        #expect(ScreenLines.unedged("  ● The tag is set in main.yml, line 9:" + pad + "│") == "  ● The tag is set in main.yml, line 9:")
        #expect(ScreenLines.unedged("❯ where is the image param?" + pad + "12:08 │") == "❯ where is the image param?" + pad + "12:08")
        #expect(ScreenLines.unedged("│") == "" && ScreenLines.unedged("   │  ") == "")
        #expect(ScreenLines.unedged(" " + String(repeating: "▀", count: 110) + " │") == " " + String(repeating: "▀", count: 110))
        // a gutter, a bar glued to text, a table row and plain text keep every character
        #expect(ScreenLines.unedged("│ ● Task complete") == "│ ● Task complete")
        #expect(ScreenLines.unedged("text│") == "text│")
        #expect(ScreenLines.unedged("│ a │ b │") == "│ a │ b │")
        #expect(ScreenLines.unedged("plain text   ") == "plain text")
        // the blocks and the widest line see the unedged lines
        let blocks = ScreenLines.blocks(of: "a line" + pad + "│\n│\n" + String(repeating: "▁", count: 110) + " │")
        #expect(blocks.map(\.kind) == [.prose, .rule])
        #expect(blocks[0].lines == ["a line", ""])
        #expect(ScreenLines.widest(of: "a line" + pad + "│\n│") == "a line".count)
    }

    @Test func groupsConsecutiveLinesOfAKind() {
        let screen = """
        Some prose here
        that goes on
        ┌────┬────┐
        │ a  │ b  │
        └────┴────┘
        ────────────────────
        ❯
        """
        let blocks = ScreenLines.blocks(of: screen)
        #expect(blocks.map(\.kind) == [.prose, .boxed, .rule, .prose])
        #expect(blocks[1].lines.count == 3 && blocks[1].id == 2)
        #expect(blocks[3].text == "❯")
        // the widest line of text leaves the rule out
        #expect(ScreenLines.widest(of: screen) == "Some prose here".count)
    }
}

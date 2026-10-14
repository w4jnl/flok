import Foundation
import Testing
import UIKit
@testable import Flok

struct AttachTests {
    @Test func shrinksAnImageToJPEG() throws {
        let big = UIGraphicsImageRenderer(size: CGSize(width: 4000, height: 3000)).image { ctx in
            UIColor.red.setFill()
            ctx.fill(CGRect(x: 0, y: 0, width: 4000, height: 3000))
            UIColor.blue.setFill()
            ctx.fill(CGRect(x: 100, y: 100, width: 1800, height: 1200))
        }
        let f = try #require(AttachedFile.image(big, name: "Screenshot 2026-10-14.png"))
        #expect(f.name == "Screenshot 2026-10-14.jpg" && f.mime == "image/jpeg")
        let back = try #require(UIImage(data: f.data))
        #expect(max(back.size.width, back.size.height) <= AttachedFile.maxSide)
        #expect(f.data.count < 1 << 20)
        #expect(f.sizeLabel.hasSuffix("KB"))
        #expect(AttachedFile.stem("") == "image" && AttachedFile.stem("a.b.c.png") == "a.b.c")
        #expect(AttachedFile.pdf(Data(), name: "x") == nil)
        #expect(AttachedFile.pdf(Data([1, 2]), name: "notes.PDF")?.name == "notes.pdf")
    }

    @Test func encodesTheFileAsBase64() throws {
        let body = AnswerBody(instance: "home", pane: "%1", text: "see", keys: ["Enter"], file: AttachedFile(name: "x.jpg", mime: "image/jpeg", data: Data([1, 2, 3])))
        let json = String(decoding: try Wire.encoder.encode(body), as: UTF8.self)
        #expect(json.contains(#""data":"AQID""#))
        #expect(json.contains(#""name":"x.jpg""#))
        #expect(json.contains("image\\/jpeg") || json.contains("image/jpeg"))
    }

    @Test func readsTheInstanceFeatures() throws {
        let with = try Wire.decoder.decode(InstanceState.self, from: Data(#"{"name":"Home","online":true,"since":"2026-10-14T08:00:00Z","features":["answer","screen","long-text","files"]}"#.utf8))
        #expect(with.takesFiles && with.takesLongText)
        let old = try Wire.decoder.decode(InstanceState.self, from: Data(#"{"name":"Home","online":true,"since":"2026-10-14T08:00:00Z"}"#.utf8))
        #expect(!old.takesFiles && !old.takesLongText)
    }
}

struct VocabularyTests {
    @Test func picksIdentifiersOffTheScreen() {
        let screen = """
        ● Read internal/ui/link.go
          ⎿  Read 345 lines
        ✻ Thinking about the TypeAnswer path and flok-relay… (12s · ↓ 1.2k tokens)
        Do you want to run make build? The answer is yes or no.
        $ go test ./internal/answer -run TestLongText --count=1
        │ flok-relay │ snapshot_json │ https://example.net/x │
        """
        let words = Vocabulary.build(instances: [], screen: screen)
        for want in ["internal/ui/link.go", "TypeAnswer", "flok-relay", "./internal/answer", "TestLongText", "--count=1", "snapshot_json"] {
            #expect(words.contains(want), "missing \(want) in \(words)")
        }
        for bad in ["yes", "Thinking", "345", "Read", "https://example.net/x", "12s", "1.2k"] {
            #expect(!words.contains(bad), "\(bad) is not an identifier")
        }
        #expect(words.firstIndex(of: "flok-relay")! < words.firstIndex(of: "TypeAnswer")!, "the most frequent first")
    }

    @Test func namesComeFirstAndOnce() throws {
        let json = """
        {"name":"home","online":true,"since":"2026-10-14T08:00:00Z","snap":{"agents":[
          {"pane_id":"%3","session_name":"Hugo","name":"flok","kind":"claude","state":"blocked","unseen":1},
          {"pane_id":"%1","host":"beta","session_name":"Remote","name":"docs","kind":"claude","state":"done","unseen":0}]}}
        """
        let inst = try Wire.decoder.decode(InstanceState.self, from: Data(json.utf8))
        let words = Vocabulary.build(instances: [inst], screen: "editing docs/phone.md for FLOK and flok", limit: 6)
        #expect(Array(words.prefix(5)) == ["home", "flok", "Hugo", "docs", "beta"])
        #expect(words.count == 6 && words.last == "Remote", "the limit holds and names are unique regardless of case: \(words)")
    }

    @Test func looksLikeCode() {
        for yes in ["relay.go", "flok-relay", "a/b", "m11", "camelCase", "TypeAnswer", "--no-focus", "-v", "snapshot_json", "p256"] {
            #expect(Vocabulary.looksLikeCode(yes), "\(yes)")
        }
        for no in ["hello", "Hello", "12.5", "ok", "DONE", "the", "a-", "x", "https://a.b"] {
            #expect(!Vocabulary.looksLikeCode(no), "\(no)")
        }
    }
}

struct CleanupTests {
    @Test func acceptsOnlyACleanedVersion() {
        let raw = "um so please fix the relay dot go file and run make build"
        #expect(Cleanup.accept("Please fix the relay.go file and run make build.", raw: raw) == "Please fix the relay.go file and run make build.")
        #expect(Cleanup.accept("\"Please fix relay.go and run make build.\"", raw: raw) == "Please fix relay.go and run make build.")
        #expect(Cleanup.accept("Cleaned: Fix relay.go, run make build.", raw: raw) == "Fix relay.go, run make build.")
        #expect(Cleanup.accept("", raw: raw) == raw)
        #expect(Cleanup.accept("Sure! Here is the cleaned message, with some notes on what I changed and why it reads better now that the filler words are gone and the names are code: " + raw, raw: raw) == raw)
        #expect(Cleanup.accept("ok", raw: raw) == raw, "far too short to be the same message")
    }

    @Test func instructionsCarryTheVocabulary() {
        let s = Cleanup.instructions(vocabulary: ["relay.go", "flok-relay"])
        #expect(s.contains("relay.go, flok-relay") && s.contains("Reply with the cleaned message only"))
        #expect(!Cleanup.instructions(vocabulary: []).contains("spelled exactly"))
        #expect(Cleanup.prompt("hi there") == "Dictated: hi there")
    }
}

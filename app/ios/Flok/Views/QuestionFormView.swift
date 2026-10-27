// The agent's question form (Claude Code's AskUserQuestion) as the phone fills it in: a section
// per question with its options as rows to tick (several) or pick (one), a row for words of
// your own, and Submit. The choices go to the instance as a FormAnswerBody, and the instance
// presses them into the agent's own form key by key, checking the screen after each; what it
// could not do comes back as an error, and the terminal view behind is where to finish.
import SwiftUI

/// What the user has filled in so far and what it sends: pure, so the rules are testable.
struct FormDraft: Equatable {
    var form: QuestionForm
    var selected: [Set<Int>]
    var other: [String]

    /// The longest words the instance takes for one question (answer.MaxFormOther).
    static let maxOther = 200

    init(form: QuestionForm) {
        self.form = form
        selected = Array(repeating: [], count: form.questions.count)
        other = Array(repeating: "", count: form.questions.count)
    }

    /// Ticks or unticks option j of question i on a multi-select; on a single-select picks it
    /// alone (or unpicks it), dropping any words, since the form takes one or the other.
    mutating func tap(_ i: Int, _ j: Int) {
        if form.questions[i].isMulti {
            if selected[i].contains(j) { selected[i].remove(j) } else { selected[i].insert(j) }
        } else {
            selected[i] = selected[i].contains(j) ? [] : [j]
            if !selected[i].isEmpty { other[i] = "" }
        }
    }

    /// Words of the user's own for question i; on a single-select they replace a pick.
    mutating func setOther(_ i: Int, _ text: String) {
        other[i] = text
        if !form.questions[i].isMulti, !trimmedOther(i).isEmpty { selected[i] = [] }
    }

    func trimmedOther(_ i: Int) -> String { other[i].trimmingCharacters(in: .whitespacesAndNewlines) }
    func answered(_ i: Int) -> Bool { !selected[i].isEmpty || !trimmedOther(i).isEmpty }
    /// Every question answered: the form will not warn of one left open.
    var complete: Bool { form.questions.indices.allSatisfy(answered) }

    var body: FormAnswerBody {
        FormAnswerBody(answers: form.questions.indices.map { i in
            let words = trimmedOther(i)
            return FormChoice(selected: selected[i].isEmpty ? nil : selected[i].sorted(),
                              other: words.isEmpty ? nil : String(words.prefix(Self.maxOther)))
        })
    }
}

struct QuestionFormView: View {
    let agentName: String
    /// Sends the answer; the message to show when the instance could not type it, nil when sent.
    let onSubmit: @MainActor (FormAnswerBody) async -> String?
    @State private var draft: FormDraft
    @State private var sending = false
    @State private var failure: String?
    @Environment(\.dismiss) private var dismiss

    init(agentName: String, form: QuestionForm, onSubmit: @escaping @MainActor (FormAnswerBody) async -> String?) {
        self.agentName = agentName
        self.onSubmit = onSubmit
        _draft = State(initialValue: FormDraft(form: form))
    }

    var body: some View {
        NavigationStack {
            Form {
                ForEach(Array(draft.form.questions.enumerated()), id: \.offset) { i, q in
                    Section {
                        Text(q.text)
                            .font(.subheadline)
                            .listRowBackground(Color.clear)
                        ForEach(Array(q.options.enumerated()), id: \.offset) { j, o in
                            optionRow(i, j, o, multi: q.isMulti)
                        }
                        TextField("Type something", text: Binding(get: { draft.other[i] }, set: { draft.setOther(i, $0) }))
                            .autocorrectionDisabled()
                            .textInputAutocapitalization(.never)
                            .submitLabel(.done)
                    } header: {
                        Text("\(q.header ?? "Question \(i + 1)") · \(q.isMulti ? "pick any" : "pick one")")
                    }
                }
                if let failure {
                    Section {
                        Text(failure).font(.footnote).foregroundStyle(Palette.orange)
                        Text("The form on the agent's screen is as far as it got; the keys under the screen finish it.")
                            .font(.footnote).foregroundStyle(.secondary)
                    }
                }
            }
            .navigationTitle(agentName)
            .navigationBarTitleDisplayMode(.inline)
            .toolbar {
                ToolbarItem(placement: .cancellationAction) { Button("Cancel") { dismiss() } }
                ToolbarItem(placement: .confirmationAction) {
                    Button { Task { await submit() } } label: {
                        if sending { ProgressView() } else { Text("Submit") }
                    }
                    .disabled(!draft.complete || sending)
                }
            }
            .interactiveDismissDisabled(sending)
        }
    }

    @ViewBuilder private func optionRow(_ i: Int, _ j: Int, _ o: FormOption, multi: Bool) -> some View {
        let on = draft.selected[i].contains(j)
        Button { draft.tap(i, j) } label: {
            HStack(alignment: .firstTextBaseline, spacing: 10) {
                Image(systemName: multi ? (on ? "checkmark.square.fill" : "square") : (on ? "largecircle.fill.circle" : "circle"))
                    .foregroundStyle(on ? Color.accentColor : Color.secondary)
                VStack(alignment: .leading, spacing: 2) {
                    Text(o.label)
                    if let d = o.description, !d.isEmpty {
                        Text(d).font(.footnote).foregroundStyle(.secondary)
                    }
                }
                Spacer(minLength: 0)
            }
            .contentShape(Rectangle())
        }
        .buttonStyle(.plain)
        .accessibilityAddTraits(on ? .isSelected : [])
    }

    private func submit() async {
        sending = true
        failure = await onSubmit(draft.body)
        sending = false
        if failure == nil { dismiss() }
    }
}

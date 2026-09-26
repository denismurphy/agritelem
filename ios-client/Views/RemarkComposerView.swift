import Domain
import SwiftUI

/// Records an observation about an animal. Saving works offline; the remark
/// uploads when the device is next online.
struct RemarkComposerView: View {
    let animal: AnimalID
    let animalName: String?

    @Environment(AppModel.self) private var app
    @Environment(\.dismiss) private var dismiss
    @State private var model = RemarkComposerModel()
    @State private var savedCount = 0
    @FocusState private var noteFocused: Bool

    var body: some View {
        NavigationStack {
            Form {
                Section {
                    TextField("What did you observe?", text: $model.note, axis: .vertical)
                        .lineLimit(4...10)
                        .focused($noteFocused)
                } footer: {
                    NoteCounter(remaining: model.remainingCharacters)
                }

                Section("Category") {
                    Picker("Category", selection: $model.category) {
                        ForEach(RemarkCategory.allCases, id: \.self) { category in
                            Label {
                                Text(category.title)
                            } icon: {
                                Image(systemName: category.systemImage)
                                    .foregroundStyle(category.tint)
                            }
                            .tag(category)
                        }
                    }
                    .pickerStyle(.inline)
                    .labelsHidden()
                }

                Section {
                    DatePicker("Observed", selection: $model.eventDate, in: ...Date.now)
                } footer: {
                    if app.claims == nil {
                        Label("Sign in under Settings to record remarks.", systemImage: "person.crop.circle.badge.exclamationmark")
                            .foregroundStyle(.orange)
                    } else if !app.isOnline {
                        Label("You're offline. The remark will upload automatically.", systemImage: "icloud.slash")
                    }
                }

                if let error = model.errorMessage {
                    Section {
                        Label(error, systemImage: "exclamationmark.triangle.fill")
                            .foregroundStyle(.red)
                    }
                }
            }
            .navigationTitle(animalName.map { "Remark on \($0)" } ?? String(localized: "New Remark"))
            .navigationBarTitleDisplayMode(.inline)
            .toolbar {
                ToolbarItem(placement: .cancellationAction) {
                    Button("Cancel", role: .cancel) { dismiss() }
                }
                ToolbarItem(placement: .confirmationAction) {
                    Button("Save", systemImage: "checkmark") {
                        Task { await save() }
                    }
                    .disabled(!model.canSave || app.claims == nil)
                }
            }
            .sensoryFeedback(.success, trigger: savedCount)
            .interactiveDismissDisabled(!model.trimmedNote.isEmpty)
            .onAppear { noteFocused = true }
        }
    }

    private func save() async {
        let saved = await model.submit { note, category, date in
            try await app.addRemark(animal: animal, note: note, category: category, eventDate: date)
        }
        if saved {
            savedCount += 1
            dismiss()
        }
    }
}

private struct NoteCounter: View {
    let remaining: Int

    var body: some View {
        HStack {
            Spacer()
            Text("\(remaining) characters left")
                .monospacedDigit()
                .foregroundStyle(remaining < 0 ? .red : remaining < 100 ? .orange : .secondary)
        }
    }
}

#if DEBUG
#Preview("Composer") {
    RemarkComposerView(animal: DemoData.id(0), animalName: "Bessie")
        .environment(AppModel.demo())
}
#endif

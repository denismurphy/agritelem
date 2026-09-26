import Domain
import Foundation
import Observation

@MainActor
@Observable
final class RemarkComposerModel {
    /// The server's limit on note length, in characters.
    static let maxNoteLength = 2000

    var note = ""
    var category: RemarkCategory = .general
    var eventDate = Date.now
    private(set) var isSaving = false
    private(set) var errorMessage: String?

    var trimmedNote: String { note.trimmingCharacters(in: .whitespacesAndNewlines) }
    /// Counted in Unicode scalars, as the server counts them (Go runes), not
    /// grapheme clusters: "👍🏽" is one character on screen but two here.
    var remainingCharacters: Int { Self.maxNoteLength - trimmedNote.unicodeScalars.count }
    var canSave: Bool { !trimmedNote.isEmpty && remainingCharacters >= 0 && !isSaving }

    /// Saves through `save`, returning true on success.
    func submit(_ save: (String, RemarkCategory, Date) async throws -> Void) async -> Bool {
        guard canSave else { return false }
        isSaving = true
        defer { isSaving = false }
        do {
            try await save(trimmedNote, category, min(eventDate, .now))
            errorMessage = nil
            return true
        } catch {
            errorMessage = error.localizedDescription
            return false
        }
    }
}

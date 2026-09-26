import SwiftUI

/// Toolbar control showing synchronisation state; tapping syncs now.
struct SyncStatusButton: View {
    @Environment(AppModel.self) private var app

    var body: some View {
        Button {
            app.syncSoon()
        } label: {
            Image(systemName: symbol)
                .symbolRenderingMode(.hierarchical)
                .symbolEffect(.rotate, options: .repeating, isActive: app.syncStatus == .syncing)
                .contentTransition(.symbolEffect(.replace))
                .overlay(alignment: .topTrailing) {
                    if app.pendingUploads > 0 {
                        Text(app.pendingUploads, format: .number)
                            .font(.caption2.weight(.bold))
                            .monospacedDigit()
                            .foregroundStyle(.white)
                            .padding(.horizontal, 5)
                            .background(.orange, in: .capsule)
                            .offset(x: 9, y: -7)
                    }
                }
        }
        .accessibilityLabel(Text(label))
        .accessibilityHint(Text("Syncs now"))
    }

    private var symbol: String {
        switch app.syncStatus {
        case .idle: "checkmark.icloud"
        case .syncing: "arrow.triangle.2.circlepath"
        case .offline: "icloud.slash"
        case .needsSignIn: "person.crop.circle.badge.exclamationmark"
        case .failed: "exclamationmark.icloud"
        }
    }

    private var label: LocalizedStringResource {
        switch app.syncStatus {
        case .idle: app.pendingUploads == 0 ? "Up to date" : "\(app.pendingUploads) changes waiting to upload"
        case .syncing: "Syncing"
        case .offline: "Offline; changes will upload later"
        case .needsSignIn: "Sign in required"
        case .failed: "Sync failed"
        }
    }
}

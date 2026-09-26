import Persistence
import SwiftUI

struct SettingsView: View {
    @Environment(AppModel.self) private var app

    var body: some View {
        NavigationStack {
            Form {
                if let storageError = app.storageError {
                    Section {
                        Label("Changes won't be saved on this device: \(storageError)", systemImage: "externaldrive.badge.exclamationmark")
                            .foregroundStyle(.red)
                    }
                }
                AccountSection()
                SyncSection()
                if !app.failedRemarks.isEmpty {
                    NeedsAttentionSection(remarks: app.failedRemarks)
                }
                ServerSection(current: app.serverURL)
                AboutSection()
            }
            .navigationTitle("Settings")
        }
    }
}

private struct AccountSection: View {
    @Environment(AppModel.self) private var app
    @State private var token = ""
    @State private var error: String?

    var body: some View {
        Section {
            if let claims = app.claims {
                LabeledContent {
                    Text(claims.subject).monospaced()
                } label: {
                    Label("Signed in as", systemImage: "person.crop.circle.fill")
                }
                if let expires = claims.expires {
                    LabeledContent("Token expires") {
                        Text(expires, format: .relative(presentation: .named))
                            .foregroundStyle(expires < .now ? .red : .secondary)
                    }
                }
                Button("Sign Out", systemImage: "rectangle.portrait.and.arrow.right", role: .destructive) {
                    app.signOut()
                }
            } else {
                SecureField("Access token", text: $token)
                    .textContentType(.password)
                    .autocorrectionDisabled()
                    .textInputAutocapitalization(.never)
                Button("Sign In", systemImage: "key.fill") {
                    do {
                        try app.signIn(token: token)
                        token = ""
                        error = nil
                    } catch {
                        self.error = error.localizedDescription
                    }
                }
                .disabled(token.isEmpty)
            }
        } header: {
            Text("Account")
        } footer: {
            if let error {
                Text(error).foregroundStyle(.red)
            } else if app.claims == nil {
                Text("Paste a token from `make token`. It is stored in the Keychain on this device only.")
            }
        }
    }
}

private struct SyncSection: View {
    @Environment(AppModel.self) private var app

    var body: some View {
        Section("Sync") {
            LabeledContent {
                Text(statusText)
            } label: {
                Label("Status", systemImage: "arrow.triangle.2.circlepath.icloud")
            }
            LabeledContent("Waiting to upload") {
                Text(app.pendingUploads, format: .number).monospacedDigit()
            }
            if let lastSynced = app.lastSynced {
                LabeledContent("Last synced") {
                    Text(lastSynced, format: .relative(presentation: .named))
                }
            }
            Button("Sync Now", systemImage: "arrow.clockwise") {
                app.syncSoon()
            }
            .disabled(app.syncStatus == .syncing)
        }
    }

    private var statusText: LocalizedStringResource {
        switch app.syncStatus {
        case .idle: "Up to date"
        case .syncing: "Syncing…"
        case .offline: "Offline"
        case .needsSignIn: "Sign in required"
        case let .failed(reason): "Failed: \(reason)"
        }
    }
}

/// Remarks the server refused. They never reach the herd record until the
/// user retries or discards them, so they are shown prominently.
private struct NeedsAttentionSection: View {
    @Environment(AppModel.self) private var app
    let remarks: [LocalRemark]

    var body: some View {
        Section {
            ForEach(remarks) { remark in
                VStack(alignment: .leading, spacing: 4) {
                    Text(remark.remark.note).font(.subheadline)
                    if let error = remark.lastError {
                        Text(error).font(.caption).foregroundStyle(.red)
                    }
                    Text(remark.remark.animal.displayID).font(.caption2).monospaced().foregroundStyle(.secondary)
                }
                .swipeActions(edge: .trailing) {
                    Button("Discard", systemImage: "trash", role: .destructive) {
                        Task { await app.discard(remark) }
                    }
                }
                .swipeActions(edge: .leading) {
                    Button("Retry", systemImage: "arrow.clockwise") {
                        Task { await app.retry(remark) }
                    }
                    .tint(.blue)
                }
            }
        } header: {
            Label("Needs Attention", systemImage: "exclamationmark.triangle.fill")
                .foregroundStyle(.red)
        } footer: {
            Text("The server rejected these remarks. Swipe right to retry or left to discard.")
        }
    }
}

private struct ServerSection: View {
    @Environment(AppModel.self) private var app
    let current: URL
    @State private var draft = ""

    var body: some View {
        Section("Server") {
            TextField("Server URL", text: $draft)
                .keyboardType(.URL)
                .textContentType(.URL)
                .autocorrectionDisabled()
                .textInputAutocapitalization(.never)
                .onSubmit(apply)
            if URL(string: draft) != current {
                Button("Use This Server", systemImage: "server.rack", action: apply)
                    .disabled(URL(string: draft)?.scheme?.hasPrefix("http") != true)
            }
        }
        .onAppear { draft = current.absoluteString }
    }

    private func apply() {
        if let url = URL(string: draft), url.scheme?.hasPrefix("http") == true {
            app.setServerURL(url)
        }
    }
}

private struct AboutSection: View {
    var body: some View {
        Section("About") {
            LabeledContent("Data standard", value: "ICAR ADE 1.5.1")
            LabeledContent("Version", value: Bundle.main.infoDictionary?["CFBundleShortVersionString"] as? String ?? "–")
        }
    }
}

#if DEBUG
#Preview("Settings") {
    SettingsView()
        .environment(AppModel.demo())
}
#endif

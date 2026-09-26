import SwiftUI

struct RootView: View {
    @Environment(AppModel.self) private var app

    var body: some View {
        TabView {
            Tab("Herd", systemImage: "square.grid.2x2.fill") {
                // Rebuild the dashboard's model if the server changes.
                HerdDashboardView(app: app)
                    .id(app.serverURL)
            }
            Tab("Settings", systemImage: "gearshape.fill") {
                SettingsView()
            }
            .badge(app.failedRemarks.count)
        }
    }
}

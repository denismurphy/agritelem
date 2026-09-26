import SwiftUI

@main
struct AgritelemApp: App {
    @State private var model = Self.makeModel()
    @Environment(\.scenePhase) private var scenePhase

    private static func makeModel() -> AppModel {
        #if DEBUG
        // UI tests and demos run against built-in sample data.
        if ProcessInfo.processInfo.arguments.contains("-AgritelemDemo") {
            return .demo()
        }
        #endif
        return .live()
    }

    var body: some Scene {
        WindowGroup {
            RootView()
                .environment(model)
                .task { await model.start() }
                .onChange(of: scenePhase) { _, phase in
                    if phase == .active {
                        model.syncSoon()
                    }
                }
        }
    }
}

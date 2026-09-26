import Domain
import SwiftUI

struct HerdDashboardView: View {
    let app: AppModel
    @State private var model: HerdDashboardModel

    init(app: AppModel) {
        self.app = app
        self.model = HerdDashboardModel(api: app.api, database: app.database)
    }

    var body: some View {
        NavigationStack {
            HerdList(model: model)
                .navigationTitle("Herd")
                .searchable(text: $model.searchText, prompt: Text("Name or tag"))
                .toolbar {
                    ToolbarItem(placement: .topBarLeading) {
                        StatusFilterMenu(filter: $model.filter)
                    }
                    ToolbarItem(placement: .topBarTrailing) {
                        SyncStatusButton()
                    }
                }
                .refreshable {
                    await app.syncNow()
                    await model.refresh()
                }
                .navigationDestination(for: AnimalID.self) { id in
                    AnimalDetailView(animalID: id, app: app)
                }
                .task { await model.load() }
                .task { await model.observeAnimals() }
        }
    }
}

/// The dashboard's scrolling content: KPIs, animals needing attention, and
/// the filtered herd.
private struct HerdList: View {
    let model: HerdDashboardModel

    var body: some View {
        List {
            if let overview = model.overview {
                Section {
                    KPIGrid(overview: overview)
                } footer: {
                    OverviewFootnote(day: overview.day, fetchedAt: model.fetchedAt, isCached: model.isShowingCachedData)
                }
                .listRowInsets(EdgeInsets())
                .listRowBackground(Color.clear)

                if !model.attention.isEmpty, model.filter == .all, model.searchText.isEmpty {
                    Section("Needs Attention") {
                        AttentionCarousel(rows: model.attention)
                            .listRowInsets(EdgeInsets())
                            .listRowBackground(Color.clear)
                    }
                }

                Section(model.filter == .all ? "All Animals" : "Filtered") {
                    ForEach(model.rows) { row in
                        NavigationLink(value: row.id) {
                            HerdRowView(row: row)
                        }
                    }
                }
            }
        }
        .listStyle(.insetGrouped)
        .scrollContentBackground(.hidden)
        .background(Color(.systemGroupedBackground))
        .overlay { HerdPlaceholder(phase: model.phase, hasData: model.overview != nil, isFilteredEmpty: model.rows.isEmpty) }
        .animation(.default, value: model.rows)
    }
}

private struct HerdPlaceholder: View {
    let phase: LoadPhase
    let hasData: Bool
    let isFilteredEmpty: Bool

    var body: some View {
        switch phase {
        case .loading where !hasData:
            ProgressView("Loading herd…")
        case let .failed(message) where !hasData:
            ContentUnavailableView {
                Label("Herd Unavailable", systemImage: "wifi.exclamationmark")
            } description: {
                Text(message)
            }
        default:
            if hasData, isFilteredEmpty {
                ContentUnavailableView.search
            }
        }
    }
}

private struct OverviewFootnote: View {
    let day: Date
    let fetchedAt: Date?
    let isCached: Bool

    var body: some View {
        VStack(alignment: .leading, spacing: 2) {
            Text("Telemetry for \(day, format: Date.FormatStyle(date: .complete, time: .omitted, timeZone: .gmt)) (UTC)")
            if isCached, let fetchedAt {
                Label {
                    Text("Offline — showing data from \(fetchedAt, format: .relative(presentation: .named))")
                } icon: {
                    Image(systemName: "icloud.slash")
                }
                .foregroundStyle(.orange)
            }
        }
        .font(.footnote)
        .padding(.horizontal)
    }
}

#if DEBUG
#Preview("Dashboard") {
    let app = AppModel.demo()
    HerdDashboardView(app: app)
        .environment(app)
        .task { await app.start() }
}
#endif

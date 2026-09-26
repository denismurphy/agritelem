import Domain
import Persistence
import SwiftUI

struct AnimalDetailView: View {
    @Environment(AppModel.self) private var app
    @State private var model: AnimalDetailModel
    @State private var isComposing = false

    init(animalID: AnimalID, app: AppModel) {
        self.model = AnimalDetailModel(animalID: animalID, api: app.api, database: app.database)
    }

    var body: some View {
        ScrollView {
            VStack(spacing: 16) {
                AnimalHeader(
                    id: model.animalID,
                    name: model.animal?.name,
                    tag: model.animal?.managementTag,
                    breedCode: model.animal?.breedCode,
                    birthDate: model.animal?.birthDate,
                    flags: model.latestFlags
                )
                if model.isShowingCachedData {
                    CachedDataBanner()
                }
                TelemetryCard(model: model)
                if !model.devices.isEmpty {
                    DevicesCard(devices: model.devices)
                }
                RemarksCard(remarks: model.remarks) { isComposing = true }
            }
            .padding()
        }
        .background(Color(.systemGroupedBackground))
        .navigationTitle(model.animal?.name ?? model.animalID.displayID)
        .navigationBarTitleDisplayMode(.inline)
        .toolbar {
            ToolbarItem(placement: .primaryAction) {
                Button("Add Remark", systemImage: "square.and.pencil") { isComposing = true }
            }
        }
        .sheet(isPresented: $isComposing) {
            RemarkComposerView(animal: model.animalID, animalName: model.animal?.name)
                .presentationDetents([.medium, .large])
        }
        .task { await model.load() }
        .task { await model.observeAnimal() }
        .task { await model.observeRemarks() }
    }
}

private struct CachedDataBanner: View {
    var body: some View {
        Label("Offline — showing the last downloaded telemetry", systemImage: "icloud.slash")
            .font(.footnote.weight(.medium))
            .foregroundStyle(.orange)
            .frame(maxWidth: .infinity, alignment: .leading)
            .padding(12)
            .background(.orange.opacity(0.12), in: .rect(cornerRadius: 14, style: .continuous))
    }
}

/// Card chrome shared by the detail sections.
struct DetailCard<Content: View>: View {
    let title: LocalizedStringResource
    let systemImage: String
    @ViewBuilder var content: Content

    var body: some View {
        VStack(alignment: .leading, spacing: 14) {
            Label {
                Text(title)
            } icon: {
                Image(systemName: systemImage)
            }
            .font(.headline)
            content
        }
        .padding(16)
        .frame(maxWidth: .infinity, alignment: .leading)
        .background(Color(.secondarySystemGroupedBackground), in: .rect(cornerRadius: 22, style: .continuous))
    }
}

private struct AnimalHeader: View {
    let id: AnimalID
    let name: String?
    let tag: String?
    let breedCode: String?
    let birthDate: Date?
    let flags: Set<TelemetryFlag>

    private var statuses: [AnimalStatus] {
        var result: [AnimalStatus] = []
        if flags.contains(.heatSignature) { result.append(.inHeat) }
        if flags.contains(.thermalStress) { result.append(.heatStress) }
        return result.isEmpty ? [.normal] : result
    }

    var body: some View {
        VStack(spacing: 12) {
            TagBadge(tag: tag ?? "–", tint: statuses[0].tint, size: 76)
            VStack(spacing: 4) {
                Text(name ?? id.displayID)
                    .font(.system(.title, design: .rounded, weight: .bold))
                HStack(spacing: 6) {
                    if let breed = Breed.name(for: breedCode) {
                        Text(breed)
                    }
                    if let birthDate {
                        Text(verbatim: "·")
                        Text("Born \(birthDate, format: .dateTime.month(.abbreviated).year())")
                    }
                }
                .font(.subheadline)
                .foregroundStyle(.secondary)
                Label {
                    Text(id.displayID).monospacedDigit()
                } icon: {
                    Image(systemName: "barcode")
                }
                .font(.caption)
                .foregroundStyle(.tertiary)
                .accessibilityLabel(Text("ISO 11784 identifier \(id.id)"))
            }
            HStack(spacing: 8) {
                ForEach(statuses, id: \.self) { status in
                    Label {
                        Text(status.title)
                    } icon: {
                        Image(systemName: status.systemImage)
                    }
                    .font(.subheadline.weight(.semibold))
                    .padding(.horizontal, 12)
                    .padding(.vertical, 6)
                    .foregroundStyle(status.tint)
                    .background(status.tint.opacity(0.15), in: .capsule)
                }
            }
        }
        .frame(maxWidth: .infinity)
        .padding(.vertical, 8)
    }
}

private struct DevicesCard: View {
    let devices: [Device]

    var body: some View {
        DetailCard(title: "Sensors", systemImage: "dot.radiowaves.left.and.right") {
            ForEach(devices) { device in
                HStack {
                    Image(systemName: "capsule.fill")
                        .foregroundStyle(.teal.gradient)
                        .rotationEffect(.degrees(90))
                    VStack(alignment: .leading) {
                        Text(device.name ?? device.id).font(.subheadline.weight(.semibold))
                        if let serial = device.serial {
                            Text(serial).font(.caption).monospaced().foregroundStyle(.secondary)
                        }
                    }
                    Spacer()
                    if let since = device.assignedSince {
                        Text("Since \(since, format: .dateTime.day().month(.abbreviated).year())")
                            .font(.caption)
                            .foregroundStyle(.secondary)
                    }
                }
                .accessibilityElement(children: .combine)
            }
        }
    }
}

private struct RemarksCard: View {
    let remarks: [LocalRemark]
    let onAdd: () -> Void

    var body: some View {
        DetailCard(title: "Remarks", systemImage: "text.bubble.fill") {
            if remarks.isEmpty {
                Button(action: onAdd) {
                    Label("Record the first observation", systemImage: "plus.circle.fill")
                        .frame(maxWidth: .infinity)
                }
                .buttonStyle(.bordered)
                .controlSize(.large)
            } else {
                VStack(alignment: .leading, spacing: 14) {
                    ForEach(remarks) { remark in
                        RemarkRow(
                            note: remark.remark.note,
                            category: remark.remark.category,
                            eventDate: remark.remark.eventDate,
                            state: remark.state,
                            error: remark.lastError
                        )
                    }
                }
            }
        }
    }
}

private struct RemarkRow: View {
    let note: String
    let category: RemarkCategory
    let eventDate: Date
    let state: RemarkSyncState
    let error: String?

    var body: some View {
        HStack(alignment: .top, spacing: 12) {
            Image(systemName: category.systemImage)
                .font(.subheadline)
                .foregroundStyle(.white)
                .frame(width: 32, height: 32)
                .background(category.tint.gradient, in: .circle)
            VStack(alignment: .leading, spacing: 4) {
                Text(note)
                    .font(.subheadline)
                HStack(spacing: 6) {
                    Text(category.title)
                    Text(verbatim: "·")
                    Text(eventDate, format: .relative(presentation: .named))
                    Spacer()
                    SyncStateIcon(state: state)
                }
                .font(.caption)
                .foregroundStyle(.secondary)
                if let error, state == .failed {
                    Text(error)
                        .font(.caption)
                        .foregroundStyle(.red)
                }
            }
        }
        .accessibilityElement(children: .combine)
    }
}

struct SyncStateIcon: View {
    let state: RemarkSyncState

    var body: some View {
        switch state {
        case .pending:
            Image(systemName: "clock.arrow.circlepath")
                .foregroundStyle(.orange)
                .accessibilityLabel(Text("Waiting to upload"))
        case .synced:
            Image(systemName: "checkmark.icloud.fill")
                .foregroundStyle(.green)
                .accessibilityLabel(Text("Uploaded"))
        case .failed:
            Image(systemName: "exclamationmark.triangle.fill")
                .foregroundStyle(.red)
                .accessibilityLabel(Text("Upload failed"))
        }
    }
}

#if DEBUG
#Preview("Animal in heat") {
    let app = AppModel.demo()
    NavigationStack {
        AnimalDetailView(animalID: DemoData.id(DemoData.estrusIndex), app: app)
    }
    .environment(app)
    .task { await app.start() }
}
#endif

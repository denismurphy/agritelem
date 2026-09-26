import Domain
import SwiftUI

/// A management-tag badge coloured by the animal's most urgent status.
struct TagBadge: View {
    let tag: String
    let tint: Color
    var size: CGFloat = 46

    var body: some View {
        Text(tag)
            .font(.system(size: size * 0.36, weight: .bold, design: .rounded))
            .monospacedDigit()
            .minimumScaleFactor(0.6)
            .lineLimit(1)
            .foregroundStyle(.white)
            .frame(width: size, height: size)
            .background(tint.gradient, in: .rect(cornerRadius: size * 0.28, style: .continuous))
            .accessibilityLabel(Text("Tag \(tag)"))
    }
}

struct HerdRowView: View {
    let row: HerdRow

    var body: some View {
        HStack(spacing: 14) {
            TagBadge(tag: row.tag, tint: row.primaryStatus.tint)
            VStack(alignment: .leading, spacing: 3) {
                Text(row.name)
                    .font(.headline)
                HStack(spacing: 4) {
                    if let breed = Breed.name(for: row.breedCode) {
                        Text(breed)
                        Text(verbatim: "·")
                    }
                    Text(row.id.displayID)
                        .monospacedDigit()
                }
                .font(.caption)
                .foregroundStyle(.secondary)
                .lineLimit(1)
            }
            Spacer(minLength: 8)
            StatusSymbols(statuses: row.statuses)
            RuminationReadout(minutes: row.ruminationMinutes)
        }
        .padding(.vertical, 4)
    }
}

private struct StatusSymbols: View {
    let statuses: [AnimalStatus]

    var body: some View {
        HStack(spacing: 6) {
            ForEach(statuses.filter { $0 != .normal }, id: \.self) { status in
                Image(systemName: status.systemImage)
                    .foregroundStyle(status.tint)
                    .symbolEffect(.pulse, options: .repeat(.periodic(delay: 2)), isActive: status == .inHeat)
                    .accessibilityLabel(Text(status.title))
            }
        }
        .font(.body)
    }
}

private struct RuminationReadout: View {
    let minutes: Double?

    var body: some View {
        VStack(alignment: .trailing, spacing: 0) {
            if let minutes {
                Text(minutes, format: .number.precision(.fractionLength(0)))
                    .font(.system(.body, design: .rounded, weight: .semibold))
                    .monospacedDigit()
                Text("min")
                    .font(.caption2)
                    .foregroundStyle(.secondary)
            } else {
                Image(systemName: "minus")
                    .foregroundStyle(.tertiary)
            }
        }
        .frame(minWidth: 36, alignment: .trailing)
        .accessibilityElement(children: .ignore)
        .accessibilityLabel(minutes.map { Text("Rumination \($0, format: .number.precision(.fractionLength(0))) minutes") } ?? Text("No rumination data"))
    }
}

/// Horizontally scrolling cards for animals that need attention.
struct AttentionCarousel: View {
    let rows: [HerdRow]

    var body: some View {
        ScrollView(.horizontal) {
            LazyHStack(spacing: 12) {
                ForEach(rows) { row in
                    NavigationLink(value: row.id) {
                        AttentionCard(name: row.name, tag: row.tag, status: row.primaryStatus)
                    }
                    .buttonStyle(.plain)
                }
            }
            .scrollTargetLayout()
        }
        .scrollTargetBehavior(.viewAligned)
        .contentMargins(.horizontal, 16, for: .scrollContent)
        .scrollIndicators(.hidden)
    }
}

private struct AttentionCard: View {
    let name: String
    let tag: String
    let status: AnimalStatus

    var body: some View {
        VStack(alignment: .leading, spacing: 12) {
            HStack {
                Image(systemName: status.systemImage)
                    .font(.title2)
                    .foregroundStyle(status.tint)
                    .symbolEffect(.breathe, options: .repeating)
                Spacer()
                Text(tag)
                    .font(.caption.weight(.bold))
                    .monospacedDigit()
                    .padding(.horizontal, 8)
                    .padding(.vertical, 3)
                    .background(status.tint.opacity(0.18), in: .capsule)
                    .foregroundStyle(status.tint)
            }
            Spacer(minLength: 0)
            Text(name)
                .font(.headline)
                .lineLimit(1)
            Text(status.title)
                .font(.subheadline.weight(.medium))
                .foregroundStyle(status.tint)
        }
        .padding(14)
        .frame(width: 168, height: 128)
        .background {
            RoundedRectangle(cornerRadius: 20, style: .continuous)
                .fill(status.tint.gradient.opacity(0.14))
                .strokeBorder(status.tint.opacity(0.35), lineWidth: 1)
        }
        .accessibilityElement(children: .combine)
    }
}

/// Toolbar menu that filters the herd by status.
struct StatusFilterMenu: View {
    @Binding var filter: StatusFilter

    var body: some View {
        Menu {
            Picker("Show", selection: $filter) {
                ForEach(StatusFilter.allCases) { option in
                    Label {
                        Text(option.title)
                    } icon: {
                        Image(systemName: option.status?.systemImage ?? "square.grid.2x2")
                    }
                    .tag(option)
                }
            }
        } label: {
            Label("Filter", systemImage: filter == .all ? "line.3.horizontal.decrease.circle" : "line.3.horizontal.decrease.circle.fill")
        }
    }
}

#if DEBUG
#Preview("Rows", traits: .sizeThatFitsLayout) {
    List {
        HerdRowView(row: HerdRow(id: DemoData.id(2), name: "Daisy", tag: "103", breedCode: "HOL", statuses: [.inHeat], ruminationMinutes: 341))
        HerdRowView(row: HerdRow(id: DemoData.id(5), name: "Hazel", tag: "106", breedCode: "HOL", statuses: [.heatStress, .veterinaryAttention], ruminationMinutes: 468))
        HerdRowView(row: HerdRow(id: DemoData.id(0), name: "Bessie", tag: "101", breedCode: "JER", statuses: [.normal], ruminationMinutes: nil))
    }
}
#endif

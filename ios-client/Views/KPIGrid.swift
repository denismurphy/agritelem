import Domain
import SwiftUI

/// Herd KPIs as a grid of tiles.
struct KPIGrid: View {
    let overview: HerdOverview

    var body: some View {
        VStack(spacing: 12) {
            RuminationTile(meanMinutes: overview.meanRuminationMinutes)
            Grid(horizontalSpacing: 12, verticalSpacing: 12) {
                GridRow {
                    CountTile(title: "In Heat", count: overview.estrusAlerts, systemImage: AnimalStatus.inHeat.systemImage, tint: AnimalStatus.inHeat.tint)
                    CountTile(title: "Heat Stress", count: overview.heatStress, systemImage: AnimalStatus.heatStress.systemImage, tint: AnimalStatus.heatStress.tint)
                }
                GridRow {
                    CountTile(title: "Vet Attention", count: overview.veterinaryAttention, systemImage: AnimalStatus.veterinaryAttention.systemImage, tint: AnimalStatus.veterinaryAttention.tint)
                    CountTile(title: "Herd Size", count: overview.animalCount, systemImage: "tag.fill", tint: .blue)
                }
            }
        }
    }
}

/// Shared tile chrome.
private struct TileBackground: ViewModifier {
    func body(content: Content) -> some View {
        content
            .padding(16)
            .frame(maxWidth: .infinity, alignment: .leading)
            .background(Color(.secondarySystemGroupedBackground), in: .rect(cornerRadius: 22, style: .continuous))
    }
}

private struct CountTile: View {
    let title: LocalizedStringResource
    let count: Int
    let systemImage: String
    let tint: Color

    var body: some View {
        VStack(alignment: .leading, spacing: 10) {
            Image(systemName: systemImage)
                .font(.title2)
                .symbolRenderingMode(.hierarchical)
                .foregroundStyle(count > 0 ? tint : .secondary)
                .symbolEffect(.bounce, value: count)
                .frame(height: 30, alignment: .leading)
            Text(count, format: .number)
                .font(.system(.largeTitle, design: .rounded, weight: .bold))
                .monospacedDigit()
                .contentTransition(.numericText(value: Double(count)))
            Text(title)
                .font(.subheadline.weight(.medium))
                .foregroundStyle(.secondary)
        }
        .modifier(TileBackground())
        .accessibilityElement(children: .combine)
    }
}

private struct RuminationTile: View {
    let meanMinutes: Double?

    /// The spec's upper plausible bound for daily rumination.
    private static let range = 0.0...720.0

    var body: some View {
        HStack(alignment: .center, spacing: 16) {
            VStack(alignment: .leading, spacing: 6) {
                Label("Average Rumination", systemImage: "leaf.fill")
                    .font(.subheadline.weight(.medium))
                    .foregroundStyle(.secondary)
                    .labelStyle(.titleAndIcon)
                if let meanMinutes {
                    Text(Measurement(value: meanMinutes, unit: UnitDuration.minutes), format: .measurement(width: .abbreviated, numberFormatStyle: .number.precision(.fractionLength(0))))
                        .font(.system(.title, design: .rounded, weight: .bold))
                        .monospacedDigit()
                        .contentTransition(.numericText(value: meanMinutes))
                    Text("per cow per day, verified days only")
                        .font(.caption)
                        .foregroundStyle(.tertiary)
                } else {
                    Text("No verified data")
                        .font(.title3.weight(.semibold))
                        .foregroundStyle(.secondary)
                }
            }
            Spacer(minLength: 0)
            Gauge(value: min(meanMinutes ?? 0, Self.range.upperBound), in: Self.range) {
                Image(systemName: "leaf.fill")
            }
            .gaugeStyle(.accessoryCircularCapacity)
            .tint(.green)
            .accessibilityHidden(true)
        }
        .modifier(TileBackground())
        .accessibilityElement(children: .combine)
    }
}

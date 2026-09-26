import Charts
import Domain
import SwiftUI

/// Daily telemetry for one metric: headline value, chart and legend.
struct TelemetryCard: View {
    @Bindable var model: AnimalDetailModel

    var body: some View {
        DetailCard(title: "Last \(AnimalDetailModel.days) Days", systemImage: "chart.bar.xaxis") {
            Picker("Metric", selection: $model.metric) {
                ForEach(ChartMetric.allCases) { metric in
                    Text(metric.title).tag(metric)
                }
            }
            .pickerStyle(.segmented)

            switch model.phase {
            case .loading where model.series.isEmpty:
                ProgressView()
                    .frame(maxWidth: .infinity, minHeight: 240)
            case let .failed(message) where model.series.isEmpty:
                ContentUnavailableView("No Telemetry", systemImage: "waveform.slash", description: Text(message))
                    .frame(minHeight: 240)
            default:
                if model.series.isEmpty {
                    ContentUnavailableView("No Data Yet", systemImage: model.metric.systemImage,
                                           description: Text("This sensor hasn't reported a complete day."))
                        .frame(minHeight: 240)
                } else {
                    LatestValue(metric: model.metric, statistic: model.latest)
                    TelemetryChart(metric: model.metric, series: model.series)
                    ChartLegend(metric: model.metric)
                }
            }
        }
    }
}

// MARK: - Formatting

extension ChartMetric {
    var tint: Color {
        switch self {
        case .rumination: .green
        case .activity: .indigo
        case .temperature: .orange
        }
    }

    /// Plausible temperature range: 39.5 °C is the heat-stress threshold.
    static let thermalStressCelsius = 39.5

    func formatted(_ value: Double) -> Text {
        switch self {
        case .rumination:
            Text(Measurement(value: value, unit: UnitDuration.minutes),
                 format: .measurement(width: .abbreviated, numberFormatStyle: .number.precision(.fractionLength(0))))
        case .activity:
            Text("\(value, format: .number.precision(.fractionLength(0))) steps")
        case .temperature:
            Text(Measurement(value: value, unit: UnitTemperature.celsius),
                 format: .measurement(width: .abbreviated, usage: .asProvided, numberFormatStyle: .number.precision(.fractionLength(1))))
        }
    }
}

/// Days are UTC calendar days; format them in UTC so they never shift.
private let utcDay = Date.FormatStyle(date: .abbreviated, time: .omitted, timeZone: .gmt)

private struct LatestValue: View {
    let metric: ChartMetric
    let statistic: DailyStatistic?

    var body: some View {
        if let statistic {
            HStack(alignment: .firstTextBaseline) {
                VStack(alignment: .leading, spacing: 2) {
                    metric.formatted(statistic.value)
                        .font(.system(.title, design: .rounded, weight: .bold))
                        .monospacedDigit()
                        .contentTransition(.numericText(value: statistic.value))
                    Text(statistic.day, format: utcDay)
                        .font(.caption)
                        .foregroundStyle(.secondary)
                }
                Spacer()
                BaselineDelta(metric: metric, value: statistic.value, baseline: statistic.baselineMedian, zScore: statistic.zScore)
            }
            .animation(.snappy, value: statistic)
        }
    }
}

/// The latest value's change against the animal's own 14-day baseline.
private struct BaselineDelta: View {
    let metric: ChartMetric
    let value: Double
    let baseline: Double?
    let zScore: Double?

    var body: some View {
        if let baseline, baseline != 0 {
            let difference = value - baseline
            let unusual = abs(zScore ?? 0) >= 2
            VStack(alignment: .trailing, spacing: 2) {
                Label {
                    change(difference, baseline: baseline)
                } icon: {
                    Image(systemName: isNegligible(difference, baseline: baseline) ? "equal" : difference >= 0 ? "arrow.up.right" : "arrow.down.right")
                }
                .font(.subheadline.weight(.semibold))
                .foregroundStyle(unusual ? .pink : .secondary)
                Text("vs 14-day baseline")
                    .font(.caption2)
                    .foregroundStyle(.tertiary)
            }
            .accessibilityElement(children: .combine)
        } else {
            Text("Building baseline…")
                .font(.caption)
                .foregroundStyle(.tertiary)
        }
    }

    private func isNegligible(_ difference: Double, baseline: Double) -> Bool {
        metric == .temperature ? abs(difference) < 0.05 : abs(difference / baseline) < 0.005
    }

    /// Relative change for totals; an absolute difference for temperature,
    /// where a percentage of degrees Celsius means nothing.
    private func change(_ difference: Double, baseline: Double) -> Text {
        // Changes that round to zero would print as "−0.0"; say so plainly.
        if isNegligible(difference, baseline: baseline) {
            return Text("On baseline")
        }
        return switch metric {
        case .temperature:
            Text(Measurement(value: difference, unit: UnitTemperature.celsius),
                 format: .measurement(width: .abbreviated, usage: .asProvided,
                                      numberFormatStyle: .number.precision(.fractionLength(1)).sign(strategy: .always())))
        case .rumination, .activity:
            Text(difference / baseline, format: .percent.precision(.fractionLength(0)).sign(strategy: .always()))
        }
    }
}

// MARK: - Chart

private struct TelemetryChart: View {
    let metric: ChartMetric
    let series: [DailyStatistic]
    @State private var selectedDay: Date?

    private var selected: DailyStatistic? {
        guard let selectedDay else { return nil }
        return series.min { abs($0.day.timeIntervalSince(selectedDay)) < abs($1.day.timeIntervalSince(selectedDay)) }
    }

    var body: some View {
        Chart {
            switch metric {
            case .temperature:
                TemperatureMarks(series: series, tint: metric.tint)
            case .rumination, .activity:
                TotalMarks(series: series, tint: metric.tint, label: metric.title)
            }
            BaselineMarks(series: series)
            FlagMarks(series: series)
            if let selected {
                SelectionRule(statistic: selected, metric: metric)
            }
        }
        .chartXSelection(value: $selectedDay)
        .chartYScale(domain: .automatic(includesZero: metric != .temperature))
        .chartXAxis {
            AxisMarks(values: .stride(by: .day, count: 7)) {
                AxisGridLine()
                AxisValueLabel(format: .dateTime.day().month(.abbreviated))
            }
        }
        .environment(\.timeZone, .gmt)
        .frame(height: 240)
        .sensoryFeedback(.selection, trigger: selected?.day)
    }
}

/// Daily totals as bars; imputed days are faded, flagged days highlighted.
private struct TotalMarks: ChartContent {
    let series: [DailyStatistic]
    let tint: Color
    let label: LocalizedStringResource

    var body: some ChartContent {
        ForEach(series) { day in
            BarMark(
                x: .value("Day", day.day, unit: .day),
                y: .value(String(localized: label), day.value)
            )
            .foregroundStyle(day.flags.isEmpty ? tint.gradient : Color.pink.gradient)
            .opacity(day.quality == .imputed ? 0.35 : 0.85)
            .cornerRadius(3)
            .accessibilityLabel(Text(day.day, format: utcDay))
            .accessibilityValue(day.quality == .imputed ? Text("\(day.value, format: .number.precision(.fractionLength(0))), estimated") : Text(day.value, format: .number.precision(.fractionLength(0))))
        }
    }
}

/// Daily mean temperature as a line over a soft area, with the heat-stress
/// threshold as a rule.
private struct TemperatureMarks: ChartContent {
    let series: [DailyStatistic]
    let tint: Color

    var body: some ChartContent {
        RuleMark(y: .value("Heat stress", ChartMetric.thermalStressCelsius))
            .foregroundStyle(.red.opacity(0.6))
            .lineStyle(StrokeStyle(lineWidth: 1, dash: [3, 3]))
            .annotation(position: .top, alignment: .leading) {
                Text("Heat stress")
                    .font(.caption2.weight(.semibold))
                    .foregroundStyle(.red)
            }
        ForEach(series) { day in
            AreaMark(
                x: .value("Day", day.day, unit: .day),
                yStart: .value("Floor", 37.5),
                yEnd: .value("Temperature", day.value)
            )
            .foregroundStyle(LinearGradient(colors: [tint.opacity(0.3), tint.opacity(0)], startPoint: .top, endPoint: .bottom))
            .interpolationMethod(.monotone)
            LineMark(x: .value("Day", day.day, unit: .day), y: .value("Temperature", day.value))
                .foregroundStyle(tint)
                .lineStyle(StrokeStyle(lineWidth: 2.5, lineCap: .round))
                .interpolationMethod(.monotone)
        }
    }
}

/// The animal's rolling 14-day median.
private struct BaselineMarks: ChartContent {
    let series: [DailyStatistic]

    var body: some ChartContent {
        ForEach(series.filter { $0.baselineMedian != nil }) { day in
            LineMark(
                x: .value("Day", day.day, unit: .day),
                y: .value("Baseline", day.baselineMedian ?? 0),
                series: .value("Series", "baseline")
            )
            .foregroundStyle(.primary.opacity(0.75))
            .lineStyle(StrokeStyle(lineWidth: 2, lineCap: .round, dash: [5, 4]))
            .interpolationMethod(.monotone)
        }
    }
}

/// Symbols above days with cross-metric findings.
private struct FlagMarks: ChartContent {
    let series: [DailyStatistic]

    var body: some ChartContent {
        ForEach(series.filter { $0.flags.contains(.heatSignature) || $0.flags.contains(.thermalStress) }) { day in
            PointMark(x: .value("Day", day.day, unit: .day), y: .value("Value", day.value))
                .symbol {
                    Image(systemName: day.flags.contains(.heatSignature) ? "heart.fill" : "thermometer.sun.fill")
                        .font(.caption)
                        .foregroundStyle(day.flags.contains(.heatSignature) ? .pink : .red)
                        .padding(4)
                        .background(.background, in: .circle)
                }
        }
    }
}

private struct SelectionRule: ChartContent {
    let statistic: DailyStatistic
    let metric: ChartMetric

    var body: some ChartContent {
        RuleMark(x: .value("Selected", statistic.day, unit: .day))
            .foregroundStyle(.secondary.opacity(0.4))
            .annotation(position: .top, spacing: 0, overflowResolution: .init(x: .fit(to: .chart), y: .disabled)) {
                SelectionCallout(statistic: statistic, metric: metric)
            }
    }
}

private struct SelectionCallout: View {
    let statistic: DailyStatistic
    let metric: ChartMetric

    var body: some View {
        VStack(alignment: .leading, spacing: 4) {
            Text(statistic.day, format: utcDay)
                .font(.caption.weight(.semibold))
                .foregroundStyle(.secondary)
            metric.formatted(statistic.value)
                .font(.headline)
                .monospacedDigit()
            if let baseline = statistic.baselineMedian {
                HStack(spacing: 4) {
                    Text("Baseline")
                    metric.formatted(baseline)
                }
                .font(.caption)
                .foregroundStyle(.secondary)
            }
            if statistic.quality == .imputed {
                Label("Estimated from \(statistic.coverage, format: .percent.precision(.fractionLength(0))) of hours", systemImage: "wand.and.stars")
                    .font(.caption2)
                    .foregroundStyle(.orange)
            }
            ForEach(statistic.flags, id: \.self) { flag in
                FlagLabel(flag: flag)
            }
        }
        .padding(10)
        .background(.regularMaterial, in: .rect(cornerRadius: 12, style: .continuous))
    }
}

private struct FlagLabel: View {
    let flag: TelemetryFlag

    var body: some View {
        switch flag {
        case .heatSignature:
            Label("Heat signature", systemImage: "heart.fill").font(.caption2.weight(.semibold)).foregroundStyle(.pink)
        case .thermalStress:
            Label("Thermal stress", systemImage: "thermometer.sun.fill").font(.caption2.weight(.semibold)).foregroundStyle(.red)
        case .timeBudgetConflict:
            Label("Conflicting time budget", systemImage: "exclamationmark.triangle").font(.caption2).foregroundStyle(.orange)
        case .dailyOutOfRange:
            Label("Outside plausible range", systemImage: "exclamationmark.triangle").font(.caption2).foregroundStyle(.orange)
        }
    }
}

private struct ChartLegend: View {
    let metric: ChartMetric

    var body: some View {
        ViewThatFits {
            HStack(spacing: 14) { items }
            VStack(alignment: .leading, spacing: 6) { items }
        }
        .font(.caption)
        .foregroundStyle(.secondary)
    }

    @ViewBuilder private var items: some View {
        LegendItem(title: "Daily") {
            if metric == .temperature {
                Capsule().fill(metric.tint).frame(width: 14, height: 2.5)
            } else {
                RoundedRectangle(cornerRadius: 2).fill(metric.tint.gradient).frame(width: 10, height: 10)
            }
        }
        LegendItem(title: "14-day baseline") {
            Capsule().stroke(.primary.opacity(0.75), style: StrokeStyle(lineWidth: 1.5, dash: [3, 2])).frame(width: 16, height: 1.5)
        }
        if metric != .temperature {
            LegendItem(title: "Estimated") {
                RoundedRectangle(cornerRadius: 2).fill(metric.tint.opacity(0.4)).frame(width: 10, height: 10)
            }
        }
        LegendItem(title: "Finding") {
            if metric == .temperature {
                Image(systemName: "thermometer.sun.fill").foregroundStyle(.red)
            } else {
                Image(systemName: "heart.fill").foregroundStyle(.pink)
            }
        }
    }
}

private struct LegendItem<Swatch: View>: View {
    let title: LocalizedStringResource
    @ViewBuilder var swatch: Swatch

    var body: some View {
        HStack(spacing: 5) {
            swatch
            Text(title)
        }
    }
}

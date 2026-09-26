import Domain
import SwiftUI

/// Loading state shared by screens that show cached data while refreshing.
enum LoadPhase: Equatable {
    case loading
    case loaded
    case failed(String)
}

extension AnimalStatus {
    var title: LocalizedStringResource {
        switch self {
        case .normal: "Normal"
        case .inHeat: "In Heat"
        case .veterinaryAttention: "Vet Attention"
        case .heatStress: "Heat Stress"
        }
    }

    var systemImage: String {
        switch self {
        case .normal: "checkmark.circle.fill"
        case .inHeat: "heart.fill"
        case .veterinaryAttention: "cross.case.fill"
        case .heatStress: "thermometer.sun.fill"
        }
    }

    var tint: Color {
        switch self {
        case .normal: .green
        case .inHeat: .pink
        case .veterinaryAttention: .red
        case .heatStress: .orange
        }
    }

    /// Order for display: the most urgent first.
    var urgency: Int {
        switch self {
        case .veterinaryAttention: 0
        case .inHeat: 1
        case .heatStress: 2
        case .normal: 3
        }
    }
}

extension RemarkCategory {
    var title: LocalizedStringResource {
        switch self {
        case .general: "General"
        case .veterinary: "Veterinary"
        case .behaviour: "Behaviour"
        case .feeding: "Feeding"
        }
    }

    var systemImage: String {
        switch self {
        case .general: "note.text"
        case .veterinary: "stethoscope"
        case .behaviour: "figure.walk.motion"
        case .feeding: "leaf.fill"
        }
    }

    var tint: Color {
        switch self {
        case .general: .gray
        case .veterinary: .red
        case .behaviour: .purple
        case .feeding: .green
        }
    }
}

/// ICAR 3-character breed codes (Interbull) for the breeds in the demo herd.
enum Breed {
    static func name(for code: String?) -> LocalizedStringResource? {
        switch code {
        case nil: nil
        case "HOL": "Holstein"
        case "JER": "Jersey"
        case "BSW": "Brown Swiss"
        case "MON": "Montbéliarde"
        case "RDC": "Ayrshire"
        case let code?: "\(code)"
        }
    }
}

/// A UTC calendar, because the server's daily statistics are UTC days.
extension Calendar {
    static let utc: Calendar = {
        var calendar = Calendar(identifier: .gregorian)
        calendar.timeZone = .gmt
        return calendar
    }()
}

// Renders Resources/Assets.xcassets/AppIcon.appiconset/AppIcon.png: an SF
// Symbol on a pasture-green gradient, opaque as iOS requires.
// Run from ios-client/: swift scripts/render-app-icon.swift
import AppKit

let size = 1024
// RGB without alpha: iOS app icons must be opaque.
guard let context = CGContext(data: nil, width: size, height: size, bitsPerComponent: 8, bytesPerRow: 0,
                              space: CGColorSpace(name: CGColorSpace.sRGB)!,
                              bitmapInfo: CGImageAlphaInfo.noneSkipLast.rawValue) else {
    fatalError("cannot create bitmap context")
}
let colors = [CGColor(srgbRed: 0.20, green: 0.66, blue: 0.43, alpha: 1), CGColor(srgbRed: 0.04, green: 0.30, blue: 0.25, alpha: 1)]
let gradient = CGGradient(colorsSpace: nil, colors: colors as CFArray, locations: [0, 1])!
context.drawLinearGradient(gradient, start: CGPoint(x: 0, y: size), end: CGPoint(x: size, y: 0), options: [])

NSGraphicsContext.saveGraphicsState()
NSGraphicsContext.current = NSGraphicsContext(cgContext: context, flipped: false)
let configuration = NSImage.SymbolConfiguration(pointSize: 440, weight: .semibold)
    .applying(.init(paletteColors: [.white]))
guard let symbol = NSImage(systemSymbolName: "waveform.path.ecg", accessibilityDescription: nil)?
    .withSymbolConfiguration(configuration) else {
    fatalError("symbol unavailable")
}
symbol.draw(in: NSRect(x: (Double(size) - symbol.size.width) / 2, y: (Double(size) - symbol.size.height) / 2,
                       width: symbol.size.width, height: symbol.size.height))
NSGraphicsContext.restoreGraphicsState()

let png = NSBitmapImageRep(cgImage: context.makeImage()!).representation(using: .png, properties: [:])!
try png.write(to: URL(fileURLWithPath: "Resources/Assets.xcassets/AppIcon.appiconset/AppIcon.png"))
print("wrote AppIcon.png")

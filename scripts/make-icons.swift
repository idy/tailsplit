import AppKit
import Foundation

guard CommandLine.arguments.count == 2 else { exit(2) }
let directory = URL(fileURLWithPath: CommandLine.arguments[1], isDirectory: true)
try FileManager.default.createDirectory(at: directory, withIntermediateDirectories: true)

// TailSplit's split-route mark on a 64-unit grid with y pointing down: a source
// whose route splits toward two destinations. The tray icons
// (cmd/tailsplit-tray/icon.go) and the Windows packager draw the same geometry.
let dots: [(CGFloat, CGFloat)] = [(15, 32), (49, 17), (49, 47)]

func drawIcon(_ size: Int) -> Data {
    let bitmap = NSBitmapImageRep(
        bitmapDataPlanes: nil, pixelsWide: size, pixelsHigh: size,
        bitsPerSample: 8, samplesPerPixel: 4, hasAlpha: true,
        isPlanar: false, colorSpaceName: .deviceRGB, bytesPerRow: 0, bitsPerPixel: 0
    )!
    NSGraphicsContext.saveGraphicsState()
    NSGraphicsContext.current = NSGraphicsContext(bitmapImageRep: bitmap)
    let scale = CGFloat(size) / 1024
    let transform = NSAffineTransform()
    transform.scale(by: scale)
    transform.concat()
    // Apple's icon grid: an 824-point rounded tile with a soft drop shadow.
    // Shadow metrics are in device pixels, so they follow the rendition size.
    let tile = NSRect(x: 100, y: 100, width: 824, height: 824)
    let background = NSBezierPath(roundedRect: tile, xRadius: 185, yRadius: 185)
    NSGraphicsContext.saveGraphicsState()
    let shadow = NSShadow()
    shadow.shadowColor = NSColor(white: 0, alpha: 0.3)
    shadow.shadowOffset = NSSize(width: 0, height: -10 * scale)
    shadow.shadowBlurRadius = 24 * scale
    shadow.set()
    NSColor(srgbRed: 72 / 255, green: 112 / 255, blue: 242 / 255, alpha: 1).setFill()
    background.fill()
    NSGraphicsContext.restoreGraphicsState()
    // A diagonal blue-to-indigo gradient behind the white mark.
    NSGradient(starting: NSColor(srgbRed: 91 / 255, green: 141 / 255, blue: 1, alpha: 1),
               ending: NSColor(srgbRed: 53 / 255, green: 84 / 255, blue: 230 / 255, alpha: 1))!
        .draw(in: background, angle: -45)
    let unit = tile.width / 64
    func point(_ x: CGFloat, _ y: CGFloat) -> NSPoint {
        NSPoint(x: tile.minX + x * unit, y: tile.maxY - y * unit)
    }
    // Small renditions keep a legible minimum line width and dot size.
    NSColor.white.set()
    let route = NSBezierPath()
    route.lineWidth = max(4.5 * unit, 1.6 / scale)
    route.lineCapStyle = .round
    route.move(to: point(15, 32))
    route.line(to: point(26, 32))
    for end: CGFloat in [17, 47] {
        route.move(to: point(26, 32))
        route.curve(to: point(49, end), controlPoint1: point(36, 32), controlPoint2: point(37, end))
    }
    route.stroke()
    let radius = max(5.25 * unit, 1.75 / scale)
    for (x, y) in dots {
        let center = point(x, y)
        NSBezierPath(ovalIn: NSRect(x: center.x - radius, y: center.y - radius,
                                    width: 2 * radius, height: 2 * radius)).fill()
    }
    NSGraphicsContext.restoreGraphicsState()
    return bitmap.representation(using: .png, properties: [:])!
}

for points in [16, 32, 128, 256, 512] {
    for multiple in [1, 2] {
        let suffix = multiple == 1 ? "" : "@2x"
        let name = "icon_\(points)x\(points)\(suffix).png"
        try drawIcon(points * multiple).write(to: directory.appendingPathComponent(name))
    }
}

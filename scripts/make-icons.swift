import AppKit
import Foundation

guard CommandLine.arguments.count == 2 else { exit(2) }
let directory = URL(fileURLWithPath: CommandLine.arguments[1], isDirectory: true)
try FileManager.default.createDirectory(at: directory, withIntermediateDirectories: true)

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
    let background = NSBezierPath(roundedRect: NSRect(x: 65, y: 65, width: 894, height: 894),
                                  xRadius: 206, yRadius: 206)
    NSGradient(starting: NSColor(srgbRed: 0.10, green: 0.45, blue: 0.91, alpha: 1),
               ending: NSColor(srgbRed: 0.06, green: 0.73, blue: 0.75, alpha: 1))!
        .draw(in: background, angle: 45)
    NSColor.white.setStroke()
    let route = NSBezierPath()
    route.lineWidth = 62
    route.lineCapStyle = .round
    route.lineJoinStyle = .round
    route.move(to: NSPoint(x: 512, y: 265))
    route.line(to: NSPoint(x: 512, y: 472))
    route.line(to: NSPoint(x: 300, y: 684))
    route.move(to: NSPoint(x: 512, y: 472))
    route.line(to: NSPoint(x: 724, y: 684))
    route.move(to: NSPoint(x: 300, y: 550))
    route.line(to: NSPoint(x: 300, y: 684))
    route.line(to: NSPoint(x: 434, y: 684))
    route.move(to: NSPoint(x: 590, y: 684))
    route.line(to: NSPoint(x: 724, y: 684))
    route.line(to: NSPoint(x: 724, y: 550))
    route.stroke()
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

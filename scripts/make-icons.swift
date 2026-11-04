import AppKit
import Foundation

guard CommandLine.arguments.count == 2 else { exit(2) }
let directory = URL(fileURLWithPath: CommandLine.arguments[1], isDirectory: true)
try FileManager.default.createDirectory(at: directory, withIntermediateDirectories: true)

// TailSplit's snake on a 64-unit grid with y pointing down: its body is the S
// and its forked tail the route splitting in two. The outline starts at the
// nose and each curve is a cubic Bézier from the previous point; the eye is a
// round hole. The tray icons (cmd/tailsplit-tray/icon.go) and the Windows
// packager draw the same geometry.
let start: (CGFloat, CGFloat) = (53.4, 21.68)
let curves: [[(CGFloat, CGFloat)]] = [
    [(52.9, 22.65), (51.94, 23.25), (50.89, 23.49)],
    [(46.01, 24.6), (43.48, 20.21), (39.58, 18.56)],
    [(36.94, 17.45), (34.01, 17.28), (31.18, 17.37)],
    [(28.78, 17.45), (26.27, 18.1), (24.6, 19.93)],
    [(19.47, 25.57), (30.86, 27.54), (34.03, 28.41)],
    [(39.04, 29.8), (45.05, 32.06), (47.15, 37.29)],
    [(48.15, 39.81), (47.99, 42.69), (47.04, 45.2)],
    [(45.28, 49.83), (40.8, 52.56), (36.03, 53.12)],
    [(34.32, 53.32), (32.58, 53.24), (30.87, 53.33)],
    [(28.75, 53.44), (26.64, 53.64), (24.54, 53.9)],
    [(19.38, 54.53), (14.28, 55.52), (9.22, 56.66)],
    [(15.21, 53.26), (21.56, 51.11), (28.22, 49.44)],
    [(21.03, 46.73), (14.91, 42.28), (9.22, 37.19)],
    [(14.49, 39.48), (19.52, 42.29), (24.98, 44.12)],
    [(28.28, 45.23), (32.2, 46.35), (35.7, 45.67)],
    [(37.15, 45.38), (38.65, 44.68), (39.42, 43.36)],
    [(44.7, 34.38), (14.31, 38.99), (15.03, 22.36)],
    [(15.19, 18.56), (17.15, 15.03), (20.16, 12.74)],
    [(25.74, 8.52), (33.87, 9.15), (40.21, 11.26)],
    [(41.74, 11.77), (43.13, 12.62), (44.64, 13.13)],
    [(46.06, 13.61), (47.58, 13.49), (49, 13.95)],
    [(51.97, 14.92), (55.06, 18.45), (53.4, 21.68)],
]
let eye: (CGFloat, CGFloat, CGFloat) = (48.7, 16.7, 1.9)  // Center and radius.

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
    // The tile's single color, the configuration panel's canvas.
    NSColor(srgbRed: 28 / 255, green: 28 / 255, blue: 30 / 255, alpha: 1).setFill()
    background.fill()
    NSGraphicsContext.restoreGraphicsState()
    // Small renditions enlarge the mark and keep a legible minimum tail width.
    let unit = tile.width / 64 * (size <= 24 ? 1.1 : 1)
    func point(_ x: CGFloat, _ y: CGFloat) -> NSPoint {
        NSPoint(x: tile.midX + (x - 32) * unit, y: tile.midY - (y - 32) * unit)
    }
    NSColor.white.set()
    let mark = NSBezierPath()
    mark.windingRule = .evenOdd
    mark.move(to: point(start.0, start.1))
    for curve in curves {
        mark.curve(to: point(curve[2].0, curve[2].1), controlPoint1: point(curve[0].0, curve[0].1),
                   controlPoint2: point(curve[1].0, curve[1].1))
    }
    mark.close()
    // Below 32 pixels the eye's hole only blurs.
    if size >= 32 {
        let center = point(eye.0, eye.1), radius = eye.2 * unit
        mark.appendOval(in: NSRect(x: center.x - radius, y: center.y - radius,
                                   width: 2 * radius, height: 2 * radius))
    }
    mark.fill()
    // Stroking softens the tips, growing the outline by half the line width.
    mark.lineJoinStyle = .round
    mark.lineWidth = 2 * max(0.5 * unit, 0.35 / scale)
    mark.stroke()
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

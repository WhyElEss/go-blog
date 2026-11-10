import AppKit

// Draws static/og.png, the 1200×630 link-preview card of the blog's home
// page (and of any page without a picture of its own), in the style of
// wishlist.stanislavski.me and vinyl.stanislavski.me: title, tagline and
// domain on the left, the blog's record-and-maple-leaf icon on the right.
// Runs on a Mac (AppKit):
//
//	curl -so /tmp/icon.png https://blog.stanislavski.me/img/2026/01/favicon-512.png
//	swift tools/og-card.swift /tmp/icon.png static
//
// The owner chose the light scheme of three drawn (light, red, dark).
let args = CommandLine.arguments
let record = NSImage(contentsOfFile: args[1])!
let out = args[2]
let W: CGFloat = 1200, H: CGFloat = 630

struct Scheme {
    let name: String
    let bgTop: NSColor, bgBottom: NSColor
    let title: NSColor, amp: NSColor, tagline: NSColor, domain: NSColor
    let glow: NSColor?
}

func rgb(_ hex: UInt32, _ a: CGFloat = 1) -> NSColor {
    NSColor(srgbRed: CGFloat((hex >> 16) & 0xff) / 255, green: CGFloat((hex >> 8) & 0xff) / 255, blue: CGFloat(hex & 0xff) / 255, alpha: a)
}

let schemes = [
    Scheme(name: "og", bgTop: rgb(0xf7f2ea), bgBottom: rgb(0xeee5d6),
           title: rgb(0x1b1b1f), amp: rgb(0xe00000), tagline: rgb(0x4a4642), domain: rgb(0xc8102e), glow: nil),
]

func font(_ size: CGFloat, _ weight: NSFont.Weight) -> NSFont {
    NSFont.systemFont(ofSize: size, weight: weight)
}

for s in schemes {
    let rep = NSBitmapImageRep(bitmapDataPlanes: nil, pixelsWide: Int(W), pixelsHigh: Int(H), bitsPerSample: 8,
                               samplesPerPixel: 4, hasAlpha: true, isPlanar: false, colorSpaceName: .deviceRGB,
                               bytesPerRow: 0, bitsPerPixel: 0)!
    NSGraphicsContext.saveGraphicsState()
    NSGraphicsContext.current = NSGraphicsContext(bitmapImageRep: rep)
    let ctx = NSGraphicsContext.current!.cgContext

    NSGradient(starting: s.bgTop, ending: s.bgBottom)!.draw(in: NSRect(x: 0, y: 0, width: W, height: H), angle: -60)

    // The record: the blog's icon, large on the right, partly off the edge.
    let d: CGFloat = 520
    let rect = NSRect(x: W - d + 40, y: (H - d) / 2, width: d, height: d)
    if let g = s.glow {
        let grad = NSGradient(starting: g, ending: g.withAlphaComponent(0))!
        grad.draw(fromCenter: NSPoint(x: rect.midX, y: rect.midY), radius: 0,
                  toCenter: NSPoint(x: rect.midX, y: rect.midY), radius: d * 0.75, options: [])
    }
    ctx.saveGState()
    let shadow = NSShadow()
    shadow.shadowColor = NSColor.black.withAlphaComponent(0.35)
    shadow.shadowBlurRadius = 30
    shadow.shadowOffset = NSSize(width: 0, height: -10)
    shadow.set()
    record.draw(in: rect, from: .zero, operation: .sourceOver, fraction: 1)
    ctx.restoreGState()

    // Text, left column.
    let left: CGFloat = 84
    let title = NSMutableAttributedString()
    let tf = font(96, .heavy)
    title.append(NSAttributedString(string: "Maple ", attributes: [.font: tf, .foregroundColor: s.title]))
    title.append(NSAttributedString(string: "&", attributes: [.font: tf, .foregroundColor: s.amp]))
    title.append(NSAttributedString(string: "\nVinyl", attributes: [.font: tf, .foregroundColor: s.title]))
    let para = NSMutableParagraphStyle()
    para.lineHeightMultiple = 0.92
    title.addAttribute(.paragraphStyle, value: para, range: NSRange(location: 0, length: title.length))
    title.draw(in: NSRect(x: left, y: 250, width: 640, height: 240))

    let tag = NSMutableParagraphStyle()
    tag.lineSpacing = 4
    NSAttributedString(string: "Canadian life, analog warmth,\nand audio adventures",
                       attributes: [.font: font(34, .regular), .foregroundColor: s.tagline, .paragraphStyle: tag])
        .draw(in: NSRect(x: left, y: 140, width: 620, height: 100))
    NSAttributedString(string: "blog.stanislavski.me", attributes: [.font: font(30, .semibold), .foregroundColor: s.domain])
        .draw(at: NSPoint(x: left, y: 66))

    NSGraphicsContext.restoreGraphicsState()
    try! rep.representation(using: .png, properties: [:])!.write(to: URL(fileURLWithPath: "\(out)/\(s.name).png"))
}

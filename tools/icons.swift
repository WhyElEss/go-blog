import AppKit

// Draws the blog's icon set and its link-preview card into static/ from
// its record-and-maple-leaf icon (the upload
// /img/2026/01/favicon-512.png), the way wishlist.stanislavski.me and
// vinyl.stanislavski.me have theirs:
//
//	icon.svg              any size, for browsers that take SVG icons
//	favicon-32.png        32×32 for the rest
//	favicon.ico           the same 32×32 PNG in an ICO, for /favicon.ico
//	apple-touch-icon.png  180×180, opaque (iOS rounds the corners itself)
//	og.png                1200×630 card for pages without a picture: name,
//	                      tagline, domain, the record (owner chose the light
//	                      scheme of three drawn: light, red, dark)
//
// Every record is drawn as vectors with the same numbers as icon.svg.
//
// The leaf is traced from the icon's red pixels and simplified to a
// polygon, so every size has the logo's leaf. Runs on a Mac (AppKit):
//
//	curl -so /tmp/icon.png https://blog.stanislavski.me/img/2026/01/favicon-512.png
//	swift tools/icons.swift /tmp/icon.png static

let args = CommandLine.arguments
let src = NSBitmapImageRep(data: try! Data(contentsOf: URL(fileURLWithPath: args[1])))!
let out = args[2]
let N = src.pixelsWide
precondition(src.pixelsHigh == N, "a square icon")

// Red pixels: the leaf.
func isLeaf(_ x: Int, _ y: Int) -> Bool {
    guard x >= 0, y >= 0, x < N, y < N, let c = src.colorAt(x: x, y: y)?.usingColorSpace(.sRGB) else { return false }
    return c.alphaComponent > 0.5 && c.redComponent > 0.75 && c.greenComponent < 0.35 && c.blueComponent < 0.35
}

// The leaf's outline along pixel edges, walked clockwise from its topmost
// left pixel (a square-tracing walk on the corner grid).
var start: (Int, Int)? = nil
outer: for y in 0..<N { for x in 0..<N where isLeaf(x, y) { start = (x, y); break outer } }
guard let (sx, sy) = start else { fatalError("no red leaf in the icon") }
var pts: [(Double, Double)] = []
var (cx, cy) = (sx, sy) // corner position (top-left corner of pixel sx,sy)
var dir = 0             // 0 right, 1 down, 2 left, 3 up (y grows downwards)
let dx = [1, 0, -1, 0], dy = [0, 1, 0, -1]
// The pixel on the right-hand / left-hand side of moving from corner (cx,cy) in direction d.
func ahead(_ cx: Int, _ cy: Int, _ d: Int) -> (left: Bool, right: Bool) {
    switch d {
    case 0: return (isLeaf(cx, cy - 1), isLeaf(cx, cy))
    case 1: return (isLeaf(cx, cy), isLeaf(cx - 1, cy))
    case 2: return (isLeaf(cx - 1, cy), isLeaf(cx - 1, cy - 1))
    default: return (isLeaf(cx - 1, cy - 1), isLeaf(cx, cy - 1))
    }
}
repeat {
    pts.append((Double(cx), Double(cy)))
    // Keep the leaf on the right: turn left if the left pixel is leaf,
    // go straight if only the right one is, else turn right.
    for turn in [3, 0, 1, 2] {
        let d = (dir + turn) % 4
        let a = ahead(cx, cy, d)
        if a.right && !a.left { dir = d; break }
    }
    cx += dx[dir]; cy += dy[dir]
} while !(cx == sx && cy == sy) && pts.count < N * N

// Douglas–Peucker: straight edges stay straight, stair steps go.
func simplify(_ p: [(Double, Double)], _ eps: Double) -> [(Double, Double)] {
    guard p.count > 2 else { return p }
    let (a, b) = (p.first!, p.last!)
    var (iMax, dMax) = (0, 0.0)
    for i in 1..<(p.count - 1) {
        let (x, y) = p[i]
        let len = max(hypot(b.0 - a.0, b.1 - a.1), 1e-9)
        let d = abs((b.1 - a.1) * x - (b.0 - a.0) * y + b.0 * a.1 - b.1 * a.0) / len
        if d > dMax { (iMax, dMax) = (i, d) }
    }
    if dMax <= eps { return [a, b] }
    return simplify(Array(p[...iMax]), eps).dropLast() + simplify(Array(p[iMax...]), eps)
}
// Split the closed loop at its farthest point so both halves are open runs.
let far = pts.indices.max { hypot(pts[$0].0 - pts[0].0, pts[$0].1 - pts[0].1) < hypot(pts[$1].0 - pts[0].0, pts[$1].1 - pts[0].1) }!
let leaf = Array(simplify(Array(pts[...far]), 1.6).dropLast() + simplify(Array(pts[far...]) + [pts[0]], 1.6).dropLast())

// Everything is drawn in a 0…1 box: the record as in the icon, the leaf
// in the icon's own place.
let s = Double(N)
let leafUnit = leaf.map { ($0.0 / s, $0.1 / s) }

func drawIcon(_ size: Int, background: NSColor?, recordScale: Double) -> NSBitmapImageRep {
    let rep = NSBitmapImageRep(bitmapDataPlanes: nil, pixelsWide: size, pixelsHigh: size, bitsPerSample: 8,
                               samplesPerPixel: 4, hasAlpha: true, isPlanar: false, colorSpaceName: .deviceRGB,
                               bytesPerRow: 0, bitsPerPixel: 0)!
    NSGraphicsContext.saveGraphicsState()
    NSGraphicsContext.current = NSGraphicsContext(bitmapImageRep: rep)
    let ctx = NSGraphicsContext.current!.cgContext
    let S = CGFloat(size)
    if let bg = background {
        NSGradient(starting: rgb(0xf7f2ea), ending: rgb(0xeee5d6))!.draw(in: NSRect(x: 0, y: 0, width: S, height: S), angle: -60)
        _ = bg
    }
    // y up in AppKit: flip so the 0…1 box reads like the icon.
    ctx.translateBy(x: 0, y: S)
    ctx.scaleBy(x: 1, y: -1)
    let k = CGFloat(recordScale)
    ctx.translateBy(x: S * (1 - k) / 2, y: S * (1 - k) / 2)
    ctx.scaleBy(x: S * k, y: S * k)
    recordPath(ctx)
    NSGraphicsContext.restoreGraphicsState()
    return rep
}

func rgb(_ hex: UInt32) -> NSColor {
    NSColor(srgbRed: CGFloat((hex >> 16) & 0xff) / 255, green: CGFloat((hex >> 8) & 0xff) / 255, blue: CGFloat(hex & 0xff) / 255, alpha: 1)
}

// The record: disc, grooves, label ring, leaf. Same numbers as the SVG.
let disc = rgb(0x1d1d22), groove = rgb(0x2e2e35), red = rgb(0xff0000)
let rings: [Double] = [0.44, 0.40, 0.36, 0.32, 0.28, 0.24]
func recordPath(_ ctx: CGContext) {
    ctx.setFillColor(disc.cgColor)
    ctx.fillEllipse(in: CGRect(x: 0.04, y: 0.04, width: 0.92, height: 0.92))
    ctx.setStrokeColor(groove.cgColor)
    ctx.setLineWidth(0.008)
    for r in rings { ctx.strokeEllipse(in: CGRect(x: 0.5 - r, y: 0.5 - r, width: 2 * r, height: 2 * r)) }
    ctx.setFillColor(red.cgColor)
    ctx.beginPath()
    ctx.move(to: CGPoint(x: leafUnit[0].0, y: leafUnit[0].1))
    for p in leafUnit.dropFirst() { ctx.addLine(to: CGPoint(x: p.0, y: p.1)) }
    ctx.closePath()
    ctx.fillPath()
}

func png(_ rep: NSBitmapImageRep) -> Data { rep.representation(using: .png, properties: [:])! }

// icon.svg in a 64 box.
func f(_ v: Double) -> String { String(format: "%.2f", v * 64).replacingOccurrences(of: ".00", with: "") }
var svg = "<svg xmlns=\"http://www.w3.org/2000/svg\" viewBox=\"0 0 64 64\">"
svg += "<circle cx=\"32\" cy=\"32\" r=\"\(f(0.46))\" fill=\"#1d1d22\"/>"
svg += "<g fill=\"none\" stroke=\"#2e2e35\" stroke-width=\"\(f(0.008))\">"
for r in rings { svg += "<circle cx=\"32\" cy=\"32\" r=\"\(f(r))\"/>" }
svg += "</g>"
svg += "<path fill=\"#ff0000\" d=\"M" + leafUnit.map { "\(f($0.0)) \(f($0.1))" }.joined(separator: "L") + "Z\"/></svg>\n"
try! svg.write(toFile: "\(out)/icon.svg", atomically: true, encoding: .utf8)

let p32 = png(drawIcon(32, background: nil, recordScale: 1.06))
try! p32.write(to: URL(fileURLWithPath: "\(out)/favicon-32.png"))
// ICO with one PNG entry (Windows Vista+ and every browser read PNG in ICO).
var ico = Data([0, 0, 1, 0, 1, 0, 32, 32, 0, 0, 1, 0, 32, 0])
var size = UInt32(p32.count).littleEndian, offset = UInt32(22).littleEndian
ico.append(Data(bytes: &size, count: 4)); ico.append(Data(bytes: &offset, count: 4)); ico.append(p32)
try! ico.write(to: URL(fileURLWithPath: "\(out)/favicon.ico"))
try! png(drawIcon(180, background: rgb(0xf7f2ea), recordScale: 0.86)).write(to: URL(fileURLWithPath: "\(out)/apple-touch-icon.png"))
// og.png: text on the left as in the owner's other cards, the record whole
// on the right.
do {
    let W: CGFloat = 1200, H: CGFloat = 630
    let rep = NSBitmapImageRep(bitmapDataPlanes: nil, pixelsWide: Int(W), pixelsHigh: Int(H), bitsPerSample: 8,
                               samplesPerPixel: 4, hasAlpha: true, isPlanar: false, colorSpaceName: .deviceRGB,
                               bytesPerRow: 0, bitsPerPixel: 0)!
    NSGraphicsContext.saveGraphicsState()
    NSGraphicsContext.current = NSGraphicsContext(bitmapImageRep: rep)
    let ctx = NSGraphicsContext.current!.cgContext
    NSGradient(starting: rgb(0xf7f2ea), ending: rgb(0xeee5d6))!.draw(in: NSRect(x: 0, y: 0, width: W, height: H), angle: -60)

    // The disc is 0.92 of the 0…1 box: a 470 px disc, 84 px from the
    // right edge like the text from the left one, centred in height.
    let d: CGFloat = 470, box = d / 0.92
    let cxR = W - 84 - d / 2, cyR = H / 2
    ctx.saveGState()
    ctx.setShadow(offset: CGSize(width: 0, height: -10), blur: 30, color: NSColor.black.withAlphaComponent(0.3).cgColor)
    ctx.setFillColor(disc.cgColor)
    ctx.fillEllipse(in: CGRect(x: cxR - d / 2, y: cyR - d / 2, width: d, height: d))
    ctx.restoreGState()
    ctx.saveGState()
    ctx.translateBy(x: cxR - box / 2, y: cyR + box / 2)
    ctx.scaleBy(x: box, y: -box)
    recordPath(ctx)
    ctx.restoreGState()

    func font(_ size: CGFloat, _ weight: NSFont.Weight) -> NSFont { NSFont.systemFont(ofSize: size, weight: weight) }
    let left: CGFloat = 84
    let ink = rgb(0x1b1b1f), tf = font(96, .heavy)
    let title = NSMutableAttributedString(string: "Maple ", attributes: [.font: tf, .foregroundColor: ink])
    title.append(NSAttributedString(string: "&", attributes: [.font: tf, .foregroundColor: rgb(0xe00000)]))
    title.append(NSAttributedString(string: "\nVinyl", attributes: [.font: tf, .foregroundColor: ink]))
    let para = NSMutableParagraphStyle()
    para.lineHeightMultiple = 0.92
    title.addAttribute(.paragraphStyle, value: para, range: NSRange(location: 0, length: title.length))
    title.draw(in: NSRect(x: left, y: 250, width: 560, height: 240))
    let tag = NSMutableParagraphStyle()
    tag.lineSpacing = 4
    NSAttributedString(string: "Canadian life, analog warmth,\nand audio adventures",
                       attributes: [.font: font(34, .regular), .foregroundColor: rgb(0x4a4642), .paragraphStyle: tag])
        .draw(in: NSRect(x: left, y: 140, width: 560, height: 100))
    NSAttributedString(string: "blog.stanislavski.me", attributes: [.font: font(30, .semibold), .foregroundColor: rgb(0xc8102e)])
        .draw(at: NSPoint(x: left, y: 66))
    NSGraphicsContext.restoreGraphicsState()
    try! png(rep).write(to: URL(fileURLWithPath: "\(out)/og.png"))
}
print("leaf: \(leaf.count) points from \(pts.count) edge steps")

// Draws GoDraw's icon: a cursive g in one pen stroke, whose exit stroke leaves
// as an arrow. Writes the flat icon and the stroke layers of AppIcon.icon.
//
//	swift icon/make.swift
import CoreGraphics
import Foundation
import ImageIO
import UniformTypeIdentifiers

let srgb = CGColorSpace(name: CGColorSpace.sRGB)!

struct RGB {
	var r, g, b: CGFloat
	init(_ hex: UInt32) {
		r = CGFloat((hex >> 16) & 255) / 255
		g = CGFloat((hex >> 8) & 255) / 255
		b = CGFloat(hex & 255) / 255
	}
	init(r: CGFloat, g: CGFloat, b: CGFloat) { (self.r, self.g, self.b) = (r, g, b) }
	var cg: CGColor { a(1) }
	func a(_ alpha: CGFloat) -> CGColor { CGColor(srgbRed: r, green: g, blue: b, alpha: alpha) }
	func mix(_ o: RGB, _ t: CGFloat) -> RGB { RGB(r: r + (o.r - r) * t, g: g + (o.g - g) * t, b: b + (o.b - b) * t) }
}

func P(_ x: CGFloat, _ y: CGFloat) -> CGPoint { CGPoint(x: x, y: y) }

// --- the colours ---

let lightInk = [RGB(0x2F6BF0), RGB(0x6A4BEA), RGB(0xB23FD0), RGB(0xE8437E), RGB(0xF2682E), RGB(0xF59A23)]
let darkInk = [RGB(0x57A0FF), RGB(0x8C6CFF), RGB(0xD45BE0), RGB(0xFF5F92), RGB(0xFF7A4D), RGB(0xFFC14D)]
let lightBoard = (top: RGB(0xFFFFFF), bottom: RGB(0xE8ECF3))
let darkBoard = (top: RGB(0x262B45), bottom: RGB(0x0E0F18))

func ramp(_ stops: [RGB], _ t: CGFloat) -> RGB {
	let x = max(0, min(0.9999, t)) * CGFloat(stops.count - 1)
	let i = Int(x)
	return stops[i].mix(stops[i + 1], x - CGFloat(i))
}

// --- the stroke, in a 1024 space whose tile runs from 100 to 924 ---

/// Catmull-Rom through knots, sampled evenly every `step`.
func spline(_ knots: [CGPoint], step: CGFloat = 0.75) -> [CGPoint] {
	var dense: [CGPoint] = []
	for i in 0..<(knots.count - 1) {
		let p0 = knots[max(i - 1, 0)], p1 = knots[i], p2 = knots[i + 1], p3 = knots[min(i + 2, knots.count - 1)]
		func at(_ a: CGFloat, _ b: CGFloat, _ c: CGFloat, _ d: CGFloat, _ t: CGFloat) -> CGFloat {
			0.5 * (2 * b + (c - a) * t + (2 * a - 5 * b + 4 * c - d) * t * t + (3 * b - a - 3 * c + d) * t * t * t)
		}
		for j in 0..<200 {
			let t = CGFloat(j) / 200
			dense.append(P(at(p0.x, p1.x, p2.x, p3.x, t), at(p0.y, p1.y, p2.y, p3.y, t)))
		}
	}
	dense.append(knots[knots.count - 1])
	var out = [dense[0]]
	var run: CGFloat = 0
	for i in 1..<dense.count {
		run += hypot(dense[i].x - dense[i - 1].x, dense[i].y - dense[i - 1].y)
		if run >= step {
			out.append(dense[i])
			run = 0
		}
	}
	return out
}

let stemTop = P(556, 236)
let tip = P(806, 398)

/// The bowl: round a tilted ellipse from where it meets the stem, then back
/// up the stem to its top, where the pen turns round.
let bowl: [CGPoint] = {
	let center = P(414, 384), rx: CGFloat = 148, ry: CGFloat = 150, tilt: CGFloat = -0.1
	var knots: [CGPoint] = []
	for i in 0...12 {
		let a = (-22 - 316 * CGFloat(i) / 12) * .pi / 180
		let x = cos(a) * rx, y = sin(a) * ry
		knots.append(P(center.x + x * cos(tilt) - y * sin(tilt), center.y + x * sin(tilt) + y * cos(tilt)))
	}
	return spline(knots + [P(562, 320), stemTop])
}()

/// Down the stem, round the descender's loop, back across the stem, and away.
let tail = spline([
	stemTop, P(566, 400), P(568, 566), P(544, 696), P(484, 784), P(406, 820), P(330, 802), P(294, 742),
	P(332, 680), P(430, 636), P(556, 590), P(686, 504), tip,
])

/// Where the tail splits: its last pass starts in the loop, out of sight of
/// the shadow it drops as it crosses the stem.
let split = tail.firstIndex { $0.x < 300 && $0.y < 745 }!
let stem = Array(tail[...split])
let exit = Array(tail[split...])

/// How hard the pen presses along the whole stroke: heavy on the way down,
/// light on the way up. The keys are positions along the stroke.
let pressure: [(CGFloat, CGFloat)] = [
	(0, 54), (0.14, 66), (0.28, 56), (0.40, 58), (0.50, 70), (0.60, 78), (0.72, 60), (0.82, 50), (0.92, 50), (1, 56),
]

func press(_ t: CGFloat) -> CGFloat {
	for i in 1..<pressure.count where t <= pressure[i].0 {
		let (t0, w0) = pressure[i - 1], (t1, w1) = pressure[i]
		let u = (t - t0) / (t1 - t0)
		return w0 + (w1 - w0) * (u * u * (3 - 2 * u))
	}
	return pressure[pressure.count - 1].1
}

/// The arrowhead's barbs, each swept back from the tip with a slight curve.
let barbs: [[CGPoint]] = {
	let from = exit[exit.count - 60]
	let heading = atan2(tip.y - from.y, tip.x - from.x)
	return [CGFloat(1), -1].map { side in
		func back(_ turn: CGFloat, _ len: CGFloat) -> CGPoint {
			let a = heading + .pi + side * turn
			return P(tip.x + cos(a) * len, tip.y + sin(a) * len)
		}
		return spline([back(0.76, 122), back(0.70, 60), tip])
	}
}()

func pen(_ c: CGContext, _ path: [CGPoint], from: CGFloat, to: CGFloat, _ color: (CGFloat) -> CGColor, _ width: (CGFloat) -> CGFloat) {
	let n = CGFloat(path.count - 1)
	for (i, p) in path.enumerated() {
		let t = from + (to - from) * CGFloat(i) / n
		let w = width(t)
		c.setFillColor(color(t))
		c.fillEllipse(in: CGRect(x: p.x - w / 2, y: p.y - w / 2, width: w, height: w))
	}
}

/// A transparent bitmap with y down, in a 1024 space.
func bitmap(_ px: Int, _ draw: (CGContext) -> Void) -> CGImage {
	let c = CGContext(data: nil, width: px, height: px, bitsPerComponent: 8, bytesPerRow: 0, space: srgb,
		bitmapInfo: CGImageAlphaInfo.premultipliedLast.rawValue)!
	c.interpolationQuality = .high
	c.translateBy(x: 0, y: CGFloat(px))
	c.scaleBy(x: CGFloat(px) / 1024, y: -CGFloat(px) / 1024)
	draw(c)
	return c.makeImage()!
}

/// Draws an image the right way up in a bitmap, whose y runs down.
func place(_ img: CGImage, in c: CGContext) {
	c.saveGState()
	c.translateBy(x: 0, y: 1024)
	c.scaleBy(x: 1, y: -1)
	c.draw(img, in: CGRect(x: 0, y: 0, width: 1024, height: 1024))
	c.restoreGState()
}

/// The three passes of the pen, each laid over the last with a soft shadow
/// that only falls on the ink beneath it, so the crossings read as over and under.
func stroke(_ inks: [RGB], px: Int, transform: CGAffineTransform = .identity) -> CGImage {
	let lengths = [bowl, stem, exit].map { CGFloat($0.count) }
	let total = lengths.reduce(0, +)
	let t1 = lengths[0] / total, t2 = (lengths[0] + lengths[1]) / total
	let color: (CGFloat) -> CGColor = { ramp(inks, $0).cg }
	func pass(_ draw: @escaping (CGContext) -> Void) -> CGImage {
		bitmap(px) { c in
			c.concatenate(transform)
			draw(c)
		}
	}
	let passes = [
		pass { pen($0, bowl, from: 0, to: t1, color, press) },
		pass { pen($0, stem, from: t1, to: t2, color, press) },
		pass { c in
			pen(c, exit, from: t2, to: 1, color, press)
			for barb in barbs {
				pen(c, barb, from: 1, to: 1, color, { _ in 56 })
			}
		},
	]
	return bitmap(px) { c in
		for (i, ink) in passes.enumerated() {
			if i > 0 {
				c.saveGState()
				// The exit's shadow falls only on the stem, never on the loop it leaves.
				if i == 2 {
					c.concatenate(transform)
					c.clip(to: CGRect(x: 470, y: 0, width: 554, height: 1024))
					c.concatenate(transform.inverted())
				}
				c.setBlendMode(.sourceAtop)
				c.setShadow(offset: CGSize(width: 0, height: -CGFloat(px) * 0.006), blur: CGFloat(px) * 0.022, color: RGB(0x1A0B4A).a(0.34))
				place(ink, in: c)
				c.restoreGState()
			}
			place(ink, in: c)
		}
	}
}

func scaled(_ img: CGImage, _ px: Int) -> CGImage {
	var cur = img
	while cur.width >= px * 2 {
		cur = bitmap(cur.width / 2) { place(cur, in: $0) }
	}
	return cur.width == px ? cur : bitmap(px) { place(cur, in: $0) }
}

func save(_ img: CGImage, _ path: String) {
	let d = CGImageDestinationCreateWithURL(URL(fileURLWithPath: path) as CFURL, UTType.png.identifier as CFString, 1, nil)!
	CGImageDestinationAddImage(d, img, nil)
	if !CGImageDestinationFinalize(d) {
		fatalError("could not write \(path)")
	}
}

// --- the flat icon, for Linux, Windows and development builds ---

/// Apple's continuous corner, the shape of the macOS icon tile.
func squircle(_ rect: CGRect, _ r: CGFloat) -> CGPath {
	let p = CGMutablePath()
	let k: [CGFloat] = [1.52866483, 1.08849296, 0.86840694, 0.63149379, 0.07491139, 0.37282383, 0.16905956]
	func corner(_ f: (CGFloat, CGFloat) -> CGPoint) {
		p.addLine(to: f(k[0], 0))
		p.addCurve(to: f(k[3], k[4]), control1: f(k[1], 0), control2: f(k[2], 0))
		p.addCurve(to: f(k[4], k[3]), control1: f(k[5], k[6]), control2: f(k[6], k[5]))
		p.addCurve(to: f(0, k[0]), control1: f(0, k[2]), control2: f(0, k[1]))
	}
	p.move(to: P(rect.minX + k[0] * r, rect.minY))
	corner { P(rect.maxX - $0 * r, rect.minY + $1 * r) }
	corner { P(rect.maxX - $1 * r, rect.maxY - $0 * r) }
	corner { P(rect.minX + $0 * r, rect.maxY - $1 * r) }
	corner { P(rect.minX + $1 * r, rect.minY + $0 * r) }
	p.closeSubpath()
	return p
}

func flat() -> CGImage {
	let tile = squircle(CGRect(x: 100, y: 100, width: 824, height: 824), 185.4)
	let ink = stroke(lightInk, px: 3072)
	return bitmap(3072) { c in
		c.saveGState()
		c.setShadow(offset: CGSize(width: 0, height: -36), blur: 84, color: RGB(0).a(0.32))
		c.addPath(tile)
		c.setFillColor(lightBoard.top.cg)
		c.fillPath()
		c.restoreGState()

		c.saveGState()
		c.addPath(tile)
		c.clip()
		let g = CGGradient(colorsSpace: srgb, colors: [lightBoard.top.cg, lightBoard.bottom.cg] as CFArray, locations: [0, 1])!
		c.drawLinearGradient(g, start: P(512, 100), end: P(512, 924), options: [])
		c.saveGState()
		c.setShadow(offset: CGSize(width: 0, height: -72), blur: 150, color: RGB(0x3A2FA0).a(0.26))
		place(ink, in: c)
		c.restoreGState()
		c.restoreGState()
	}
}

let root = URL(fileURLWithPath: #filePath).deletingLastPathComponent().deletingLastPathComponent().path
save(scaled(flat(), 1024), root + "/resources/icon.png")

// In the .icon the tile fills the canvas and the system adds its own shadows.
let fill = CGAffineTransform(scaleX: 1024 / 824, y: 1024 / 824).translatedBy(x: -100, y: -100)
for (name, inks) in [("stroke", lightInk), ("stroke-dark", darkInk)] {
	save(scaled(stroke(inks, px: 3072, transform: fill), 1024), root + "/icon/AppIcon.icon/Assets/\(name).png")
}

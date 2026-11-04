package main

import (
	"image"
	"image/color"
	"math"
)

// TailSplit's snake on a 64-unit grid: its body is the S and its forked tail the
// route splitting in two. The outline starts at the nose and each curve is a
// cubic Bézier from the previous point; the eye is a round hole.
// scripts/make-icons.swift (macOS app icon) and packaging/windows/build.py
// (Windows app icon) draw the same geometry.
var (
	markStart  = [2]float64{53.4, 21.68}
	markCurves = [][3][2]float64{
		{{52.9, 22.65}, {51.94, 23.25}, {50.89, 23.49}},
		{{46.01, 24.6}, {43.48, 20.21}, {39.58, 18.56}},
		{{36.94, 17.45}, {34.01, 17.28}, {31.18, 17.37}},
		{{28.78, 17.45}, {26.27, 18.1}, {24.6, 19.93}},
		{{19.47, 25.57}, {30.86, 27.54}, {34.03, 28.41}},
		{{39.04, 29.8}, {45.05, 32.06}, {47.15, 37.29}},
		{{48.15, 39.81}, {47.99, 42.69}, {47.04, 45.2}},
		{{45.28, 49.83}, {40.8, 52.56}, {36.03, 53.12}},
		{{34.32, 53.32}, {32.58, 53.24}, {30.87, 53.33}},
		{{28.75, 53.44}, {26.64, 53.64}, {24.54, 53.9}},
		{{19.38, 54.53}, {14.28, 55.52}, {9.22, 56.66}},
		{{15.21, 53.26}, {21.56, 51.11}, {28.22, 49.44}},
		{{21.03, 46.73}, {14.91, 42.28}, {9.22, 37.19}},
		{{14.49, 39.48}, {19.52, 42.29}, {24.98, 44.12}},
		{{28.28, 45.23}, {32.2, 46.35}, {35.7, 45.67}},
		{{37.15, 45.38}, {38.65, 44.68}, {39.42, 43.36}},
		{{44.7, 34.38}, {14.31, 38.99}, {15.03, 22.36}},
		{{15.19, 18.56}, {17.15, 15.03}, {20.16, 12.74}},
		{{25.74, 8.52}, {33.87, 9.15}, {40.21, 11.26}},
		{{41.74, 11.77}, {43.13, 12.62}, {44.64, 13.13}},
		{{46.06, 13.61}, {47.58, 13.49}, {49, 13.95}},
		{{51.97, 14.92}, {55.06, 18.45}, {53.4, 21.68}},
	}
	markEye = [3]float64{48.7, 16.7, 1.9} // Center and radius.
	// The tile's single color, the configuration panel's canvas.
	markTile = [3]float64{28, 28, 30}
	// The snake's color while connected, the configuration panel's --good.
	markConnected = [3]float64{48, 209, 88}
)

const (
	// markRound softens the outline's tips, growing it by this many grid units.
	markRound = 0.5
	// markEyeSize is the smallest image with an eye; below it the hole only blurs.
	markEyeSize = 32
)

// markImage draws the mark with antialiased coverage. A tile icon puts a white
// mark on the app's dark rounded square; otherwise it is a black macOS template
// image. A connected mark is green on either. Small sizes enlarge the mark and
// keep a legible minimum tail width.
func markImage(size int, tile, connected bool) *image.NRGBA {
	s := float64(size)
	margin := math.Max(0.5, s*0.03)
	side := s - 2*margin
	scale, centerX, centerY := side/64, 32.0, 32.0
	ink := [3]float64{255, 255, 255}
	if !tile {
		// Without a tile, the 48-unit mark fills the image.
		scale, centerX, centerY = (s-2)/48, 31.5, 33.2
		ink = [3]float64{}
	} else if size <= 24 {
		scale *= 1.1
	}
	if connected {
		ink = markConnected
	}
	grow := math.Max(markRound*scale, 0.35)
	originX, originY := s/2-centerX*scale, s/2-centerY*scale
	segments := markSegments(originX, originY, scale)
	canvas := image.NewNRGBA(image.Rect(0, 0, size, size))
	for y := 0; y < size; y++ {
		for x := 0; x < size; x++ {
			px, py := float64(x)+0.5, float64(y)+0.5
			distance := outlineDistance(px, py, segments) - grow
			if size >= markEyeSize {
				// Growing the outline closes the hole by the same amount.
				eye := math.Hypot(px-originX-markEye[0]*scale, py-originY-markEye[1]*scale)
				distance = math.Max(distance, markEye[2]*scale-grow-eye)
			}
			mark := coverage(distance)
			if !tile {
				canvas.SetNRGBA(x, y, color.NRGBA{channel(ink[0]), channel(ink[1]), channel(ink[2]), channel(255 * mark)})
				continue
			}
			half, radius := side/2, side*0.22
			qx, qy := math.Abs(px-margin-half)-half+radius, math.Abs(py-margin-half)-half+radius
			edge := coverage(math.Hypot(math.Max(qx, 0), math.Max(qy, 0)) + math.Min(math.Max(qx, qy), 0) - radius)
			mix := func(axis int) uint8 { return channel(markTile[axis] + (ink[axis]-markTile[axis])*mark) }
			canvas.SetNRGBA(x, y, color.NRGBA{mix(0), mix(1), mix(2), channel(255 * edge)})
		}
	}
	return canvas
}

// markSegments flattens the closed outline into straight pixel segments.
func markSegments(originX, originY, scale float64) [][4]float64 {
	var points [][2]float64
	from := markStart
	for _, curve := range markCurves {
		for step := 1; step <= 12; step++ {
			t := float64(step) / 12
			u := 1 - t
			var point [2]float64
			for axis := range point {
				point[axis] = u*u*u*from[axis] + 3*u*u*t*curve[0][axis] + 3*u*t*t*curve[1][axis] + t*t*t*curve[2][axis]
			}
			points = append(points, [2]float64{originX + point[0]*scale, originY + point[1]*scale})
		}
		from = curve[2]
	}
	segments := make([][4]float64, len(points))
	previous := points[len(points)-1]
	for i, point := range points {
		segments[i] = [4]float64{previous[0], previous[1], point[0], point[1]}
		previous = point
	}
	return segments
}

// outlineDistance is the signed distance to the outline, negative inside it.
func outlineDistance(px, py float64, segments [][4]float64) float64 {
	distance, inside := math.Inf(1), false
	for _, segment := range segments {
		distance = math.Min(distance, segmentDistance(px, py, segment))
		x0, y0, x1, y1 := segment[0], segment[1], segment[2], segment[3]
		if (y0 <= py) != (y1 <= py) && px < x0+(py-y0)*(x1-x0)/(y1-y0) {
			inside = !inside
		}
	}
	if inside {
		return -distance
	}
	return distance
}

func coverage(distance float64) float64 { return math.Max(0, math.Min(1, 0.5-distance)) }

func channel(value float64) uint8 { return uint8(math.Round(value)) }

func segmentDistance(px, py float64, segment [4]float64) float64 {
	dx, dy := segment[2]-segment[0], segment[3]-segment[1]
	t := math.Max(0, math.Min(1, ((px-segment[0])*dx+(py-segment[1])*dy)/(dx*dx+dy*dy)))
	return math.Hypot(px-segment[0]-t*dx, py-segment[1]-t*dy)
}

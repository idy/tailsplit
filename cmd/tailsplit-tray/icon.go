package main

import (
	"image"
	"image/color"
	"math"
)

// TailSplit's split-route mark on a 64-unit grid: a source whose route splits
// toward two destinations. scripts/make-icons.swift (macOS app icon) and
// packaging/windows/build.py (Windows app icon) draw the same geometry.
var (
	markDots   = [][2]float64{{15, 32}, {49, 17}, {49, 47}}
	markRoutes = [][][2]float64{
		{{15, 32}, {26, 32}},
		{{26, 32}, {36, 32}, {37, 17}, {49, 17}},
		{{26, 32}, {36, 32}, {37, 47}, {49, 47}},
	}
)

const (
	markDotRadius   = 5.25
	markStrokeWidth = 4.5
)

// markImage draws the mark with antialiased coverage. A tile icon puts a white
// mark on the app's blue rounded square; otherwise it is a black macOS
// template image. Small sizes keep a legible minimum dot and line width.
func markImage(size int, tile bool) *image.NRGBA {
	s := float64(size)
	margin := math.Max(0.5, s*0.03)
	side := s - 2*margin
	scale, originX, originY := side/64, margin, margin
	if !tile {
		// Without a tile, the 44.5 by 40.5-unit mark fills the image.
		scale = (s - 2) / 44.5
		originX, originY = s/2-32*scale, s/2-32*scale
	}
	dot := math.Max(markDotRadius*scale, 1.75)
	stroke := math.Max(markStrokeWidth*scale/2, 0.8)
	var segments [][4]float64
	for _, route := range markRoutes {
		points := route
		if len(route) == 4 {
			points = nil
			for step := 0; step <= 24; step++ {
				t := float64(step) / 24
				u := 1 - t
				var point [2]float64
				for axis := range point {
					point[axis] = u*u*u*route[0][axis] + 3*u*u*t*route[1][axis] + 3*u*t*t*route[2][axis] + t*t*t*route[3][axis]
				}
				points = append(points, point)
			}
		}
		for i := 1; i < len(points); i++ {
			segments = append(segments, [4]float64{originX + points[i-1][0]*scale, originY + points[i-1][1]*scale,
				originX + points[i][0]*scale, originY + points[i][1]*scale})
		}
	}
	canvas := image.NewNRGBA(image.Rect(0, 0, size, size))
	for y := 0; y < size; y++ {
		for x := 0; x < size; x++ {
			px, py := float64(x)+0.5, float64(y)+0.5
			distance := math.Inf(1)
			for _, center := range markDots {
				distance = math.Min(distance, math.Hypot(px-originX-center[0]*scale, py-originY-center[1]*scale)-dot)
			}
			for _, segment := range segments {
				distance = math.Min(distance, segmentDistance(px, py, segment)-stroke)
			}
			mark := coverage(distance)
			if !tile {
				canvas.SetNRGBA(x, y, color.NRGBA{A: channel(255 * mark)})
				continue
			}
			half, radius := side/2, side*0.22
			qx, qy := math.Abs(px-margin-half)-half+radius, math.Abs(py-margin-half)-half+radius
			edge := coverage(math.Hypot(math.Max(qx, 0), math.Max(qy, 0)) + math.Min(math.Max(qx, qy), 0) - radius)
			// A diagonal blue-to-indigo gradient behind the white mark.
			t := math.Max(0, math.Min(1, (px+py-2*margin)/(2*side)))
			mix := func(from, to float64) uint8 {
				background := from + (to-from)*t
				return channel(background + (255-background)*mark)
			}
			canvas.SetNRGBA(x, y, color.NRGBA{mix(91, 53), mix(141, 84), mix(255, 230), channel(255 * edge)})
		}
	}
	return canvas
}

func coverage(distance float64) float64 { return math.Max(0, math.Min(1, 0.5-distance)) }

func channel(value float64) uint8 { return uint8(math.Round(value)) }

func segmentDistance(px, py float64, segment [4]float64) float64 {
	dx, dy := segment[2]-segment[0], segment[3]-segment[1]
	t := math.Max(0, math.Min(1, ((px-segment[0])*dx+(py-segment[1])*dy)/(dx*dx+dy*dy)))
	return math.Hypot(px-segment[0]-t*dx, py-segment[1]-t*dy)
}

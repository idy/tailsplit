package main

import (
	"image/color"
	"testing"
)

func TestMarkImageDrawsAntialiasedTileAndTemplate(t *testing.T) {
	for _, tile := range []bool{true, false} {
		for _, size := range []int{16, 32, 256} {
			icon := markImage(size, tile, false)
			if icon.Bounds().Dx() != size || icon.Bounds().Dy() != size {
				t.Fatalf("size %d: wrong bounds %v", size, icon.Bounds())
			}
			if icon.NRGBAAt(0, 0).A != 0 || icon.NRGBAAt(size-1, size-1).A != 0 {
				t.Fatalf("size %d tile=%v: corners are not transparent", size, tile)
			}
			partial := 0
			for y := 0; y < size; y++ {
				for x := 0; x < size; x++ {
					if alpha := icon.NRGBAAt(x, y).A; alpha > 0 && alpha < 255 {
						partial++
					}
				}
			}
			if partial == 0 {
				t.Fatalf("size %d tile=%v: edges are not antialiased", size, tile)
			}
		}
	}
	// Both tail prongs are solid white on the one-color tile, which shows through
	// the fork between them and through the eye.
	white, background := color.NRGBA{255, 255, 255, 255}, color.NRGBA{28, 28, 30, 255}
	scale := (64 - 2*64*0.03) / 64
	tile := markImage(64, true, false)
	at := func(x, y float64) color.NRGBA { return tile.NRGBAAt(int(32+(x-32)*scale), int(32+(y-32)*scale)) }
	if upper, lower := at(22, 44.8), at(22, 52.6); upper != white || lower != white {
		t.Fatalf("tail prongs are %v and %v", upper, lower)
	}
	if fork, eye, corner := at(16, 48), at(markEye[0], markEye[1]), tile.NRGBAAt(32, 4); fork != background || eye != background || corner != background {
		t.Fatalf("fork is %v, eye is %v and tile background is %v", fork, eye, corner)
	}
	// Smaller icons keep the head solid because the eye's hole would only blur.
	if head := markImage(24, true, false).NRGBAAt(18, 6); head != white {
		t.Fatalf("small icon head is %v", head)
	}
	template := markImage(32, false, false)
	if upper, fork, lower := template.NRGBAAt(9, 22).A, template.NRGBAAt(9, 25).A, template.NRGBAAt(10, 28).A; upper != 255 || fork != 0 || lower != 255 {
		t.Fatalf("template tail alpha is %d, %d, %d", upper, fork, lower)
	}
}

func TestMarkImageIsGreenWhileConnected(t *testing.T) {
	green, background := color.NRGBA{48, 209, 88, 255}, color.NRGBA{28, 28, 30, 255}
	// The menu bar image carries its own color, so macOS must not tint it.
	template := markImage(32, false, true)
	if tail, fork := template.NRGBAAt(9, 22), template.NRGBAAt(9, 25); tail != green || fork.A != 0 {
		t.Fatalf("connected tail is %v and fork is %v", tail, fork)
	}
	// Only the snake changes on the tile, whose color stays the same.
	scale := (64 - 2*64*0.03) / 64
	tile := markImage(64, true, true)
	tail := tile.NRGBAAt(int(32+(22-32)*scale), int(32+(44.8-32)*scale))
	if corner := tile.NRGBAAt(32, 4); tail != green || corner != background {
		t.Fatalf("connected tail is %v and tile background is %v", tail, corner)
	}
}

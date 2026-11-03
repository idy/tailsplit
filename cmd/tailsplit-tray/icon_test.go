package main

import (
	"image/color"
	"testing"
)

func TestMarkImageDrawsAntialiasedTileAndTemplate(t *testing.T) {
	for _, tile := range []bool{true, false} {
		for _, size := range []int{16, 32, 256} {
			icon := markImage(size, tile)
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
	// The source dot is solid white on the tile, which stays blue around it.
	margin := 64 * 0.03
	scale := (64 - 2*margin) / 64
	tile := markImage(64, true)
	if pixel := tile.NRGBAAt(int(margin+15*scale), int(margin+32*scale)); pixel != (color.NRGBA{255, 255, 255, 255}) {
		t.Fatalf("source dot is %v", pixel)
	}
	if pixel := tile.NRGBAAt(32, 4); pixel.A != 255 || pixel.B < 200 || pixel.R > 120 {
		t.Fatalf("tile background is %v", pixel)
	}
	if pixel := markImage(32, false).NRGBAAt(4, 15); pixel.A != 255 {
		t.Fatalf("template source dot is %v", pixel)
	}
}

package desktoptray

import (
	"bytes"
	"image"
	"image/draw"
	"image/png"
)

// normalizeIcon validates and copies the PNG supplied by either desktop entry
// point. Decode the entire image so a valid header cannot hide corrupt pixels.
func normalizeIcon(data []byte) ([]byte, error) {
	if _, err := png.Decode(bytes.NewReader(data)); err != nil {
		return nil, err
	}
	return bytes.Clone(data), nil
}

// MyGo renders an 18pt NSImage; the previous tray rendered our 32px artwork
// at 16pt. A transparent 36px canvas retains that exact artwork and scale.
func macTrayIcon(data []byte) ([]byte, error) {
	art, err := png.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	bounds := art.Bounds()
	width, height := (bounds.Dx()*9+7)/8, (bounds.Dy()*9+7)/8
	canvas := image.NewNRGBA(image.Rect(0, 0, width, height))
	inset := image.Pt((width-bounds.Dx())/2, (height-bounds.Dy())/2)
	draw.Draw(canvas, bounds.Sub(bounds.Min).Add(inset), art, bounds.Min, draw.Src)
	var output bytes.Buffer
	if err := png.Encode(&output, canvas); err != nil {
		return nil, err
	}
	return output.Bytes(), nil
}

package desktoptray

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"runtime"
	"testing"
)

func testPNG(t *testing.T, size int) []byte {
	t.Helper()
	img := image.NewNRGBA(image.Rect(0, 0, size, size))
	img.SetNRGBA(0, 0, color.NRGBA{R: 23, G: 81, B: 142, A: 193})
	var out bytes.Buffer
	if err := png.Encode(&out, img); err != nil {
		t.Fatal(err)
	}
	return out.Bytes()
}

func TestNormalizeIconPreservesPNG(t *testing.T) {
	for _, size := range []int{32, 64} {
		want := testPNG(t, size)
		input := bytes.Clone(want)
		got, err := normalizeIcon(input)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(got, want) {
			t.Fatal("normalization altered PNG artwork")
		}
		clear(input)
		if !bytes.Equal(got, want) {
			t.Fatal("normalized icon aliases caller-owned memory")
		}
	}
}

func TestMacTrayIconRetainsSixteenPointArtwork(t *testing.T) {
	original, _ := png.Decode(bytes.NewReader(testPNG(t, 32)))
	data, err := macTrayIcon(testPNG(t, 32))
	if err != nil {
		t.Fatal(err)
	}
	result, err := png.Decode(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	if result.Bounds() != image.Rect(0, 0, 36, 36) {
		t.Fatal("wrong 18pt canvas", result.Bounds())
	}
	for y := 0; y < 36; y++ {
		for x := 0; x < 36; x++ {
			want := color.NRGBAModel.Convert(original.At(x-2, y-2))
			if color.NRGBAModel.Convert(result.At(x, y)) != want {
				t.Fatalf("artwork altered at %d,%d", x, y)
			}
		}
	}
}

func TestNormalizeIconRejectsInvalidPNGAndICO(t *testing.T) {
	valid := testPNG(t, 64)
	corrupt := bytes.Clone(valid)
	corrupt[len(corrupt)-1] ^= 0xff
	for _, test := range []struct {
		name string
		data []byte
	}{
		{"empty", nil},
		{"not an image", []byte("not an icon")},
		{"PNG signature only", valid[:8]},
		{"PNG header without pixels", valid[:33]},
		{"corrupt PNG checksum", corrupt},
		{"ICO header", []byte{0, 0, 1, 0, 1, 0}},
	} {
		t.Run(test.name, func(t *testing.T) {
			if _, err := normalizeIcon(test.data); err == nil {
				t.Fatal("accepted data that is not a complete PNG")
			}
		})
	}
}

func TestInitialSnapshotConsumesPreseededPNGAndRetainsItAfterInvalidUpdate(t *testing.T) {
	s := newMenuState()
	input := testPNG(t, 32)
	want := bytes.Clone(input)
	if runtime.GOOS == "darwin" {
		var err error
		want, err = macTrayIcon(want)
		if err != nil {
			t.Fatal(err)
		}
	}
	setIcon(s, input, true)
	clear(input)
	initial := s.takeSnapshot(allChanged)
	if !initial.template || !bytes.Equal(initial.icon, want) {
		t.Fatal("initial snapshot lost the preseeded PNG or template flag")
	}
	setIcon(s, []byte("invalid PNG"), false)
	if got := s.takeSnapshot(0); got.changed != 0 {
		t.Fatalf("invalid icon dirtied the valid initial state: %b", got.changed)
	}
	if got := s.takeSnapshot(iconChanged); !got.template || !bytes.Equal(got.icon, want) {
		t.Fatal("invalid update replaced the preseeded icon")
	}
}

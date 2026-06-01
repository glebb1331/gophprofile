package imageproc

import (
	"bytes"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"testing"

	"github.com/stretchr/testify/require"
)

func makePNG(t *testing.T, w, h int) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for x := 0; x < w; x++ {
		for y := 0; y < h; y++ {
			img.Set(x, y, color.RGBA{R: uint8(x), G: uint8(y), B: 100, A: 255})
		}
	}
	buf := bytes.NewBuffer(nil)
	require.NoError(t, png.Encode(buf, img))
	return buf.Bytes()
}

func makeJPEG(t *testing.T, w, h int) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	buf := bytes.NewBuffer(nil)
	require.NoError(t, jpeg.Encode(buf, img, &jpeg.Options{Quality: 80}))
	return buf.Bytes()
}

func TestDecodePNG(t *testing.T) {
	data := makePNG(t, 80, 60)
	dec, err := Decode(bytes.NewReader(data))
	require.NoError(t, err)
	require.Equal(t, "png", dec.Format)
	require.Equal(t, 80, dec.Width)
	require.Equal(t, 60, dec.Height)
}

func TestDecodeJPEG(t *testing.T) {
	data := makeJPEG(t, 120, 90)
	dec, err := Decode(bytes.NewReader(data))
	require.NoError(t, err)
	require.Equal(t, "jpeg", dec.Format)
}

func TestDecodeInvalid(t *testing.T) {
	_, err := Decode(bytes.NewReader([]byte("not an image")))
	require.Error(t, err)
}

func TestResize(t *testing.T) {
	data := makePNG(t, 200, 200)
	dec, err := Decode(bytes.NewReader(data))
	require.NoError(t, err)
	resized := Resize(dec.Image, 50, 50)
	require.Equal(t, 50, resized.Bounds().Dx())
	require.Equal(t, 50, resized.Bounds().Dy())
}

func TestEncodeJPEGAndPNG(t *testing.T) {
	data := makePNG(t, 40, 40)
	dec, err := Decode(bytes.NewReader(data))
	require.NoError(t, err)
	out, err := EncodeJPEG(dec.Image, 90)
	require.NoError(t, err)
	require.NotEmpty(t, out)
	// JPEG SOI marker.
	require.Equal(t, byte(0xFF), out[0])
	require.Equal(t, byte(0xD8), out[1])

	pngBytes, err := EncodePNG(dec.Image)
	require.NoError(t, err)
	require.NotEmpty(t, pngBytes)
	require.Equal(t, byte(0x89), pngBytes[0])
}

func TestIsAllowedMime(t *testing.T) {
	allowed := []string{"image/jpeg", "image/png"}
	require.True(t, IsAllowedMime("image/png", allowed))
	require.False(t, IsAllowedMime("image/gif", allowed))
}

func TestDetectMime(t *testing.T) {
	data := makePNG(t, 10, 10)
	head := data
	if len(head) > 512 {
		head = head[:512]
	}
	require.Equal(t, "image/png", DetectMime(head))
}

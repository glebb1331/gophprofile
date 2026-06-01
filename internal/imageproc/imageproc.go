package imageproc

import (
	"bytes"
	"errors"
	"fmt"
	"image"
	"image/jpeg"
	"image/png"
	"io"
	"net/http"

	"github.com/disintegration/imaging"
	_ "golang.org/x/image/webp"
)

var ErrUnsupportedFormat = errors.New("unsupported image format")

type DecodedImage struct {
	Image  image.Image
	Format string
	Width  int
	Height int
}

func Decode(r io.Reader) (*DecodedImage, error) {
	img, format, err := image.Decode(r)
	if err != nil {
		return nil, fmt.Errorf("decode image: %w", err)
	}
	bounds := img.Bounds()
	return &DecodedImage{
		Image:  img,
		Format: format,
		Width:  bounds.Dx(),
		Height: bounds.Dy(),
	}, nil
}

func Resize(src image.Image, width, height int) image.Image {
	return imaging.Fill(src, width, height, imaging.Center, imaging.Lanczos)
}

func EncodeJPEG(img image.Image, quality int) ([]byte, error) {
	if quality <= 0 || quality > 100 {
		quality = 85
	}
	buf := bytes.NewBuffer(nil)
	if err := jpeg.Encode(buf, img, &jpeg.Options{Quality: quality}); err != nil {
		return nil, fmt.Errorf("encode jpeg: %w", err)
	}
	return buf.Bytes(), nil
}

func EncodePNG(img image.Image) ([]byte, error) {
	buf := bytes.NewBuffer(nil)
	if err := png.Encode(buf, img); err != nil {
		return nil, fmt.Errorf("encode png: %w", err)
	}
	return buf.Bytes(), nil
}

func DetectMime(head []byte) string {
	return http.DetectContentType(head)
}

func IsAllowedMime(mime string, allowed []string) bool {
	for _, a := range allowed {
		if a == mime {
			return true
		}
	}
	return false
}

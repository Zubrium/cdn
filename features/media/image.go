package media

import (
	"bytes"
	"context"
	"fmt"
	"image"
	_ "image/gif"
	"image/jpeg"
	"image/png"
	"io"

	xdraw "golang.org/x/image/draw"
	_ "golang.org/x/image/webp"

	"websiteengine/pkg/storage"
)

var imageVariants = []struct {
	Name   string
	Target int
}{
	{Name: "thumbnail", Target: 320},
	{Name: "medium", Target: 960},
	{Name: "large", Target: 1600},
}

const maxImagePixels = 40_000_000

func inspectImage(data []byte) (image.Config, error) {
	cfg, err := decodeBoundedRasterConfig(bytes.NewReader(data))
	if err != nil {
		return image.Config{}, err
	}
	return cfg, nil
}

// ValidateRasterReader enforces the same decoded-dimension ceiling before
// original public media is persisted or served inline.
func ValidateRasterReader(reader io.Reader) error {
	_, err := decodeBoundedRasterConfig(reader)
	return err
}

func decodeBoundedRasterConfig(reader io.Reader) (image.Config, error) {
	cfg, _, err := image.DecodeConfig(reader)
	if err != nil {
		return image.Config{}, fmt.Errorf("decode image header: %w", err)
	}
	if cfg.Width <= 0 || cfg.Height <= 0 || int64(cfg.Width)*int64(cfg.Height) > maxImagePixels {
		return image.Config{}, fmt.Errorf("image dimensions are not supported")
	}
	return cfg, nil
}

func buildImageVariants(ctx context.Context, store storage.ObjectStore, id string, data []byte, contentType string) (map[string]string, error) {
	cfg, err := inspectImage(data)
	if err != nil {
		return nil, err
	}
	src, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("decode image: %w", err)
	}

	encodeFormat, ext, mime := "png", ".png", "image/png"
	if contentType == "image/jpeg" {
		encodeFormat, ext, mime = "jpeg", ".jpg", "image/jpeg"
	}

	variants := make(map[string]string, len(imageVariants))
	for _, variant := range imageVariants {
		scale := float64(variant.Target) / float64(max(cfg.Width, cfg.Height))
		if scale > 1 {
			scale = 1
		}
		width := max(1, int(float64(cfg.Width)*scale+0.5))
		height := max(1, int(float64(cfg.Height)*scale+0.5))
		dst := image.NewRGBA(image.Rect(0, 0, width, height))
		xdraw.CatmullRom.Scale(dst, dst.Bounds(), src, src.Bounds(), xdraw.Src, nil)

		var buf bytes.Buffer
		switch encodeFormat {
		case "jpeg":
			if err := jpeg.Encode(&buf, dst, &jpeg.Options{Quality: 85}); err != nil {
				return nil, fmt.Errorf("encode JPEG: %w", err)
			}
		default:
			if err := png.Encode(&buf, dst); err != nil {
				return nil, fmt.Errorf("encode PNG: %w", err)
			}
		}

		key := fmt.Sprintf("media/%s/%s%s", id, variant.Name, ext)
		if err := store.Put(ctx, key, bytes.NewReader(buf.Bytes()), int64(buf.Len()), storage.PutOptions{
			ContentType:  mime,
			CacheControl: "public, max-age=31536000, immutable",
		}); err != nil {
			return nil, err
		}
		variants[variant.Name] = key
	}
	return variants, nil
}

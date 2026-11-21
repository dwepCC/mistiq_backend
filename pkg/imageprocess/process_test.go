package imageprocess

import (
	"bytes"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"math/rand"
	"testing"
)

// Pixeles con ruido aleatorio (alta entropía) en vez de un patrón liso — se comprime mal, como
// una foto real de cámara, así el test de "el resultado pesa menos" es representativo. Un
// gradiente/patrón repetitivo comprime tan bien de entrada que reducirlo puede no bajar bytes.
func randomPixels(w, h int, seed int64) *image.NRGBA {
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	r := rand.New(rand.NewSource(seed))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.Set(x, y, color.NRGBA{uint8(r.Intn(256)), uint8(r.Intn(256)), uint8(r.Intn(256)), 255})
		}
	}
	return img
}

func solidJPEG(t *testing.T, w, h int, quality int) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, randomPixels(w, h, 1), &jpeg.Options{Quality: quality}); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func solidPNG(t *testing.T, w, h int) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := png.Encode(&buf, randomPixels(w, h, 2)); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestOptimizeJPEGDownscalesOversizedImage(t *testing.T) {
	data := solidJPEG(t, 2000, 1500, 95)
	res, ok := Optimize(data, ".jpg")
	if !ok {
		t.Fatal("esperaba ok=true para un JPEG grande")
	}
	if res.Width > MaxDimensionPx || res.Height > MaxDimensionPx {
		t.Errorf("dimensiones no reducidas: %dx%d", res.Width, res.Height)
	}
	if len(res.Data) >= len(data) {
		t.Errorf("resultado no es más liviano: original=%d optimizado=%d", len(data), len(res.Data))
	}
	if res.Extension != ".jpg" {
		t.Errorf("extensión esperada .jpg, got %s", res.Extension)
	}
}

func TestOptimizeSmallImageOnlyRecompresses(t *testing.T) {
	data := solidJPEG(t, 400, 300, 100)
	res, ok := Optimize(data, "jpg")
	if !ok {
		t.Fatal("esperaba ok=true (recompresión, sin redimensionar)")
	}
	if res.Width != 400 || res.Height != 300 {
		t.Errorf("no debía redimensionar una imagen ya chica: got %dx%d", res.Width, res.Height)
	}
}

func TestOptimizePNGPreservesFormat(t *testing.T) {
	data := solidPNG(t, 2000, 2000)
	res, ok := Optimize(data, ".png")
	if !ok {
		t.Fatal("esperaba ok=true para un PNG grande")
	}
	if res.Extension != ".png" {
		t.Errorf("extensión esperada .png, got %s", res.Extension)
	}
	if res.Width > MaxDimensionPx || res.Height > MaxDimensionPx {
		t.Errorf("dimensiones no reducidas: %dx%d", res.Width, res.Height)
	}
}

func TestOptimizeSkipsWebp(t *testing.T) {
	if _, ok := Optimize([]byte("no importa el contenido"), ".webp"); ok {
		t.Error("esperaba ok=false para webp (no soportado)")
	}
}

func TestOptimizeSkipsUnsupportedFormat(t *testing.T) {
	if _, ok := Optimize([]byte("no es una imagen"), ".gif"); ok {
		t.Error("esperaba ok=false para formato no whitelisteado")
	}
}

func TestOptimizeInvalidDataFailsGracefully(t *testing.T) {
	if _, ok := Optimize([]byte("esto no es un jpeg valido"), ".jpg"); ok {
		t.Error("esperaba ok=false para datos corruptos")
	}
}

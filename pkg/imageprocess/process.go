// Package imageprocess reduce el peso de las imágenes que suben los tenants (productos,
// contactos, etc.) antes de guardarlas en disco — evita que una foto de cámara/celular en alta
// resolución (varios MB, miles de píxeles de lado) quede tal cual en uploads/, inflando el disco
// y volviendo lentas las vistas que muestran muchas a la vez (grilla de POS, catálogo, selector
// de "Nuevo comprobante") aunque ahí se vean en una tarjeta de 40-60px.
package imageprocess

import (
	"bytes"
	"strings"

	"github.com/disintegration/imaging"
)

// MaxDimensionPx: lado más largo al que se reduce una imagen — de sobra para verla en detalle/
// zoom en la app; nunca se agranda una imagen más chica que esto (imaging.Fit no hace upscale).
const MaxDimensionPx = 1000

// JPEGQuality: calidad de recompresión para fuentes JPEG. 82 es el punto usual de "sin pérdida
// perceptible, con buena compresión" para fotos de producto.
const JPEGQuality = 82

// Result imagen procesada lista para guardar en disco.
type Result struct {
	Data      []byte
	Extension string // con el punto, ej. ".jpg" — puede diferir de la extensión original.
	Width     int
	Height    int
}

// Optimize decodifica, reduce (si hace falta) y recomprime una imagen subida por el usuario.
// ext es la extensión original en minúsculas, con o sin punto (ej. ".jpg", "png").
//
// Devuelve ok=false —y el llamador debe guardar los bytes originales sin tocar, como se hacía
// antes de este paquete— en dos casos:
//   - Formato no soportado para procesar. .webp no se procesa: imaging.Decode no lo soporta sin
//     agregar una dependencia (golang.org/x/image con soporte webp) que exige una versión de Go
//     más nueva que la fijada en go.mod — se prefiere no tocar el toolchain del proyecto por esto.
//   - El resultado optimizado no quedó más liviano que el original (imagen ya pequeña/comprimida):
//     no tiene sentido reemplazarla por una version igual o más pesada.
//
// PNG conserva su formato de salida (preserva transparencia); JPEG se recomprime a JPEGQuality.
// Una imagen ya <= MaxDimensionPx en ambos lados solo se recomprime, no se reescala — igual baja
// de peso por la recompresión aunque no haga falta redimensionarla.
func Optimize(data []byte, ext string) (Result, bool) {
	ext = strings.ToLower(strings.TrimPrefix(ext, "."))
	if ext != "jpg" && ext != "jpeg" && ext != "png" {
		return Result{}, false
	}

	img, err := imaging.Decode(bytes.NewReader(data), imaging.AutoOrientation(true))
	if err != nil {
		return Result{}, false
	}

	resized := imaging.Fit(img, MaxDimensionPx, MaxDimensionPx, imaging.Lanczos)

	var buf bytes.Buffer
	outExt := ".jpg"
	format := imaging.JPEG
	var opts []imaging.EncodeOption
	if ext == "png" {
		outExt = ".png"
		format = imaging.PNG
	} else {
		opts = append(opts, imaging.JPEGQuality(JPEGQuality))
	}
	if err := imaging.Encode(&buf, resized, format, opts...); err != nil {
		return Result{}, false
	}

	if buf.Len() >= len(data) {
		return Result{}, false
	}

	bounds := resized.Bounds()
	return Result{
		Data:      buf.Bytes(),
		Extension: outExt,
		Width:     bounds.Dx(),
		Height:    bounds.Dy(),
	}, true
}

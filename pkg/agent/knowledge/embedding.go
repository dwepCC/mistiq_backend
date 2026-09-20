package knowledge

import (
	"encoding/json"
	"math"
	"strings"
)

const defaultChunkSize = 800

// Split trocea un texto en párrafos de hasta `size` caracteres, respetando
// saltos de párrafo cuando es posible (no corta una palabra a la mitad).
func Split(content string, size int) []string {
	if size <= 0 {
		size = defaultChunkSize
	}
	content = strings.TrimSpace(content)
	if content == "" {
		return nil
	}
	paragraphs := strings.Split(content, "\n\n")
	var chunks []string
	var current strings.Builder

	flush := func() {
		if current.Len() > 0 {
			chunks = append(chunks, strings.TrimSpace(current.String()))
			current.Reset()
		}
	}

	for _, p := range paragraphs {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		if current.Len()+len(p)+2 > size {
			flush()
		}
		if len(p) > size {
			// párrafo más grande que el tamaño de chunk: cortar por palabras.
			words := strings.Fields(p)
			var piece strings.Builder
			for _, w := range words {
				if piece.Len()+len(w)+1 > size {
					chunks = append(chunks, strings.TrimSpace(piece.String()))
					piece.Reset()
				}
				if piece.Len() > 0 {
					piece.WriteByte(' ')
				}
				piece.WriteString(w)
			}
			if piece.Len() > 0 {
				chunks = append(chunks, strings.TrimSpace(piece.String()))
			}
			continue
		}
		if current.Len() > 0 {
			current.WriteString("\n\n")
		}
		current.WriteString(p)
	}
	flush()
	return chunks
}

// Cosine calcula la similitud coseno entre dos vectores de igual longitud.
// Devuelve 0 si son de longitud distinta o alguno tiene norma cero.
func Cosine(a, b []float32) float32 {
	if len(a) != len(b) || len(a) == 0 {
		return 0
	}
	var dot, normA, normB float64
	for i := range a {
		dot += float64(a[i]) * float64(b[i])
		normA += float64(a[i]) * float64(a[i])
		normB += float64(b[i]) * float64(b[i])
	}
	if normA == 0 || normB == 0 {
		return 0
	}
	return float32(dot / (math.Sqrt(normA) * math.Sqrt(normB)))
}

// EncodeEmbedding serializa un vector a JSON para guardarlo en una columna
// de texto (v1 sin motor vectorial).
func EncodeEmbedding(v []float32) (string, error) {
	raw, err := json.Marshal(v)
	if err != nil {
		return "", err
	}
	return string(raw), nil
}

// DecodeEmbedding deserializa un embedding guardado con EncodeEmbedding.
func DecodeEmbedding(s string) ([]float32, error) {
	if s == "" {
		return nil, nil
	}
	var v []float32
	if err := json.Unmarshal([]byte(s), &v); err != nil {
		return nil, err
	}
	return v, nil
}

package service

import (
	"strings"
	"unicode"

	"tukifac/pkg/database"

	"gorm.io/gorm"
)

// Cruce del nombre de una agencia con el ubigeo (departamento / provincia / distrito).
//
// Los listados de los transportistas (p. ej. el Excel de Shalom) traen solo código y nombre, y el nombre mezcla
// ciudades («ABANCAY»), direcciones («AV BOLIVAR») y a veces el departamento («CUSCO CALCA»). Se infiere la ubicación
// con tres niveles de confianza:
//   - confirmada: el nombre completo (sin ruido) coincide con un solo distrito o provincia.
//   - sugerida:   coincide solo una parte del nombre, o el nombre parece una dirección. Hay que revisarla.
//   - pendiente:  no se encontró nada.

type ubiPlace struct {
	distID, distName string
	provID, provName string
	regID, regName   string
}

type ubiIndex struct {
	places []ubiPlace
	byDist map[string][]int // nombre normalizado del distrito → lugares
	byProv map[string][]int // nombre normalizado de la provincia → lugares de esa provincia
	regs   []ubiRegion
}

type ubiRegion struct {
	id, name string
	toks     []string
}

// AgencyGuess resultado del cruce de una agencia con el ubigeo.
type AgencyGuess struct {
	Department string
	Province   string
	District   string
	Ubigeo     string
	Status     string
}

var addressPrefixes = map[string]bool{
	"av": true, "avenida": true, "jr": true, "jiron": true, "calle": true, "ca": true, "psje": true, "pasaje": true,
	"mz": true, "aahh": true, "asoc": true, "asociacion": true, "urb": true, "pj": true, "pp": true, "jj": true,
	"carretera": true, "cdra": true, "ant": true, "antigua": true, "autopista": true, "panamericana": true,
}

// ruido que no ayuda a ubicar la agencia
var noiseTokens = map[string]bool{
	"co": true, "av": true, "avenida": true, "jr": true, "jiron": true, "calle": true, "cdra": true, "cuadra": true,
	"mz": true, "lt": true, "aahh": true, "asoc": true, "asociacion": true, "urb": true, "pj": true, "pp": true,
	"jj": true, "de": true, "del": true, "la": true, "el": true, "los": true, "las": true, "y": true, "vmt": true,
}

func normTokens(s string) []string {
	s = foldText(s)
	f := strings.FieldsFunc(s, func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) })
	return f
}

func joinKey(t []string) string { return strings.Join(t, " ") }

// cleanTokens palabras de un nombre del ubigeo sin artículos ni preposiciones («La Merced» → «merced»), igual que las agencias.
func cleanTokens(s string) []string {
	var out []string
	for _, t := range normTokens(s) {
		if !noiseTokens[t] {
			out = append(out, t)
		}
	}
	return out
}

// agencyTokens devuelve las palabras útiles del nombre y si parece una dirección.
func agencyTokens(name string) (toks []string, addr bool) {
	all := normTokens(name)
	if len(all) > 0 && addressPrefixes[all[0]] {
		addr = true
	}
	for _, t := range all {
		if noiseTokens[t] || isNumber(t) {
			continue
		}
		toks = append(toks, t)
	}
	return toks, addr
}

func isNumber(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if !unicode.IsDigit(r) {
			return false
		}
	}
	return true
}

// loadUbiIndex carga el ubigeo de la BD central en memoria (≈1.900 distritos).
func loadUbiIndex(db *gorm.DB) (*ubiIndex, error) {
	var regs []database.UbiRegion
	var provs []database.UbiProvincia
	var dists []database.UbiDistrito
	if err := db.Find(&regs).Error; err != nil {
		return nil, err
	}
	if err := db.Find(&provs).Error; err != nil {
		return nil, err
	}
	if err := db.Find(&dists).Error; err != nil {
		return nil, err
	}
	regName := map[string]string{}
	ix := &ubiIndex{byDist: map[string][]int{}, byProv: map[string][]int{}}
	for _, r := range regs {
		regName[r.ID] = r.Nombre
		ix.regs = append(ix.regs, ubiRegion{id: r.ID, name: r.Nombre, toks: cleanTokens(r.Nombre)})
	}
	provName := map[string]string{}
	for _, p := range provs {
		provName[p.ID] = p.Nombre
	}
	for _, d := range dists {
		i := len(ix.places)
		ix.places = append(ix.places, ubiPlace{
			distID: d.ID, distName: d.Nombre, provID: d.ProvinciaID, provName: provName[d.ProvinciaID],
			regID: d.RegionID, regName: regName[d.RegionID],
		})
		dk := joinKey(cleanTokens(d.Nombre))
		ix.byDist[dk] = append(ix.byDist[dk], i)
		pk := joinKey(cleanTokens(provName[d.ProvinciaID]))
		ix.byProv[pk] = append(ix.byProv[pk], i)
	}
	return ix, nil
}

// stripRegion busca un departamento dentro de las palabras; devuelve su id y las palabras restantes.
func (ix *ubiIndex) stripRegion(toks []string) (string, []string) {
	bestID, bestStart, bestLen := "", -1, 0
	for _, r := range ix.regs {
		n := len(r.toks)
		if n == 0 || n > len(toks) {
			continue
		}
		for s := 0; s+n <= len(toks); s++ {
			if joinKey(toks[s:s+n]) == joinKey(r.toks) && n > bestLen {
				bestID, bestStart, bestLen = r.id, s, n
			}
		}
	}
	if bestID == "" {
		return "", toks
	}
	rest := append(append([]string{}, toks[:bestStart]...), toks[bestStart+bestLen:]...)
	return bestID, rest
}

func (ix *ubiIndex) guessFromPlace(p ubiPlace, status string) AgencyGuess {
	return AgencyGuess{Department: p.regName, Province: p.provName, District: p.distName, Ubigeo: p.distID, Status: status}
}

// capital elige el distrito «capital» de una provincia (el que se llama igual; si no, el que termina en 01).
func (ix *ubiIndex) capital(idx []int) (ubiPlace, bool) {
	for _, i := range idx {
		p := ix.places[i]
		if joinKey(cleanTokens(p.distName)) == joinKey(cleanTokens(p.provName)) {
			return p, true
		}
	}
	for _, i := range idx {
		if strings.HasSuffix(ix.places[i].distID, "01") {
			return ix.places[i], true
		}
	}
	return ubiPlace{}, false
}

// exact busca un nombre completo como distrito y, si no, como provincia (→ su capital). Solo acepta resultados únicos.
func (ix *ubiIndex) exact(toks []string, regID string) (ubiPlace, bool) {
	if len(toks) == 0 {
		return ubiPlace{}, false
	}
	k := joinKey(toks)
	var cands []int
	for _, i := range ix.byDist[k] {
		if regID == "" || ix.places[i].regID == regID {
			cands = append(cands, i)
		}
	}
	if len(cands) == 1 {
		return ix.places[cands[0]], true
	}
	if len(cands) > 1 {
		// Varios distritos con ese nombre: se acepta el que es capital de su propia provincia si es único.
		var caps []ubiPlace
		for _, i := range cands {
			p := ix.places[i]
			if joinKey(cleanTokens(p.distName)) == joinKey(cleanTokens(p.provName)) {
				caps = append(caps, p)
			}
		}
		if len(caps) == 1 {
			return caps[0], true
		}
		return ubiPlace{}, false
	}
	provs := map[string][]int{}
	for _, i := range ix.byProv[k] {
		if regID == "" || ix.places[i].regID == regID {
			provs[ix.places[i].provID] = append(provs[ix.places[i].provID], i)
		}
	}
	if len(provs) == 1 {
		for _, idx := range provs {
			return ix.capital(idx)
		}
	}
	return ubiPlace{}, false
}

// districtIn busca, dentro de una provincia, un distrito cuyo nombre sea exactamente las palabras dadas.
func (ix *ubiIndex) districtIn(provID string, toks []string) (ubiPlace, bool) {
	if len(toks) == 0 {
		return ubiPlace{}, false
	}
	var found []ubiPlace
	for _, i := range ix.byDist[joinKey(toks)] {
		if ix.places[i].provID == provID {
			found = append(found, ix.places[i])
		}
	}
	if len(found) == 1 {
		return found[0], true
	}
	return ubiPlace{}, false
}

// Guess cruza el nombre de una agencia con el ubigeo.
func (ix *ubiIndex) Guess(name string) AgencyGuess {
	pending := AgencyGuess{Status: database.AgencyLocPending}
	toks, addr := agencyTokens(name)
	if len(toks) == 0 {
		return pending
	}
	regID, rest := ix.stripRegion(toks)
	capStatus := func(g AgencyGuess) AgencyGuess {
		if addr && g.Status == database.AgencyLocConfirmed {
			g.Status = database.AgencyLocSuggested // una calle puede llamarse como una ciudad
		}
		return g
	}

	// 1) El nombre completo coincide (con o sin la palabra del departamento).
	for _, cand := range [][]string{rest, toks} {
		if p, ok := ix.exact(cand, regID); ok {
			return capStatus(ix.guessFromPlace(p, database.AgencyLocConfirmed))
		}
	}

	// 2) Coincide una parte: ventanas de 3 a 1 palabras, empezando por el final del nombre.
	for n := min(3, len(toks)); n >= 1; n-- {
		for i := 0; i+n <= len(toks); i++ {
			// Direcciones: la ciudad suele ir al final («AV. X CALLAO»); nombres de ciudad: al inicio.
			s := i
			if addr {
				s = len(toks) - n - i
			}
			w := toks[s : s+n]
			if n == 1 && (len(w[0]) < 3 || shortStop[w[0]]) {
				continue
			}
			p, ok := ix.exact(w, regID)
			if !ok {
				continue
			}
			// «MIRAFLORES CHICLAYO»: lo que sobra puede ser un distrito de esa provincia.
			left := append(append([]string{}, toks[:s]...), toks[s+n:]...)
			if d, ok := ix.districtIn(p.provID, left); ok {
				p = d
			}
			return ix.guessFromPlace(p, database.AgencyLocSuggested)
		}
	}

	// 2b) Nombre ambiguo, pero todos los candidatos están en el mismo departamento.
	for _, cand := range [][]string{rest, toks} {
		if g, ok := ix.sharedArea(cand, regID); ok {
			return g
		}
	}

	// 3) Solo el departamento.
	if regID != "" {
		for _, r := range ix.regs {
			if r.id == regID {
				return AgencyGuess{Department: r.name, Status: database.AgencyLocSuggested}
			}
		}
	}
	return pending
}

// palabras cortas que, solas, no identifican un lugar
var shortStop = map[string]bool{"san": true, "sur": true, "via": true, "pro": true, "nor": true, "nuevo": true}

// sharedArea cuando un nombre completo es ambiguo (hay varios distritos así), devuelve el departamento — y la
// provincia, si también coincide — que comparten todos los candidatos: sirve para filtrar aunque no se pueda fijar el distrito.
func (ix *ubiIndex) sharedArea(toks []string, regID string) (AgencyGuess, bool) {
	k := joinKey(toks)
	var cands []int
	cands = append(cands, ix.byDist[k]...)
	cands = append(cands, ix.byProv[k]...)
	var first *ubiPlace
	sameProv := true
	for _, i := range cands {
		p := ix.places[i]
		if regID != "" && p.regID != regID {
			continue
		}
		if first == nil {
			pp := p
			first = &pp
			continue
		}
		if p.regID != first.regID {
			return AgencyGuess{}, false
		}
		if p.provID != first.provID {
			sameProv = false
		}
	}
	if first == nil {
		return AgencyGuess{}, false
	}
	g := AgencyGuess{Department: first.regName, Status: database.AgencyLocSuggested}
	if sameProv {
		g.Province = first.provName
	}
	return g, true
}

package service

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"tukifac/pkg/database"
	"tukifac/pkg/secrets"
)

// SIRE (Sistema Integrado de Registros Electrónicos de SUNAT): propuesta de RVIE (ventas) y RCE
// (compras). Flujo: (1) token OAuth con credenciales SOL + client_id/secret, (2) pedir la propuesta →
// ticket, (3) consultar el ticket hasta «Terminado», (4) bajar el ZIP, (5) comparar contra lo
// registrado en Tukifac y, en ventas, (6) aceptar la propuesta.
//
// Diferencias con el sistema anterior: secretos cifrados y nunca devueltos al navegador, token en
// caché con vencimiento, TLS verificado, historial de tickets en BD, errores de SUNAT con mensaje.

const (
	SireBookSale     = "sale"
	SireBookPurchase = "purchase"

	defaultAuthURL = "https://api-seguridad.sunat.gob.pe"
	defaultAPIURL  = "https://api-sire.sunat.gob.pe"
)

// SireStatus mapa de estados de un ticket (codEstadoProceso).
var SireStatus = map[string]string{
	"00": "No solicitado", "01": "Cargado", "02": "Validando archivo", "03": "Procesado con errores",
	"04": "Procesado sin errores", "05": "En proceso", "06": "Terminado",
}

type SireClient struct {
	AuthURL string
	APIURL  string
	HTTP    *http.Client
}

func NewSireClient() *SireClient {
	return &SireClient{AuthURL: defaultAuthURL, APIURL: defaultAPIURL, HTTP: &http.Client{Timeout: 60 * time.Second}}
}

type cachedToken struct {
	token   string
	expires time.Time
}

var (
	tokenMu    sync.Mutex
	tokenCache = map[string]cachedToken{}
)

type SireCreds struct{ ClientID, ClientSecret, Username, Password string }

// creds descifra las credenciales guardadas.
func (s *Service) sireCreds() (*SireCreds, error) {
	st, err := s.SireSettings()
	if err != nil {
		return nil, err
	}
	if st.ClientID == "" || st.ClientSecretEnc == "" || st.SolUsername == "" || st.SolPasswordEnc == "" {
		return nil, errors.New("configura las credenciales SIRE (client_id, client_secret, usuario y clave SOL) en Ajustes → Contabilidad")
	}
	sec, err := secrets.Decrypt(st.ClientSecretEnc)
	if err != nil {
		return nil, fmt.Errorf("no se pudo descifrar el client_secret: %w", err)
	}
	pw, err := secrets.Decrypt(st.SolPasswordEnc)
	if err != nil {
		return nil, fmt.Errorf("no se pudo descifrar la clave SOL: %w", err)
	}
	return &SireCreds{ClientID: st.ClientID, ClientSecret: sec, Username: st.SolUsername, Password: pw}, nil
}

func (c *SireClient) token(cr *SireCreds) (string, error) {
	key := c.AuthURL + "|" + cr.ClientID + "|" + cr.Username
	tokenMu.Lock()
	defer tokenMu.Unlock()
	if t, ok := tokenCache[key]; ok && time.Now().Before(t.expires) {
		return t.token, nil
	}
	form := url.Values{
		"grant_type": {"password"}, "scope": {"https://api-sire.sunat.gob.pe"},
		"client_id": {cr.ClientID}, "client_secret": {cr.ClientSecret}, "username": {cr.Username}, "password": {cr.Password},
	}
	resp, err := c.HTTP.PostForm(fmt.Sprintf("%s/v1/clientessol/%s/oauth2/token/", c.AuthURL, url.PathEscape(cr.ClientID)), form)
	if err != nil {
		return "", fmt.Errorf("no se pudo conectar con SUNAT: %w", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return "", sunatError("autenticación SUNAT", resp.StatusCode, body)
	}
	var out struct {
		AccessToken string `json:"access_token"`
		ExpiresIn   int    `json:"expires_in"`
	}
	if err := json.Unmarshal(body, &out); err != nil || out.AccessToken == "" {
		return "", errors.New("SUNAT no devolvió un token válido")
	}
	ttl := time.Duration(out.ExpiresIn) * time.Second
	if ttl <= 0 || ttl > time.Hour {
		ttl = time.Hour
	}
	tokenCache[key] = cachedToken{token: out.AccessToken, expires: time.Now().Add(ttl - 2*time.Minute)}
	return out.AccessToken, nil
}

func sunatError(op string, status int, body []byte) error {
	var e struct {
		Msg  string `json:"msg"`
		Desc string `json:"error_description"`
		Err  string `json:"error"`
		Cod  string `json:"cod"`
		// SUNAT detalla los 422 aquí (p. ej. 1001 «El campo numRuc no enviado o es vacío»).
		Errors []struct {
			Cod string `json:"cod"`
			Msg string `json:"msg"`
		} `json:"errors"`
	}
	_ = json.Unmarshal(body, &e)
	msg := strings.TrimSpace(strings.Join([]string{e.Cod, e.Msg, e.Desc, e.Err}, " "))
	if len(e.Errors) > 0 {
		det := make([]string, 0, len(e.Errors))
		for _, x := range e.Errors {
			det = append(det, strings.TrimSpace(x.Cod+" "+x.Msg))
		}
		msg += " [" + strings.Join(det, "; ") + "]"
	}
	if msg == "" {
		msg = strings.TrimSpace(string(body))
		if len(msg) > 200 {
			msg = msg[:200]
		}
	}
	return fmt.Errorf("%s: HTTP %d %s", op, status, msg)
}

func (c *SireClient) do(cr *SireCreds, method, path string, q url.Values) ([]byte, error) {
	tk, err := c.token(cr)
	if err != nil {
		return nil, err
	}
	u := c.APIURL + path
	if len(q) > 0 {
		u += "?" + q.Encode()
	}
	req, _ := http.NewRequest(method, u, nil)
	req.Header.Set("Authorization", "Bearer "+tk)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/json") // el manual lo lista en todos los servicios
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, fmt.Errorf("no se pudo conectar con SUNAT: %w", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode == http.StatusUnauthorized {
		// Token vencido en SUNAT: se descarta la caché para que el próximo intento pida uno nuevo.
		tokenMu.Lock()
		delete(tokenCache, c.AuthURL+"|"+cr.ClientID+"|"+cr.Username)
		tokenMu.Unlock()
	}
	if resp.StatusCode/100 != 2 {
		err := sunatError("SUNAT SIRE", resp.StatusCode, body)
		if resp.StatusCode == http.StatusUnprocessableEntity && !solUserHasRUC(cr.Username) {
			err = fmt.Errorf("%w. El usuario SOL debe guardarse con el RUC por delante (RUC + usuario, p. ej. 20123456789MODDATOS); SUNAT toma el RUC de ese usuario", err)
		}
		return nil, err
	}
	return body, nil
}

// solUserHasRUC indica si el usuario SOL empieza con un RUC de 11 dígitos (formato exigido por la API SIRE).
func solUserHasRUC(u string) bool {
	if len(u) <= 11 {
		return false
	}
	for _, r := range u[:11] {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

func bookCode(book string) string {
	if book == SireBookPurchase {
		return "080000"
	}
	return "140000"
}

// SirePeriod año con sus periodos.
type SirePeriod struct {
	Year    string `json:"year"`
	Status  string `json:"status"`
	Periods []struct {
		Period string `json:"period"`
		Status string `json:"status"`
	} `json:"periods"`
}

// SirePeriods periodos disponibles del libro (ejercicios y periodos tributarios).
func (s *Service) SirePeriods(c *SireClient, book string) ([]SirePeriod, error) {
	cr, err := s.sireCreds()
	if err != nil {
		return nil, err
	}
	body, err := c.do(cr, http.MethodGet, "/v1/contribuyente/migeigv/libros/rvierce/padron/web/omisos/"+bookCode(book)+"/periodos", nil)
	if err != nil {
		return nil, err
	}
	var raw []struct {
		NumEjercicio string `json:"numEjercicio"`
		DesEstado    string `json:"desEstado"`
		LisPeriodos  []struct {
			PerTributario string `json:"perTributario"`
			DesEstado     string `json:"desEstado"`
		} `json:"lisPeriodos"`
	}
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, errors.New("respuesta inesperada de SUNAT al listar periodos")
	}
	out := make([]SirePeriod, 0, len(raw))
	for _, r := range raw {
		p := SirePeriod{Year: r.NumEjercicio, Status: r.DesEstado}
		for _, l := range r.LisPeriodos {
			p.Periods = append(p.Periods, struct {
				Period string `json:"period"`
				Status string `json:"status"`
			}{l.PerTributario, l.DesEstado})
		}
		out = append(out, p)
	}
	return out, nil
}

func validPeriod(p string) error {
	if len(p) != 6 {
		return errors.New("periodo inválido (use AAAAMM)")
	}
	if _, err := strconv.Atoi(p); err != nil {
		return errors.New("periodo inválido (use AAAAMM)")
	}
	return nil
}

// SireRequestTicket pide la propuesta del periodo y registra el ticket.
func (s *Service) SireRequestTicket(c *SireClient, book, period string, userID uint) (*database.TenantSireTicket, error) {
	if err := validPeriod(period); err != nil {
		return nil, err
	}
	cr, err := s.sireCreds()
	if err != nil {
		return nil, err
	}
	var body []byte
	if book == SireBookPurchase {
		body, err = c.do(cr, http.MethodGet, "/v1/contribuyente/migeigv/libros/rce/propuesta/web/propuesta/"+period+"/exportacioncomprobantepropuesta",
			url.Values{"codTipoArchivo": {"0"}, "codOrigenEnvio": {"2"}}) // 2 = Servicio API (manual RCE v22, §5.34)
	} else {
		body, err = c.do(cr, http.MethodGet, "/v1/contribuyente/migeigv/libros/rvie/propuesta/web/propuesta/"+period+"/exportapropuesta",
			url.Values{"codTipoArchivo": {"0"}})
	}
	if err != nil {
		return nil, err
	}
	var out struct {
		NumTicket string `json:"numTicket"`
	}
	if err := json.Unmarshal(body, &out); err != nil || out.NumTicket == "" {
		return nil, errors.New("SUNAT no devolvió un número de ticket")
	}
	t := &database.TenantSireTicket{Book: book, Period: period, Ticket: out.NumTicket, StatusCode: "01", StatusText: SireStatus["01"], UserID: userID}
	if err := s.db.Create(t).Error; err != nil {
		return nil, err
	}
	return t, nil
}

// SireTickets historial de tickets del libro.
func (s *Service) SireTickets(book string, limit int) ([]database.TenantSireTicket, error) {
	if limit <= 0 || limit > 100 {
		limit = 30
	}
	var rows []database.TenantSireTicket
	err := s.db.Where("book = ?", book).Order("id DESC").Limit(limit).Find(&rows).Error
	return rows, err
}

// SireRow fila de la comparación SUNAT ↔ Tukifac.
type SireRow struct {
	Source   string  `json:"source"` // sunat | system (la fila del sistema solo se muestra si difiere)
	Date     string  `json:"date"`
	DocType  string  `json:"doc_type"`
	Series   string  `json:"series"`
	Number   string  `json:"number"`
	Total    float64 `json:"total"`
	Local    float64 `json:"local_total"`
	Match    string  `json:"match"` // ok | diff | only_sunat | only_system
	Document string  `json:"document"`
}

type SireResult struct {
	Ticket      database.TenantSireTicket `json:"ticket"`
	Ready       bool                      `json:"ready"`
	Rows        []SireRow                 `json:"rows"`
	Summary     map[string]int            `json:"summary"`
	FileName    string                    `json:"file_name,omitempty"`
	LocalWarns  []string                  `json:"warnings,omitempty"`
	TotalSunat  float64                   `json:"total_sunat"`
	TotalSystem float64                   `json:"total_system"`
}

// SireQueryTicket consulta el estado del ticket; si está «Terminado» baja el archivo y lo compara.
func (s *Service) SireQueryTicket(c *SireClient, book, period, ticket string) (*SireResult, error) {
	if err := validPeriod(period); err != nil {
		return nil, err
	}
	cr, err := s.sireCreds()
	if err != nil {
		return nil, err
	}
	body, err := c.do(cr, http.MethodGet, "/v1/contribuyente/migeigv/libros/rvierce/gestionprocesosmasivos/web/masivo/consultaestadotickets",
		url.Values{"perIni": {period}, "perFin": {period}, "page": {"1"}, "perPage": {"20"}, "numTicket": {ticket}})
	if err != nil {
		return nil, err
	}
	var q struct {
		Registros []struct {
			NumTicket      string `json:"numTicket"`
			CodEstado      string `json:"codEstadoProceso"`
			DesEstado      string `json:"desEstadoProceso"`
			ArchivoReporte []struct {
				Nombre string `json:"nomArchivoReporte"`
				// SUNAT escribe «codTipoAchivoReporte» (sin «r») en la respuesta del ticket.
				Tipo  *string `json:"codTipoAchivoReporte"`
				Tipo2 *string `json:"codTipoArchivoReporte"`
			} `json:"archivoReporte"`
		} `json:"registros"`
	}
	if err := json.Unmarshal(body, &q); err != nil || len(q.Registros) == 0 {
		return nil, errors.New("SUNAT no encontró el ticket indicado")
	}
	r := q.Registros[0]
	var t database.TenantSireTicket
	s.db.Where("book = ? AND period = ? AND ticket = ?", book, period, ticket).First(&t)
	if t.ID == 0 {
		t = database.TenantSireTicket{Book: book, Period: period, Ticket: ticket}
	}
	t.StatusCode, t.StatusText = r.CodEstado, firstNonEmpty(SireStatus[r.CodEstado], r.DesEstado)
	if len(r.ArchivoReporte) > 0 {
		t.FileName = r.ArchivoReporte[0].Nombre
	}
	s.db.Save(&t)
	res := &SireResult{Ticket: t, Summary: map[string]int{}}
	if r.CodEstado != "06" || t.FileName == "" {
		return res, nil
	}
	tipoArchivo := "01"
	if len(r.ArchivoReporte) > 0 {
		if v := r.ArchivoReporte[0].Tipo; v != nil && *v != "" {
			tipoArchivo = *v
		} else if v := r.ArchivoReporte[0].Tipo2; v != nil && *v != "" {
			tipoArchivo = *v
		}
	}
	// Manual v22: nomArchivoReporte y codTipoArchivoReporte (los que devolvió el ticket); en ventas, además codLibro.
	q2 := url.Values{"nomArchivoReporte": {t.FileName}, "codTipoArchivoReporte": {tipoArchivo}}
	if book == SireBookSale {
		q2.Set("codLibro", "140000")
	}
	zipBytes, err := c.do(cr, http.MethodGet, "/v1/contribuyente/migeigv/libros/rvierce/gestionprocesosmasivos/web/masivo/archivoreporte", q2)
	if err != nil {
		return nil, err
	}
	lines, err := readSireZip(zipBytes)
	if err != nil {
		return nil, err
	}
	res.Ready = true
	res.FileName = t.FileName
	rows, err := s.compareSire(book, period, lines, res)
	if err != nil {
		return nil, err
	}
	res.Rows = rows
	return res, nil
}

func firstNonEmpty(a ...string) string {
	for _, v := range a {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

// SireAccept acepta la propuesta de ventas (RVIE). Irreversible en SUNAT: el llamador debe confirmarlo.
func (s *Service) SireAccept(c *SireClient, period string) error {
	if err := validPeriod(period); err != nil {
		return err
	}
	cr, err := s.sireCreds()
	if err != nil {
		return err
	}
	tk, err := c.token(cr)
	if err != nil {
		return err
	}
	req, _ := http.NewRequest(http.MethodPost, c.APIURL+"/v1/contribuyente/migeigv/libros/rvie/propuesta/web/propuesta/"+period+"/aceptapropuesta", nil)
	req.Header.Set("Authorization", "Bearer "+tk)
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return fmt.Errorf("no se pudo conectar con SUNAT: %w", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode/100 != 2 {
		return sunatError("aceptar propuesta", resp.StatusCode, body)
	}
	now := time.Now()
	s.db.Model(&database.TenantSireTicket{}).Where("book = ? AND period = ?", SireBookSale, period).Update("accepted_at", now)
	return nil
}

// ── Lectura del archivo y comparación ────────────────────────────────────────────────────────────

type sireLine struct {
	Date, DocType, Series, Number string
	Total                         float64
}

// readSireZip lee el primer .txt del ZIP (omite la cabecera) y separa por «|».
func readSireZip(b []byte) ([]string, error) {
	zr, err := zip.NewReader(bytes.NewReader(b), int64(len(b)))
	if err != nil {
		return nil, errors.New("el archivo de SUNAT no es un ZIP válido")
	}
	for _, f := range zr.File {
		if !strings.HasSuffix(strings.ToLower(f.Name), ".txt") {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			return nil, err
		}
		data, err := io.ReadAll(rc)
		rc.Close()
		if err != nil {
			return nil, err
		}
		text := strings.ReplaceAll(string(data), "\r\n", "\n")
		var lines []string
		for i, l := range strings.Split(text, "\n") {
			if i == 0 || strings.TrimSpace(l) == "" {
				continue
			}
			lines = append(lines, l)
		}
		return lines, nil
	}
	return nil, errors.New("el ZIP de SUNAT no contiene un archivo .txt")
}

func parseSireLines(book string, lines []string) []sireLine {
	numIdx, totIdx := 8, 25
	if book == SireBookPurchase {
		numIdx, totIdx = 9, 24
	}
	var out []sireLine
	for _, l := range lines {
		f := strings.Split(l, "|")
		if len(f) <= totIdx {
			continue
		}
		total, _ := strconv.ParseFloat(strings.TrimSpace(f[totIdx]), 64)
		out = append(out, sireLine{Date: strings.TrimSpace(f[4]), DocType: strings.TrimSpace(f[6]), Series: strings.TrimSpace(f[7]), Number: strings.TrimLeft(strings.TrimSpace(f[numIdx]), "0"), Total: total})
	}
	return out
}

// compareSire cruza por tipo + serie + número y compara importes (en soles).
func (s *Service) compareSire(book, period string, lines []string, res *SireResult) ([]SireRow, error) {
	from, err := time.Parse("200601", period)
	if err != nil {
		return nil, err
	}
	to := from.AddDate(0, 1, 0)
	type local struct {
		total float64
		date  time.Time
	}
	loc := map[string]local{}
	key := func(t, se, n string) string { return t + "|" + strings.ToUpper(se) + "|" + strings.TrimLeft(n, "0") }
	if book == SireBookSale {
		docs, w, err := s.SalesDocs(from, to)
		if err != nil {
			return nil, err
		}
		res.LocalWarns = w
		for _, d := range docs {
			if d.Voided {
				continue
			}
			sg := sign(d.SunatCode)
			loc[key(d.SunatCode, d.Series, d.Number)] = local{round2(sg * d.PEN.Total), d.IssueDate}
		}
	} else {
		docs, w, err := s.PurchaseDocs(from, to)
		if err != nil {
			return nil, err
		}
		res.LocalWarns = w
		for _, d := range docs {
			sg := sign(d.SunatCode)
			loc[key(d.SunatCode, d.Series, d.Number)] = local{round2(sg * d.PEN.Total), d.IssueDate}
		}
	}
	var rows []SireRow
	seen := map[string]bool{}
	for _, l := range parseSireLines(book, lines) {
		k := key(l.DocType, l.Series, l.Number)
		seen[k] = true
		row := SireRow{Source: "sunat", Date: l.Date, DocType: l.DocType, Series: l.Series, Number: l.Number, Total: round2(l.Total), Document: l.Series + "-" + l.Number}
		res.TotalSunat += row.Total
		if lc, ok := loc[k]; ok {
			row.Local = lc.total
			if absDiff(lc.total, row.Total) <= 0.02 {
				row.Match = "ok"
			} else {
				row.Match = "diff"
			}
		} else {
			row.Match = "only_sunat"
		}
		res.Summary[row.Match]++
		rows = append(rows, row)
	}
	var missing []string
	for k := range loc {
		if !seen[k] {
			missing = append(missing, k)
		}
	}
	sort.Strings(missing)
	for _, k := range missing {
		p := strings.Split(k, "|")
		lc := loc[k]
		rows = append(rows, SireRow{Source: "system", Date: lc.date.Format("02/01/2006"), DocType: p[0], Series: p[1], Number: p[2], Local: lc.total, Match: "only_system", Document: p[1] + "-" + p[2]})
		res.Summary["only_system"]++
	}
	for _, l := range loc {
		res.TotalSystem += l.total
	}
	res.TotalSunat, res.TotalSystem = round2(res.TotalSunat), round2(res.TotalSystem)
	return rows, nil
}

func absDiff(a, b float64) float64 {
	if a > b {
		return a - b
	}
	return b - a
}

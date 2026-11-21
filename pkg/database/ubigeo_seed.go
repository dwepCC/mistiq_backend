package database

import (
	"bufio"
	_ "embed"
	"io"
	"os"
	"strings"

	"gorm.io/gorm"
)

//go:embed data_ubi.txt
var ubigeoDistritosEmbedded string

// UbigeoDistritosCSVPath devuelve la ruta del CSV de distritos (id|nombre|info_busqueda|provincia_id|region_id).
// Por defecto usa el archivo embebido en el binario. Se puede sobreescribir con UBIGEO_DISTRITOS_CSV.
func UbigeoDistritosCSVPath() string {
	if p := os.Getenv("UBIGEO_DISTRITOS_CSV"); p != "" {
		return p
	}
	return ""
}

// ubiRegionesPeru son los 25 departamentos del Perú (código INEI 6 dígitos).
var ubiRegionesPeru = []UbiRegion{
	{ID: "010000", Nombre: "Amazonas"},
	{ID: "020000", Nombre: "Áncash"},
	{ID: "030000", Nombre: "Apurímac"},
	{ID: "040000", Nombre: "Arequipa"},
	{ID: "050000", Nombre: "Ayacucho"},
	{ID: "060000", Nombre: "Cajamarca"},
	{ID: "070000", Nombre: "Callao"},
	{ID: "080000", Nombre: "Cusco"},
	{ID: "090000", Nombre: "Huancavelica"},
	{ID: "100000", Nombre: "Huánuco"},
	{ID: "110000", Nombre: "Ica"},
	{ID: "120000", Nombre: "Junín"},
	{ID: "130000", Nombre: "La Libertad"},
	{ID: "140000", Nombre: "Lambayeque"},
	{ID: "150000", Nombre: "Lima"},
	{ID: "160000", Nombre: "Loreto"},
	{ID: "170000", Nombre: "Madre de Dios"},
	{ID: "180000", Nombre: "Moquegua"},
	{ID: "190000", Nombre: "Pasco"},
	{ID: "200000", Nombre: "Piura"},
	{ID: "210000", Nombre: "Puno"},
	{ID: "220000", Nombre: "San Martín"},
	{ID: "230000", Nombre: "Tacna"},
	{ID: "240000", Nombre: "Tumbes"},
	{ID: "250000", Nombre: "Ucayali"},
}

// ubiProvinciasPeru son las provincias del Perú (196), con region_id referenciando ubi_regiones.
var ubiProvinciasPeru = []UbiProvincia{
	{ID: "010100", Nombre: "Chachapoyas", RegionID: "010000"},
	{ID: "010200", Nombre: "Bagua", RegionID: "010000"},
	{ID: "010300", Nombre: "Bongará", RegionID: "010000"},
	{ID: "010400", Nombre: "Condorcanqui", RegionID: "010000"},
	{ID: "010500", Nombre: "Luya", RegionID: "010000"},
	{ID: "010600", Nombre: "Rodríguez de Mendoza", RegionID: "010000"},
	{ID: "010700", Nombre: "Utcubamba", RegionID: "010000"},
	{ID: "020100", Nombre: "Huaraz", RegionID: "020000"},
	{ID: "020200", Nombre: "Aija", RegionID: "020000"},
	{ID: "020300", Nombre: "Antonio Raymondi", RegionID: "020000"},
	{ID: "020400", Nombre: "Asunción", RegionID: "020000"},
	{ID: "020500", Nombre: "Bolognesi", RegionID: "020000"},
	{ID: "020600", Nombre: "Carhuaz", RegionID: "020000"},
	{ID: "020700", Nombre: "Carlos Fermín Fitzcarrald", RegionID: "020000"},
	{ID: "020800", Nombre: "Casma", RegionID: "020000"},
	{ID: "020900", Nombre: "Corongo", RegionID: "020000"},
	{ID: "021000", Nombre: "Huari", RegionID: "020000"},
	{ID: "021100", Nombre: "Huarmey", RegionID: "020000"},
	{ID: "021200", Nombre: "Huaylas", RegionID: "020000"},
	{ID: "021300", Nombre: "Mariscal Luzuriaga", RegionID: "020000"},
	{ID: "021400", Nombre: "Ocros", RegionID: "020000"},
	{ID: "021500", Nombre: "Pallasca", RegionID: "020000"},
	{ID: "021600", Nombre: "Pomabamba", RegionID: "020000"},
	{ID: "021700", Nombre: "Recuay", RegionID: "020000"},
	{ID: "021800", Nombre: "Santa", RegionID: "020000"},
	{ID: "021900", Nombre: "Sihuas", RegionID: "020000"},
	{ID: "022000", Nombre: "Yungay", RegionID: "020000"},
	{ID: "030100", Nombre: "Abancay", RegionID: "030000"},
	{ID: "030200", Nombre: "Andahuaylas", RegionID: "030000"},
	{ID: "030300", Nombre: "Antabamba", RegionID: "030000"},
	{ID: "030400", Nombre: "Aymaraes", RegionID: "030000"},
	{ID: "030500", Nombre: "Cotabambas", RegionID: "030000"},
	{ID: "030600", Nombre: "Chincheros", RegionID: "030000"},
	{ID: "030700", Nombre: "Grau", RegionID: "030000"},
	{ID: "040100", Nombre: "Arequipa", RegionID: "040000"},
	{ID: "040200", Nombre: "Camaná", RegionID: "040000"},
	{ID: "040300", Nombre: "Caravelí", RegionID: "040000"},
	{ID: "040400", Nombre: "Castilla", RegionID: "040000"},
	{ID: "040500", Nombre: "Caylloma", RegionID: "040000"},
	{ID: "040600", Nombre: "Condesuyos", RegionID: "040000"},
	{ID: "040700", Nombre: "Islay", RegionID: "040000"},
	{ID: "040800", Nombre: "La Uniòn", RegionID: "040000"},
	{ID: "050100", Nombre: "Huamanga", RegionID: "050000"},
	{ID: "050200", Nombre: "Cangallo", RegionID: "050000"},
	{ID: "050300", Nombre: "Huanca Sancos", RegionID: "050000"},
	{ID: "050400", Nombre: "Huanta", RegionID: "050000"},
	{ID: "050500", Nombre: "La Mar", RegionID: "050000"},
	{ID: "050600", Nombre: "Lucanas", RegionID: "050000"},
	{ID: "050700", Nombre: "Parinacochas", RegionID: "050000"},
	{ID: "050800", Nombre: "Pàucar del Sara Sara", RegionID: "050000"},
	{ID: "050900", Nombre: "Sucre", RegionID: "050000"},
	{ID: "051000", Nombre: "Víctor Fajardo", RegionID: "050000"},
	{ID: "051100", Nombre: "Vilcas Huamán", RegionID: "050000"},
	{ID: "060100", Nombre: "Cajamarca", RegionID: "060000"},
	{ID: "060200", Nombre: "Cajabamba", RegionID: "060000"},
	{ID: "060300", Nombre: "Celendín", RegionID: "060000"},
	{ID: "060400", Nombre: "Chota", RegionID: "060000"},
	{ID: "060500", Nombre: "Contumazá", RegionID: "060000"},
	{ID: "060600", Nombre: "Cutervo", RegionID: "060000"},
	{ID: "060700", Nombre: "Hualgayoc", RegionID: "060000"},
	{ID: "060800", Nombre: "Jaén", RegionID: "060000"},
	{ID: "060900", Nombre: "San Ignacio", RegionID: "060000"},
	{ID: "061000", Nombre: "San Marcos", RegionID: "060000"},
	{ID: "061100", Nombre: "San Miguel", RegionID: "060000"},
	{ID: "061200", Nombre: "San Pablo", RegionID: "060000"},
	{ID: "061300", Nombre: "Santa Cruz", RegionID: "060000"},
	{ID: "070100", Nombre: "Prov. Const. del Callao", RegionID: "070000"},
	{ID: "080100", Nombre: "Cusco", RegionID: "080000"},
	{ID: "080200", Nombre: "Acomayo", RegionID: "080000"},
	{ID: "080300", Nombre: "Anta", RegionID: "080000"},
	{ID: "080400", Nombre: "Calca", RegionID: "080000"},
	{ID: "080500", Nombre: "Canas", RegionID: "080000"},
	{ID: "080600", Nombre: "Canchis", RegionID: "080000"},
	{ID: "080700", Nombre: "Chumbivilcas", RegionID: "080000"},
	{ID: "080800", Nombre: "Espinar", RegionID: "080000"},
	{ID: "080900", Nombre: "La Convención", RegionID: "080000"},
	{ID: "081000", Nombre: "Paruro", RegionID: "080000"},
	{ID: "081100", Nombre: "Paucartambo", RegionID: "080000"},
	{ID: "081200", Nombre: "Quispicanchi", RegionID: "080000"},
	{ID: "081300", Nombre: "Urubamba", RegionID: "080000"},
	{ID: "090100", Nombre: "Huancavelica", RegionID: "090000"},
	{ID: "090200", Nombre: "Acobamba", RegionID: "090000"},
	{ID: "090300", Nombre: "Angaraes", RegionID: "090000"},
	{ID: "090400", Nombre: "Castrovirreyna", RegionID: "090000"},
	{ID: "090500", Nombre: "Churcampa", RegionID: "090000"},
	{ID: "090600", Nombre: "Huaytará", RegionID: "090000"},
	{ID: "090700", Nombre: "Tayacaja", RegionID: "090000"},
	{ID: "100100", Nombre: "Huánuco", RegionID: "100000"},
	{ID: "100200", Nombre: "Ambo", RegionID: "100000"},
	{ID: "100300", Nombre: "Dos de Mayo", RegionID: "100000"},
	{ID: "100400", Nombre: "Huacaybamba", RegionID: "100000"},
	{ID: "100500", Nombre: "Huamalíes", RegionID: "100000"},
	{ID: "100600", Nombre: "Leoncio Prado", RegionID: "100000"},
	{ID: "100700", Nombre: "Marañón", RegionID: "100000"},
	{ID: "100800", Nombre: "Pachitea", RegionID: "100000"},
	{ID: "100900", Nombre: "Puerto Inca", RegionID: "100000"},
	{ID: "101000", Nombre: "Lauricocha", RegionID: "100000"},
	{ID: "101100", Nombre: "Yarowilca", RegionID: "100000"},
	{ID: "110100", Nombre: "Ica", RegionID: "110000"},
	{ID: "110200", Nombre: "Chincha", RegionID: "110000"},
	{ID: "110300", Nombre: "Nasca", RegionID: "110000"},
	{ID: "110400", Nombre: "Palpa", RegionID: "110000"},
	{ID: "110500", Nombre: "Pisco", RegionID: "110000"},
	{ID: "120100", Nombre: "Huancayo", RegionID: "120000"},
	{ID: "120200", Nombre: "Concepción", RegionID: "120000"},
	{ID: "120300", Nombre: "Chanchamayo", RegionID: "120000"},
	{ID: "120400", Nombre: "Jauja", RegionID: "120000"},
	{ID: "120500", Nombre: "Junín", RegionID: "120000"},
	{ID: "120600", Nombre: "Satipo", RegionID: "120000"},
	{ID: "120700", Nombre: "Tarma", RegionID: "120000"},
	{ID: "120800", Nombre: "Yauli", RegionID: "120000"},
	{ID: "120900", Nombre: "Chupaca", RegionID: "120000"},
	{ID: "130100", Nombre: "Trujillo", RegionID: "130000"},
	{ID: "130200", Nombre: "Ascope", RegionID: "130000"},
	{ID: "130300", Nombre: "Bolívar", RegionID: "130000"},
	{ID: "130400", Nombre: "Chepén", RegionID: "130000"},
	{ID: "130500", Nombre: "Julcán", RegionID: "130000"},
	{ID: "130600", Nombre: "Otuzco", RegionID: "130000"},
	{ID: "130700", Nombre: "Pacasmayo", RegionID: "130000"},
	{ID: "130800", Nombre: "Pataz", RegionID: "130000"},
	{ID: "130900", Nombre: "Sánchez Carrión", RegionID: "130000"},
	{ID: "131000", Nombre: "Santiago de Chuco", RegionID: "130000"},
	{ID: "131100", Nombre: "Gran Chimú", RegionID: "130000"},
	{ID: "131200", Nombre: "Virú", RegionID: "130000"},
	{ID: "140100", Nombre: "Chiclayo", RegionID: "140000"},
	{ID: "140200", Nombre: "Ferreñafe", RegionID: "140000"},
	{ID: "140300", Nombre: "Lambayeque", RegionID: "140000"},
	{ID: "150100", Nombre: "Lima", RegionID: "150000"},
	{ID: "150200", Nombre: "Barranca", RegionID: "150000"},
	{ID: "150300", Nombre: "Cajatambo", RegionID: "150000"},
	{ID: "150400", Nombre: "Canta", RegionID: "150000"},
	{ID: "150500", Nombre: "Cañete", RegionID: "150000"},
	{ID: "150600", Nombre: "Huaral", RegionID: "150000"},
	{ID: "150700", Nombre: "Huarochirí", RegionID: "150000"},
	{ID: "150800", Nombre: "Huaura", RegionID: "150000"},
	{ID: "150900", Nombre: "Oyón", RegionID: "150000"},
	{ID: "151000", Nombre: "Yauyos", RegionID: "150000"},
	{ID: "160100", Nombre: "Maynas", RegionID: "160000"},
	{ID: "160200", Nombre: "Alto Amazonas", RegionID: "160000"},
	{ID: "160300", Nombre: "Loreto", RegionID: "160000"},
	{ID: "160400", Nombre: "Mariscal Ramón Castilla", RegionID: "160000"},
	{ID: "160500", Nombre: "Requena", RegionID: "160000"},
	{ID: "160600", Nombre: "Ucayali", RegionID: "160000"},
	{ID: "160700", Nombre: "Datem del Marañón", RegionID: "160000"},
	{ID: "160800", Nombre: "Putumayo", RegionID: "160000"},
	{ID: "170100", Nombre: "Tambopata", RegionID: "170000"},
	{ID: "170200", Nombre: "Manu", RegionID: "170000"},
	{ID: "170300", Nombre: "Tahuamanu", RegionID: "170000"},
	{ID: "180100", Nombre: "Mariscal Nieto", RegionID: "180000"},
	{ID: "180200", Nombre: "General Sánchez Cerro", RegionID: "180000"},
	{ID: "180300", Nombre: "Ilo", RegionID: "180000"},
	{ID: "190100", Nombre: "Pasco", RegionID: "190000"},
	{ID: "190200", Nombre: "Daniel Alcides Carrión", RegionID: "190000"},
	{ID: "190300", Nombre: "Oxapampa", RegionID: "190000"},
	{ID: "200100", Nombre: "Piura", RegionID: "200000"},
	{ID: "200200", Nombre: "Ayabaca", RegionID: "200000"},
	{ID: "200300", Nombre: "Huancabamba", RegionID: "200000"},
	{ID: "200400", Nombre: "Morropón", RegionID: "200000"},
	{ID: "200500", Nombre: "Paita", RegionID: "200000"},
	{ID: "200600", Nombre: "Sullana", RegionID: "200000"},
	{ID: "200700", Nombre: "Talara", RegionID: "200000"},
	{ID: "200800", Nombre: "Sechura", RegionID: "200000"},
	{ID: "210100", Nombre: "Puno", RegionID: "210000"},
	{ID: "210200", Nombre: "Azángaro", RegionID: "210000"},
	{ID: "210300", Nombre: "Carabaya", RegionID: "210000"},
	{ID: "210400", Nombre: "Chucuito", RegionID: "210000"},
	{ID: "210500", Nombre: "El Collao", RegionID: "210000"},
	{ID: "210600", Nombre: "Huancané", RegionID: "210000"},
	{ID: "210700", Nombre: "Lampa", RegionID: "210000"},
	{ID: "210800", Nombre: "Melgar", RegionID: "210000"},
	{ID: "210900", Nombre: "Moho", RegionID: "210000"},
	{ID: "211000", Nombre: "San Antonio de Putina", RegionID: "210000"},
	{ID: "211100", Nombre: "San Román", RegionID: "210000"},
	{ID: "211200", Nombre: "Sandia", RegionID: "210000"},
	{ID: "211300", Nombre: "Yunguyo", RegionID: "210000"},
	{ID: "220100", Nombre: "Moyobamba", RegionID: "220000"},
	{ID: "220200", Nombre: "Bellavista", RegionID: "220000"},
	{ID: "220300", Nombre: "El Dorado", RegionID: "220000"},
	{ID: "220400", Nombre: "Huallaga", RegionID: "220000"},
	{ID: "220500", Nombre: "Lamas", RegionID: "220000"},
	{ID: "220600", Nombre: "Mariscal Cáceres", RegionID: "220000"},
	{ID: "220700", Nombre: "Picota", RegionID: "220000"},
	{ID: "220800", Nombre: "Rioja", RegionID: "220000"},
	{ID: "220900", Nombre: "San Martín", RegionID: "220000"},
	{ID: "221000", Nombre: "Tocache", RegionID: "220000"},
	{ID: "230100", Nombre: "Tacna", RegionID: "230000"},
	{ID: "230200", Nombre: "Candarave", RegionID: "230000"},
	{ID: "230300", Nombre: "Jorge Basadre", RegionID: "230000"},
	{ID: "230400", Nombre: "Tarata", RegionID: "230000"},
	{ID: "240100", Nombre: "Tumbes", RegionID: "240000"},
	{ID: "240200", Nombre: "Contralmirante Villar", RegionID: "240000"},
	{ID: "240300", Nombre: "Zarumilla", RegionID: "240000"},
	{ID: "250100", Nombre: "Coronel Portillo", RegionID: "250000"},
	{ID: "250200", Nombre: "Atalaya", RegionID: "250000"},
	{ID: "250300", Nombre: "Padre Abad", RegionID: "250000"},
	{ID: "250400", Nombre: "Purús", RegionID: "250000"},
}

// SeedUbigeoRegionesProvincias inserta departamentos y provincias del Perú si las tablas están vacías.
// Se usa en BD central y en cada BD tenant. No es gestionable desde la UI.
func SeedUbigeoRegionesProvincias(db *gorm.DB) error {
	var count int64
	if err := db.Model(&UbiRegion{}).Count(&count).Error; err != nil {
		return err
	}
	if count == 0 {
		if err := db.CreateInBatches(ubiRegionesPeru, 50).Error; err != nil {
			return err
		}
	}
	if err := db.Model(&UbiProvincia{}).Count(&count).Error; err != nil {
		return err
	}
	if count == 0 {
		if err := db.CreateInBatches(ubiProvinciasPeru, 50).Error; err != nil {
			return err
		}
	}
	return nil
}

// SeedUbigeoDistritos carga distritos desde un archivo CSV con formato: id|nombre|info_busqueda|provincia_id|region_id
// (mismo formato que data_ubi.txt del seeder PHP).
// Si csvPath está vacío, usa el dataset embebido.
// Si csvPath no está vacío y el archivo no existe, intenta el dataset embebido.
// Si la tabla ubi_distritos ya tiene datos, no sobrescribe.
func SeedUbigeoDistritos(db *gorm.DB, csvPath string) error {
	var r io.Reader
	if strings.TrimSpace(csvPath) != "" {
		f, err := os.Open(csvPath)
		if err == nil {
			defer f.Close()
			r = f
		} else if strings.TrimSpace(ubigeoDistritosEmbedded) != "" {
			r = strings.NewReader(ubigeoDistritosEmbedded)
		} else {
			return nil
		}
	} else if strings.TrimSpace(ubigeoDistritosEmbedded) != "" {
		r = strings.NewReader(ubigeoDistritosEmbedded)
	} else {
		return nil
	}

	var count int64
	if err := db.Model(&UbiDistrito{}).Count(&count).Error; err != nil {
		return err
	}
	if count > 0 {
		return nil // ya sembrados
	}

	scanner := bufio.NewScanner(r)
	lineNum := 0
	var batch []UbiDistrito
	const batchSize = 500
	for scanner.Scan() {
		lineNum++
		line := strings.TrimSpace(scanner.Text())
		if line == "" || lineNum == 1 && strings.HasPrefix(line, "id") {
			continue // header o línea vacía
		}
		parts := strings.Split(line, "|")
		if len(parts) < 5 {
			continue
		}
		id := strings.TrimSpace(parts[0])
		nombre := strings.TrimSpace(parts[1])
		infoBusqueda := strings.TrimSpace(parts[2])
		provinciaID := strings.TrimSpace(parts[3])
		regionID := strings.TrimSpace(parts[4])
		if id == "" || nombre == "" {
			continue
		}
		batch = append(batch, UbiDistrito{
			ID:           id,
			Nombre:       nombre,
			ProvinciaID:  provinciaID,
			RegionID:     regionID,
			InfoBusqueda: infoBusqueda,
		})
		if len(batch) >= batchSize {
			if err := db.CreateInBatches(batch, batchSize).Error; err != nil {
				return err
			}
			batch = batch[:0]
		}
	}
	if len(batch) > 0 {
		if err := db.CreateInBatches(batch, batchSize).Error; err != nil {
			return err
		}
	}
	return scanner.Err()
}

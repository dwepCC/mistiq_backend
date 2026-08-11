package tenantmigrations

import (
	"regexp"
	"strconv"
	"strings"

	"gorm.io/gorm"
)

// dbEngine identifica el motor SQL detectado en el tenant.
type dbEngine string

const (
	engineMySQL   dbEngine = "mysql"
	engineMariaDB dbEngine = "mariadb"
	engineSQLite  dbEngine = "sqlite"
	engineUnknown dbEngine = "unknown"
)

// dbCapabilities resume motor, versión y estrategias de índice soportadas.
type dbCapabilities struct {
	Engine     dbEngine
	RawVersion string
	Major      int
	Minor      int
	Patch      int
}

func detectDBCapabilities(db *gorm.DB) dbCapabilities {
	cap := dbCapabilities{Engine: engineUnknown}
	if db == nil {
		return cap
	}
	if db.Dialector != nil {
		name := strings.ToLower(db.Dialector.Name())
		if strings.Contains(name, "sqlite") {
			cap.Engine = engineSQLite
			cap.RawVersion = "sqlite"
			return cap
		}
	}
	if err := db.Raw("SELECT VERSION()").Scan(&cap.RawVersion).Error; err != nil {
		return cap
	}
	lower := strings.ToLower(strings.TrimSpace(cap.RawVersion))
	if strings.Contains(lower, "mariadb") {
		cap.Engine = engineMariaDB
		cap.Major, cap.Minor, cap.Patch = parseMariaDBVersion(lower)
		return cap
	}
	if strings.Contains(lower, "sqlite") {
		cap.Engine = engineSQLite
		return cap
	}
	cap.Engine = engineMySQL
	cap.Major, cap.Minor, cap.Patch = parseNumericVersion(lower)
	return cap
}

var versionTripleRE = regexp.MustCompile(`(\d+)\.(\d+)\.(\d+)`)

func parseNumericVersion(s string) (major, minor, patch int) {
	m := versionTripleRE.FindStringSubmatch(s)
	if len(m) < 4 {
		return 0, 0, 0
	}
	major, _ = strconv.Atoi(m[1])
	minor, _ = strconv.Atoi(m[2])
	patch, _ = strconv.Atoi(m[3])
	return major, minor, patch
}

func parseMariaDBVersion(s string) (major, minor, patch int) {
	s = strings.ToLower(strings.TrimSpace(s))
	if idx := strings.Index(s, "mariadb"); idx > 0 {
		prefix := strings.Trim(strings.TrimSpace(s[:idx]), "-")
		if strings.Contains(prefix, "-") {
			parts := strings.Split(prefix, "-")
			prefix = parts[len(parts)-1]
		}
		return parseNumericVersion(prefix)
	}
	return parseNumericVersion(s)
}

// SupportsFunctionalPartialUnique índice único sobre expresión (MySQL 8.0.13+, MariaDB 10.6+).
func (c dbCapabilities) SupportsFunctionalPartialUnique() bool {
	switch c.Engine {
	case engineMySQL:
		if c.Major < 8 {
			return false
		}
		if c.Major > 8 {
			return true
		}
		if c.Minor > 0 {
			return true
		}
		return c.Patch >= 13
	case engineMariaDB:
		return c.Major > 10 || (c.Major == 10 && c.Minor >= 6)
	default:
		return false
	}
}

// SupportsGeneratedColumnUnique columna VIRTUAL + UNIQUE (MySQL 5.7.6+, MariaDB 10.2+).
func (c dbCapabilities) SupportsGeneratedColumnUnique() bool {
	switch c.Engine {
	case engineMySQL:
		if c.Major > 5 {
			return true
		}
		if c.Major == 5 && c.Minor > 7 {
			return true
		}
		return c.Major == 5 && c.Minor == 7 && c.Patch >= 6
	case engineMariaDB:
		return c.Major > 10 || (c.Major == 10 && c.Minor >= 2)
	default:
		return false
	}
}

func (c dbCapabilities) String() string {
	return string(c.Engine) + " " + c.RawVersion
}

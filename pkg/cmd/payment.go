package cmd

import (
	"fmt"
	"os"
	"strings"

	"tukifac/pkg/database"

	"gorm.io/gorm"
)

func RunPayment(args []string) int {
	if len(args) == 0 {
		printPaymentUsage()
		return 1
	}
	switch args[0] {
	case "audit":
		return runPaymentAudit(args[1:])
	case "repair":
		return runPaymentRepair(args[1:])
	case "verify":
		return runPaymentVerify(args[1:])
	default:
		fmt.Fprintf(os.Stderr, "unknown payment subcommand: %s\n\n", args[0])
		printPaymentUsage()
		return 1
	}
}

func printPaymentUsage() {
	fmt.Println(`payment — dominio financiero tenant

  payment audit [--slug=tenant]     Reporta faltantes y filas huérfanas
  payment repair [--slug=tenant]    Repara catálogo (split + seed idempotente)
  payment verify [--slug=tenant]    Sale 0 si audit OK, 1 si hay problemas`)
}

func runPaymentAudit(args []string) int {
	slug := paymentSlugFlag(args)
	return forEachTenant(slug, func(slug string, db *gorm.DB) error {
		audit, err := database.AuditFinancialCatalog(db)
		if err != nil {
			return err
		}
		fmt.Printf("[%s] ok=%v missing_methods=%v missing_conditions=%v missing_tax=%v orphans=%v unlinked=%v\n",
			slug, audit.OK, audit.MissingMethods, audit.MissingConditions, audit.MissingTaxTypes,
			audit.OrphanMethodRows, audit.UnlinkedBankMethods)
		if !audit.OK {
			return fmt.Errorf("audit failed")
		}
		return nil
	})
}

func runPaymentRepair(args []string) int {
	slug := paymentSlugFlag(args)
	return forEachTenant(slug, func(slug string, db *gorm.DB) error {
		if err := database.RepairFinancialCatalog(db); err != nil {
			return err
		}
		fmt.Printf("[%s] repaired\n", slug)
		return nil
	})
}

func runPaymentVerify(args []string) int {
	code := runPaymentAudit(args)
	return code
}

func paymentSlugFlag(args []string) string {
	for _, a := range args {
		if strings.HasPrefix(a, "--slug=") {
			return strings.TrimPrefix(a, "--slug=")
		}
	}
	return ""
}

func forEachTenant(slug string, fn func(slug string, db *gorm.DB) error) int {
	if database.CentralDB == nil {
		fmt.Fprintln(os.Stderr, "central DB not connected")
		return 1
	}
	tenants, err := database.ListTenantsForMigration(false)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	fail := 0
	for _, t := range tenants {
		if slug != "" && t.Slug != slug {
			continue
		}
		db, err := database.OpenTenantDBForMigration(t.DBName)
		if err != nil {
			fmt.Fprintf(os.Stderr, "[%s] connect: %v\n", t.Slug, err)
			fail++
			continue
		}
		if err := fn(t.Slug, db); err != nil {
			fmt.Fprintf(os.Stderr, "[%s] %v\n", t.Slug, err)
			fail++
		}
		database.CloseTenantDB(db)
	}
	if fail > 0 {
		return 1
	}
	return 0
}

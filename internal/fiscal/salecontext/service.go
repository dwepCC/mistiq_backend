package salecontext

import (
	"strings"
	"time"

	"tukifac/pkg/database"

	"gorm.io/gorm"
)

// Service orquesta persistencia y evaluación fiscal de ventas.
type Service struct {
	repo *Repository
}

func NewService(db *gorm.DB) *Service {
	return &Service{repo: NewRepository(db)}
}

// Persist guarda el contexto fiscal de una venta recién creada.
func (s *Service) Persist(in PersistInput) (*FiscalContextOutput, error) {
	if in.FiscalContext == nil || IsEmptyFiscalInput(in.FiscalContext) {
		return nil, nil
	}
	contact := in.Contact
	requested, manualOverride := ResolveRequestedRetention(in.FiscalContext, in.SunatDocCode, contact, in.SaleTotal, in.Currency, in.ExchangeRate)
	eval := EvaluateIGVRetention(RetentionEvalInput{
		RequestedRetention: requested,
		ManualOverride:     manualOverride,
		SunatDocCode:       in.SunatDocCode,
		Contact:            contact,
		SaleTotal:          in.SaleTotal,
		Currency:           in.Currency,
		ExchangeRate:       in.ExchangeRate,
	})

	profile := database.TenantSaleFiscalProfile{
		SaleID:                     in.SaleID,
		SchemaVersion:              SchemaVersion,
		OperationTypeCode:          DefaultOperationType,
		HasIgvRetention:            eval.HasIgvRetention,
		IgvRetentionManualOverride: eval.IgvRetentionManualOverride,
		ShowTermsConditions:        in.FiscalContext.ShowTermsConditions,
		FiscalObservations:         strings.TrimSpace(in.FiscalContext.FiscalObservations),
		PurchaseOrderNumber:        strings.TrimSpace(in.FiscalContext.PurchaseOrderNumber),
		SellerUserID:               in.FiscalContext.SellerUserID,
		CreatedByUserID:            in.UserID,
		CreatedAt:                  time.Now(),
		UpdatedAt:                  time.Now(),
	}

	var refs []database.TenantSaleFiscalReference
	for i, refIn := range in.FiscalContext.References {
		refIn.SortOrder = i
		if row, ok := normalizeReferenceInput(refIn); ok {
			row.SaleID = in.SaleID
			refs = append(refs, row)
		}
	}

	var obligations []database.TenantSaleFiscalObligation
	if eval.HasIgvRetention || eval.ObligationAmount > 0 {
		status := StatusNotApplicable
		if eval.Applicable {
			status = StatusApplicable
		}
		obligations = append(obligations, database.TenantSaleFiscalObligation{
			SaleID:              in.SaleID,
			ObligationKind:      ObligationIGVRetention,
			Direction:           DirectionWithheldFromUs,
			RatePercent:         eval.RatePercent,
			BaseAmount:          eval.BaseAmount,
			ObligationAmount:    eval.ObligationAmount,
			Currency:            in.Currency,
			ApplicabilityStatus: status,
			ApplicabilityReason: eval.Reason,
			Source:              eval.Source,
			Status:              StatusPending,
			CreatedAt:           time.Now(),
			UpdatedAt:           time.Now(),
		})
	}

	if err := s.repo.Save(profile, refs, obligations); err != nil {
		return nil, err
	}
	return s.buildOutput(&profile, refs, obligations, in.SaleTotal, eval), nil
}

// Load devuelve contexto fiscal de una venta.
func (s *Service) Load(saleID uint, saleTotal float64) (*FiscalContextOutput, error) {
	profile, refs, obligations, err := s.repo.LoadBySaleID(saleID)
	if err != nil {
		return nil, err
	}
	eval := retentionFromStored(profile, obligations, saleTotal)
	return s.buildOutput(profile, refs, obligations, saleTotal, eval), nil
}

func (s *Service) buildOutput(
	profile *database.TenantSaleFiscalProfile,
	refs []database.TenantSaleFiscalReference,
	obligations []database.TenantSaleFiscalObligation,
	saleTotal float64,
	eval RetentionEvalResult,
) *FiscalContextOutput {
	out := &FiscalContextOutput{
		Profile: FiscalProfileOutput{
			SaleID:                     profile.SaleID,
			SchemaVersion:              profile.SchemaVersion,
			OperationTypeCode:          profile.OperationTypeCode,
			HasIgvRetention:            profile.HasIgvRetention,
			IgvRetentionManualOverride: profile.IgvRetentionManualOverride,
			ShowTermsConditions:        profile.ShowTermsConditions,
			FiscalObservations:         profile.FiscalObservations,
			PurchaseOrderNumber:        profile.PurchaseOrderNumber,
			SellerUserID:               profile.SellerUserID,
		},
		Summary: FiscalSummaryOutput{
			SaleTotal:        saleTotal,
			RetentionAmount:  eval.ObligationAmount,
			NetCollectible:   eval.NetCollectible,
			RetentionApplied: eval.Applicable && eval.HasIgvRetention,
		},
	}
	for _, r := range refs {
		out.References = append(out.References, FiscalReferenceOutput{
			ID:                   r.ID,
			ReferenceKind:        r.ReferenceKind,
			ReferencedSunatType:  r.ReferencedSunatType,
			ReferencedSeries:     r.ReferencedSeries,
			ReferencedNumber:     r.ReferencedNumber,
			ReferencedFullNumber: r.ReferencedFullNumber,
			SortOrder:            r.SortOrder,
		})
	}
	for _, o := range obligations {
		out.Obligations = append(out.Obligations, FiscalObligationOutput{
			ID:                  o.ID,
			ObligationKind:      o.ObligationKind,
			Direction:           o.Direction,
			RatePercent:         o.RatePercent,
			BaseAmount:          o.BaseAmount,
			ObligationAmount:    o.ObligationAmount,
			Currency:            o.Currency,
			ApplicabilityStatus: o.ApplicabilityStatus,
			ApplicabilityReason: o.ApplicabilityReason,
			Source:              o.Source,
			Status:              o.Status,
		})
	}
	return out
}

func retentionFromStored(profile *database.TenantSaleFiscalProfile, obligations []database.TenantSaleFiscalObligation, saleTotal float64) RetentionEvalResult {
	eval := RetentionEvalResult{
		HasIgvRetention:            profile.HasIgvRetention,
		IgvRetentionManualOverride: profile.IgvRetentionManualOverride,
		BaseAmount:                 saleTotal,
		NetCollectible:             saleTotal,
		RatePercent:                IGVRetentionRate * 100,
	}
	for _, o := range obligations {
		if o.ObligationKind != ObligationIGVRetention {
			continue
		}
		eval.ObligationAmount = o.ObligationAmount
		eval.Applicable = o.ApplicabilityStatus == StatusApplicable
		eval.Reason = o.ApplicabilityReason
		eval.Source = o.Source
		if eval.Applicable && profile.HasIgvRetention {
			eval.NetCollectible = saleTotal - o.ObligationAmount
		}
		break
	}
	return eval
}

// SunatCodeFromSeries resuelve código SUNAT desde serie de documento.
func SunatCodeFromSeries(series *database.TenantDocumentSeries, docType string) string {
	if series != nil {
		if code := strings.TrimSpace(series.SunatCode); code != "" {
			return code
		}
	}
	dt := strings.ToUpper(strings.TrimSpace(docType))
	switch {
	case strings.Contains(dt, "FACTURA"):
		return "01"
	case strings.Contains(dt, "BOLETA"):
		return "03"
	case strings.Contains(dt, "NOTA") && strings.Contains(dt, "VENTA"):
		return "00"
	default:
		return ""
	}
}

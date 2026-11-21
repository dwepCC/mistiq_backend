package service

import (
	"strings"
	"testing"
)

func TestValidatePaymentMethodLogo(t *testing.T) {
	ok := []string{"", "data:image/png;base64,iVBORw0KGgo=", "data:image/webp;base64,UklGR", "data:image/jpeg;base64,/9j/4AAQ"}
	for _, v := range ok {
		if err := ValidatePaymentMethodLogo(v); err != nil {
			t.Errorf("%q debería ser válido: %v", v, err)
		}
	}
	bad := []string{"http://x.com/a.png", "data:image/svg+xml;base64,PHN2Zz4=", "data:text/html;base64,PGh0bWw+", "data:image/png;base64,", "javascript:alert(1)",
		"data:image/png;base64," + strings.Repeat("A", MaxPaymentMethodLogoBytes)}
	for _, v := range bad {
		if err := ValidatePaymentMethodLogo(v); err == nil {
			t.Errorf("%.40q debería rechazarse", v)
		}
	}
}

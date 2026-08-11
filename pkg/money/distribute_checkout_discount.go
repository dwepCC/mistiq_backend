package money

// DistributeCheckoutDiscountToLines reparte un descuento global (en base imponible)
// proporcionalmente entre las bases de cada línea.
func DistributeCheckoutDiscountToLines(lineBases []float64, discountAmount float64) []float64 {
	n := len(lineBases)
	if n == 0 {
		return nil
	}
	baseSum := RoundSunat(sumPositive(lineBases))
	disc := RoundSunat(max0(discountAmount))
	if disc > baseSum {
		disc = RoundSunat(baseSum)
	}
	if disc <= 0 || baseSum <= 0 {
		return make([]float64, n)
	}

	result := make([]float64, n)
	remaining := disc
	for i := 0; i < n; i++ {
		if i == n-1 {
			result[i] = RoundSunat(remaining)
			continue
		}
		share := RoundSunat(disc * (max0(lineBases[i]) / baseSum))
		result[i] = share
		remaining = RoundSunat(remaining - share)
	}
	return result
}

func sumPositive(values []float64) float64 {
	var sum float64
	for _, v := range values {
		if v > 0 {
			sum += v
		}
	}
	return sum
}

package http

import (
	"fmt"
	"math/big"
	"strings"
)

var weiPerEther = new(big.Float).SetInt(new(big.Int).Exp(big.NewInt(10), big.NewInt(18), nil))

// FormatWeiToDisplay converts a wei string to human-readable WLC.
// Uses up to 6 decimal places, trims trailing zeros but keeps at least 2.
// Example: "1500000000000000000" → "1.50", "250000000000000" → "0.00025"
func FormatWeiToDisplay(weiStr string) string {
	weiStr = cleanDecimalString(weiStr)
	if weiStr == "" || weiStr == "0" {
		return "0.00"
	}

	weiBig, ok := new(big.Int).SetString(weiStr, 10)
	if !ok {
		return "0.00"
	}

	weiFloat := new(big.Float).SetInt(weiBig)
	result := new(big.Float).Quo(weiFloat, weiPerEther)

	f, _ := result.Float64()
	// Use 6 decimals, then trim trailing zeros (keep minimum 2)
	s := fmt.Sprintf("%.6f", f)
	// Trim trailing zeros after decimal point, keep at least 2 decimal places
	if strings.Contains(s, ".") {
		minLen := strings.Index(s, ".") + 3 // e.g. "0.00" minimum
		s = strings.TrimRight(s, "0")
		if len(s) < minLen {
			s = s + strings.Repeat("0", minLen-len(s))
		}
	}
	return s
}

// FormatWeiPerSecToPerHour converts a wei/second price to WLC/hour with 2 decimals.
// Example: "416666666666666" (wei/sec) → "1.50" (WLC/hr)
func FormatWeiPerSecToPerHour(weiPerSec string) string {
	weiPerSec = cleanDecimalString(weiPerSec)
	if weiPerSec == "" || weiPerSec == "0" {
		return "0.00"
	}

	weiBig, ok := new(big.Int).SetString(weiPerSec, 10)
	if !ok {
		return "0.00"
	}

	// Multiply by 3600 to get wei per hour
	weiPerHour := new(big.Int).Mul(weiBig, big.NewInt(3600))

	// Convert to ether (WLC)
	weiFloat := new(big.Float).SetInt(weiPerHour)
	result := new(big.Float).Quo(weiFloat, weiPerEther)

	f, _ := result.Float64()
	return fmt.Sprintf("%.2f", f)
}

// cleanDecimalString removes decimal points from price strings
func cleanDecimalString(s string) string {
	if idx := strings.Index(s, "."); idx != -1 {
		return s[:idx]
	}
	return s
}

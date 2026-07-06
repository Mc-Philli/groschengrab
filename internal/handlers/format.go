package handlers

import (
	"strconv"
	"strings"
)

// FormatCHF formatiert einen Betrag in Schweizer Schreibweise:
// Apostroph als Tausendertrennzeichen, Punkt als Dezimaltrennzeichen,
// z. B. 1234.5 -> "CHF 1'234.50".
func FormatCHF(amount float64) string {
	sign := ""
	if amount < 0 {
		sign = "-"
		amount = -amount
	}

	// Runden auf Rappen, um Fließkomma-Ungenauigkeiten zu vermeiden.
	cents := int64(amount*100 + 0.5)
	intPart := cents / 100
	fracPart := cents % 100

	intStr := strconv.FormatInt(intPart, 10)
	var grouped strings.Builder
	n := len(intStr)
	for i, ch := range intStr {
		if i > 0 && (n-i)%3 == 0 {
			grouped.WriteByte('\'')
		}
		grouped.WriteRune(ch)
	}

	return sign + "CHF " + grouped.String() + "." + pad2(fracPart)
}

// FormatTransactionAmount berücksichtigt zusätzlich, ob es sich um eine
// Ausgabe handelt, und zeigt den Betrag dann mit Minus an.
func FormatTransactionAmount(kind string, amount float64) string {
	if kind == "expense" {
		amount = -amount
	}
	return FormatCHF(amount)
}

func pad2(n int64) string {
	if n < 10 {
		return "0" + strconv.FormatInt(n, 10)
	}
	return strconv.FormatInt(n, 10)
}

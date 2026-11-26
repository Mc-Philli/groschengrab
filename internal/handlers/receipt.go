package handlers

import (
	"errors"
	"io"
	"log"
	"net/http"
	"time"

	"household-app/internal/receipt"
)

// maxReceiptSize begrenzt Quittungsfotos. Der Browser verkleinert sie vor
// dem Hochladen auf ein paar hundert KB; das Limit greift nur, falls ein
// Originalfoto ankommt.
const maxReceiptSize = 15 << 20

// ocrSlot lässt immer nur eine Texterkennung gleichzeitig laufen, damit der
// Pi nicht mit mehreren parallelen Tesseract-Prozessen in die Knie geht.
var ocrSlot = make(chan struct{}, 1)

// receiptResponse ist die Antwort von POST /api/receipt; leere Felder heißen
// „nicht erkannt".
type receiptResponse struct {
	Amount      float64 `json:"amount,omitempty"`
	Date        string  `json:"date,omitempty"`
	Description string  `json:"description,omitempty"`
	Category    string  `json:"category,omitempty"`
	Text        string  `json:"text,omitempty"`
	Error       string  `json:"error,omitempty"`
}

// ScanReceipt nimmt ein Quittungsfoto entgegen, liest es mit Tesseract und
// gibt Betrag, Datum, Händler und (über die Kategorie-Regeln) eine Kategorie
// zurück. Das Foto wird nicht gespeichert.
func (h *Handlers) ScanReceipt(w http.ResponseWriter, r *http.Request) {
	fail := func(status int, msg string) {
		w.WriteHeader(status)
		respondJSON(w, receiptResponse{Error: msg})
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxReceiptSize)
	file, _, err := r.FormFile("photo")
	if err != nil {
		fail(http.StatusBadRequest, "Kein Foto empfangen (zu groß?).")
		return
	}
	defer file.Close()
	img, err := io.ReadAll(file)
	if err != nil {
		fail(http.StatusBadRequest, "Foto konnte nicht gelesen werden.")
		return
	}

	select {
	case ocrSlot <- struct{}{}:
		defer func() { <-ocrSlot }()
	case <-r.Context().Done():
		return
	}
	start := time.Now()
	text, err := receipt.OCR(r.Context(), img)
	if err != nil {
		if errors.Is(err, receipt.ErrNoOCR) {
			fail(http.StatusServiceUnavailable, "Die Texterkennung ist auf dem Server nicht installiert (sudo apt install tesseract-ocr tesseract-ocr-deu).")
			return
		}
		log.Printf("Quittung: %v", err)
		fail(http.StatusInternalServerError, "Die Texterkennung ist fehlgeschlagen. Bitte nochmal mit einem schärferen Foto versuchen.")
		return
	}
	log.Printf("Quittung erkannt in %s (%d Zeichen)", time.Since(start).Round(time.Millisecond), len(text))

	res := receipt.Parse(text, time.Now())
	resp := receiptResponse{
		Amount:      res.Amount,
		Date:        res.Date,
		Description: res.Merchant,
		Text:        text,
	}
	if rules, err := h.loadRules(); err == nil {
		// Zuerst nur der Händler (genauer), sonst der ganze Bon-Text.
		if resp.Category = matchRule(rules, res.Merchant); resp.Category == "" {
			resp.Category = matchRule(rules, text)
		}
	}
	if resp.Amount == 0 && resp.Date == "" && resp.Description == "" {
		resp.Error = "Auf dem Foto war nichts lesbar. Tipp: Bon flach hinlegen, gutes Licht, nur den Bon im Bild."
	}
	respondJSON(w, resp)
}

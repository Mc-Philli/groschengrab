package receipt

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"sync"
	"time"
)

// ErrNoOCR heißt: Tesseract ist auf diesem Rechner nicht installiert.
var ErrNoOCR = errors.New("Texterkennung (tesseract) ist nicht installiert")

var (
	langOnce sync.Once
	langArg  string
)

// languages wählt die installierten Sprachpakete: Deutsch, falls vorhanden
// (Umlaute, „Rückgeld"), sonst Englisch, das Tesseract immer mitbringt.
func languages() string {
	langOnce.Do(func() {
		langArg = "eng"
		out, err := exec.Command("tesseract", "--list-langs").Output()
		if err != nil {
			return
		}
		have := map[string]bool{}
		for _, l := range strings.Fields(string(out)) {
			have[l] = true
		}
		switch {
		case have["deu"] && have["eng"]:
			langArg = "deu+eng"
		case have["deu"]:
			langArg = "deu"
		}
	})
	return langArg
}

// OCR liest den Text eines Bildes (JPEG/PNG) mit Tesseract. Das Bild wird
// nur über stdin übergeben und nirgends gespeichert.
func OCR(ctx context.Context, img []byte) (string, error) {
	if _, err := exec.LookPath("tesseract"); err != nil {
		return "", ErrNoOCR
	}
	ctx, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()
	// --psm 4: eine Textspalte mit unterschiedlich großen Zeilen, passt zu
	// Kassenbons besser als die automatische Seitenaufteilung.
	cmd := exec.CommandContext(ctx, "tesseract", "stdin", "stdout", "-l", languages(), "--psm", "4")
	cmd.Stdin = bytes.NewReader(img)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			return "", errors.New("Texterkennung hat zu lange gedauert")
		}
		return "", fmt.Errorf("Texterkennung fehlgeschlagen: %v %s", err, strings.TrimSpace(stderr.String()))
	}
	return stdout.String(), nil
}

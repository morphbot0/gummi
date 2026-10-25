package main

import (
	"strings"
	"testing"
)

func TestPrintQRDrawsBlocks(t *testing.T) {
	var b strings.Builder
	printQR(&b, "https://gummi.example.ts.net/")
	if !strings.ContainsAny(b.String(), "▀▄█") {
		t.Fatalf("no QR drawn: %q", b.String())
	}
}

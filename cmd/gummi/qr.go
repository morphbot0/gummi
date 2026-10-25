package main

import (
	"fmt"
	"io"

	qrcode "github.com/skip2/go-qrcode"
)

// printQR draws url as a QR code in half-block characters, so a phone's
// camera can open the board from the terminal. It prints nothing when the
// URL cannot be encoded: the URL itself is already on the line above.
func printQR(w io.Writer, url string) {
	q, err := qrcode.New(url, qrcode.Low)
	if err != nil {
		return
	}
	fmt.Fprint(w, q.ToSmallString(false))
}

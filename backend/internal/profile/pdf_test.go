package profile

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"testing"
)

// A real, minimal text PDF exercises Poppler rather than an extractor stub.
func textPDF(text bool) []byte {
	content := ""
	if text {
		content = "BT /F1 12 Tf 50 760 Td (Synthetic Candidate Resume) Tj 0 -20 Td (Go backend and PostgreSQL experience) Tj ET"
	}
	objects := []string{"<< /Type /Catalog /Pages 2 0 R >>", "<< /Type /Pages /Kids [3 0 R] /Count 1 >>", "<< /Type /Page /Parent 2 0 R /MediaBox [0 0 612 792] /Resources << /Font << /F1 4 0 R >> >> /Contents 5 0 R >>", "<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica >>", fmt.Sprintf("<< /Length %d >>\nstream\n%s\nendstream", len(content), content)}
	var out bytes.Buffer
	out.WriteString("%PDF-1.4\n")
	offsets := []int{0}
	for i, obj := range objects {
		offsets = append(offsets, out.Len())
		fmt.Fprintf(&out, "%d 0 obj\n%s\nendobj\n", i+1, obj)
	}
	xref := out.Len()
	fmt.Fprintf(&out, "xref\n0 %d\n0000000000 65535 f \n", len(offsets))
	for _, offset := range offsets[1:] {
		fmt.Fprintf(&out, "%010d 00000 n \n", offset)
	}
	fmt.Fprintf(&out, "trailer\n<< /Size %d /Root 1 0 R >>\nstartxref\n%d\n%%%%EOF\n", len(offsets), xref)
	return out.Bytes()
}
func TestRealPDFTextAndScannedBoundary(t *testing.T) {
	if _, err := exec.LookPath("pdftotext"); err != nil {
		t.Skip("Poppler required; production verifier installs it")
	}
	text, err := Extract(context.Background(), "synthetic.pdf", textPDF(true))
	if err != nil || !strings.Contains(text, "Go backend") {
		t.Fatalf("PDF extraction=%q %v", text, err)
	}
	if _, err = Extract(context.Background(), "scan.pdf", textPDF(false)); err == nil {
		t.Fatal("empty image-only equivalent accepted")
	}
	if _, err = Extract(context.Background(), "corrupt.pdf", []byte("%PDF-1.4\ncorrupt")); err == nil {
		t.Fatal("corrupt PDF accepted")
	}
}

func TestRealEncryptedPDFIsRejectedEvenWithoutOpenPassword(t *testing.T) {
	if _, err := exec.LookPath("pdfinfo"); err != nil {
		t.Skip("Poppler required; production verifier installs it")
	}
	data, err := os.ReadFile("testdata/owner-encrypted.pdf")
	if err != nil {
		t.Fatal(err)
	}
	info, err := pdfCommand(context.Background(), data, "pdfinfo", "-")
	if err != nil || !strings.Contains(info, "Encrypted:       yes") {
		t.Fatalf("fixture must be an accessible encrypted PDF: %q %v", info, err)
	}
	if _, err = Extract(context.Background(), "encrypted.pdf", data); err != ErrInvalidFile {
		t.Fatalf("encrypted PDF accepted: %v", err)
	}
}

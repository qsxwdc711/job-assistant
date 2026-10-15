package profile

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/xml"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"
)

// ValidateFile verifies size, extension and actual signature before private persistence.
func ValidateFile(name string, data []byte) error {
	if len(data) == 0 || len(data) > MaxFileBytes {
		return ErrInvalidFile
	}
	switch strings.ToLower(filepath.Ext(name)) {
	case ".pdf":
		if !bytes.HasPrefix(data, []byte("%PDF-")) {
			return ErrInvalidFile
		}
	case ".docx":
		if !bytes.HasPrefix(data, []byte("PK\x03\x04")) {
			return ErrInvalidFile
		}
	default:
		return ErrInvalidFile
	}
	return nil
}

// Extract performs bounded local extraction without uploading the document or following its links.
func Extract(ctx context.Context, name string, data []byte) (string, error) {
	if err := ValidateFile(name, data); err != nil {
		return "", err
	}
	var text string
	var err error
	if strings.EqualFold(filepath.Ext(name), ".docx") {
		text, err = extractDOCX(data)
	} else {
		text, err = extractPDF(ctx, data)
	}
	text = strings.TrimSpace(text)
	if err != nil {
		return "", err
	}
	if !utf8.ValidString(text) || utf8.RuneCountInString(text) < 8 || len(text) > MaxTextBytes {
		return "", ErrInvalidFile
	}
	return text, nil
}

func extractDOCX(data []byte) (string, error) {
	reader, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil || len(reader.File) > 512 {
		return "", ErrInvalidFile
	}
	var document *zip.File
	var total uint64
	hasTypes := false
	for _, file := range reader.File {
		total += file.UncompressedSize64
		if total > 32<<20 || file.Flags&1 != 0 || strings.Contains(strings.ToLower(file.Name), "vbaproject") {
			return "", ErrInvalidFile
		}
		if file.Name == "[Content_Types].xml" {
			hasTypes = true
		}
		if file.Name == "word/document.xml" {
			document = file
		}
	}
	if document == nil || !hasTypes {
		return "", ErrInvalidFile
	}
	stream, err := document.Open()
	if err != nil {
		return "", ErrInvalidFile
	}
	defer stream.Close()
	decoder := xml.NewDecoder(io.LimitReader(stream, (32<<20)+1))
	var result strings.Builder
	inText := false
	for {
		token, err := decoder.Token()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return "", ErrInvalidFile
		}
		switch value := token.(type) {
		case xml.StartElement:
			if value.Name.Space == "http://schemas.openxmlformats.org/wordprocessingml/2006/main" {
				switch value.Name.Local {
				case "t":
					inText = true
				case "tab":
					result.WriteByte('\t')
				case "br":
					result.WriteByte('\n')
				}
			}
		case xml.EndElement:
			if value.Name.Local == "t" {
				inText = false
			}
			if value.Name.Local == "p" {
				result.WriteByte('\n')
			}
		case xml.CharData:
			if inText {
				result.Write(value)
			}
		}
		if result.Len() > MaxTextBytes {
			return "", ErrInvalidFile
		}
	}
	return result.String(), nil
}

type limitedBuffer struct {
	bytes.Buffer
	limit int
}

func (b *limitedBuffer) Write(data []byte) (int, error) {
	if b.Len()+len(data) > b.limit {
		return 0, ErrInvalidFile
	}
	return b.Buffer.Write(data)
}

func pdfCommand(ctx context.Context, data []byte, name string, args ...string) (string, error) {
	command := exec.CommandContext(ctx, name, args...)
	command.Env = []string{"PATH=" + os.Getenv("PATH"), "LC_ALL=C"}
	command.Stdin = bytes.NewReader(data)
	out := &limitedBuffer{limit: MaxTextBytes}
	diagnostics := &limitedBuffer{limit: 16 << 10}
	command.Stdout = out
	command.Stderr = diagnostics
	if err := command.Run(); err != nil {
		var execErr *exec.Error
		if errors.As(err, &execErr) {
			return "", ErrExtractorUnavailable
		}
		return "", ErrInvalidFile
	}
	return out.String(), nil
}

func extractPDF(ctx context.Context, data []byte) (string, error) {
	bounded, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	info, err := pdfCommand(bounded, data, "pdfinfo", "-")
	if err != nil {
		return "", err
	}
	if regexp.MustCompile(`(?mi)^Encrypted:\s+yes\b`).MatchString(info) {
		return "", ErrInvalidFile
	}
	return pdfCommand(bounded, data, "pdftotext", "-enc", "UTF-8", "-layout", "-nopgbrk", "-", "-")
}

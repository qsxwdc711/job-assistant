package profile

import (
	"archive/zip"
	"bytes"
	"context"
	"fmt"
	"io"
	"strings"
	"testing"
)

func TestRedactIdentityBeforeExternalProcessing(t *testing.T) {
	source := "姓名：张三\n电话：13812345678\n邮箱：zs@example.com\n身份证：110101199001011234\n详细住址：北京市海淀区中关村路1号\n张三负责 Go 后端开发，2027-06-30 软件工程本科毕业。"
	text := Redact(source, []string{"张三"})
	for _, secret := range []string{"张三", "13812345678", "zs@example.com", "110101199001011234", "中关村路1号"} {
		if strings.Contains(text, secret) {
			t.Fatalf("identity value remained after redaction")
		}
	}
	if !strings.Contains(text, "Go 后端开发") || !strings.Contains(text, "2027-06-30") {
		t.Fatal("job-relevant text was lost")
	}
	if strings.Contains(Redact("李四\n13900001111\n算法和数据结构", nil), "李四") {
		t.Fatal("contact header name remained")
	}
}

func TestDOCXExtractionAndBoundaries(t *testing.T) {
	var buffer bytes.Buffer
	writer := zip.NewWriter(&buffer)
	for name, body := range map[string]string{"[Content_Types].xml": "<Types/>", "word/document.xml": `<w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main"><w:body><w:p><w:r><w:t>Go 后端开发</w:t></w:r></w:p><w:p><w:r><w:t>软件工程</w:t></w:r></w:p></w:body></w:document>`} {
		entry, err := writer.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := entry.Write([]byte(body)); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	text, err := Extract(context.Background(), "resume.docx", buffer.Bytes())
	if err != nil || !strings.Contains(text, "Go 后端开发") || !strings.Contains(text, "软件工程") {
		t.Fatalf("DOCX extraction: %q %v", text, err)
	}
	for _, entry := range []struct {
		name string
		data []byte
	}{{"resume.pdf", buffer.Bytes()}, {"resume.docx", []byte("not zip")}, {"resume.exe", buffer.Bytes()}, {"resume.docx", make([]byte, MaxFileBytes+1)}} {
		if _, err := Extract(context.Background(), entry.name, entry.data); err == nil {
			t.Fatalf("invalid file accepted: %s", entry.name)
		}
	}
}

func TestDraftEvidenceRejectsInventedFacts(t *testing.T) {
	candidate := Candidate{Education: "bachelor", EducationRegion: "domestic", Skills: []string{"Go"}, Evidence: []Evidence{{Field: "education", Quote: "本科"}, {Field: "education_region", Quote: "境内"}, {Field: "skills", Quote: "Go"}}}
	if err := ValidateCandidate(candidate, "境内软件工程本科，使用 Go 开发", true); err != nil {
		t.Fatal(err)
	}
	candidate.Skills = []string{"Rust"}
	if err := ValidateCandidate(candidate, "境内软件工程本科，使用 Go 开发", true); err == nil {
		t.Fatal("invented skill accepted")
	}
}

func TestDOCXExpandedSizeAndArchiveEntryLimits(t *testing.T) {
	for _, many := range []bool{false, true} {
		var buffer bytes.Buffer
		writer := zip.NewWriter(&buffer)
		count := 1
		if many {
			count = 513
		}
		for i := 0; i < count; i++ {
			name := fmt.Sprintf("entry-%d", i)
			entry, err := writer.Create(name)
			if err != nil {
				t.Fatal(err)
			}
			content := "x"
			if !many {
				content = strings.Repeat("x", 33<<20)
			}
			if _, err = io.WriteString(entry, content); err != nil {
				t.Fatal(err)
			}
		}
		if err := writer.Close(); err != nil {
			t.Fatal(err)
		}
		if _, err := Extract(context.Background(), "oversize.docx", buffer.Bytes()); err == nil {
			t.Fatalf("expanded archive accepted (many entries=%v)", many)
		}
	}
}

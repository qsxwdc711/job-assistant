package integration

import (
	"bytes"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"jobassistant/backend/internal/application"
	"jobassistant/backend/internal/catalog"
	"jobassistant/backend/internal/httpapi"
	"jobassistant/backend/internal/identity"
	"jobassistant/backend/internal/profile"
	"jobassistant/backend/internal/task"
)

func TestS3HTTPUploadLimitsOwnershipAndAudit(t *testing.T) {
	f := newS3Fixture(t)
	f.user(t, "s3-ordinary", identity.RoleStandard, nil)
	f.user(t, "s3-other", identity.RoleVIP, nil)
	handler, err := httpapi.NewHandler(f.service, httpapi.Config{CSRFKey: bytes.Repeat([]byte{7}, 32), Origin: "http://app.test"}, f.pool.Ping, httpapi.WithRecruitment(catalog.NewStore(f.pool), application.NewStore(f.pool)), httpapi.WithResumes(f.profiles, f.matches, f.tasks, true, f.model.Version()))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := handler.Close(); err != nil {
			t.Error(err)
		}
	})
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	vip, ordinary, other, admin := newBrowser(t, server), newBrowser(t, server), newBrowser(t, server), newBrowser(t, server)
	vip.login(t, "s3-vip")
	ordinary.login(t, "s3-ordinary")
	other.login(t, "s3-other")
	admin.login(t, "admin")
	upload := func(b *browser, name string, data []byte, csrf bool) (int, []byte) {
		t.Helper()
		var body bytes.Buffer
		writer := multipart.NewWriter(&body)
		part, err := writer.CreateFormFile("file", name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = part.Write(data); err != nil {
			t.Fatal(err)
		}
		if err = writer.WriteField("sensitive_name", "张三"); err != nil {
			t.Fatal(err)
		}
		if err = writer.Close(); err != nil {
			t.Fatal(err)
		}
		req, err := http.NewRequest("POST", server.URL+"/api/v1/me/resumes", &body)
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Content-Type", writer.FormDataContentType())
		if csrf {
			req.Header.Set("X-CSRF-Token", b.csrf)
		}
		response, err := b.client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		content, err := io.ReadAll(response.Body)
		if err != nil {
			t.Fatal(err)
		}
		return response.StatusCode, content
	}
	if status, _ := upload(ordinary, "resume.docx", syntheticDOCX(t), true); status != 403 {
		t.Fatalf("ordinary upload=%d", status)
	}
	if status, _ := upload(vip, "resume.docx", syntheticDOCX(t), false); status != 403 {
		t.Fatalf("upload without CSRF=%d", status)
	}
	if status, _ := upload(vip, "resume.exe", syntheticDOCX(t), true); status != 400 {
		t.Fatalf("invalid type=%d", status)
	}
	if status, _ := upload(vip, "resume.pdf", bytes.Repeat([]byte{'x'}, profile.MaxFileBytes+1), true); status != 413 {
		t.Fatalf("oversize=%d", status)
	}
	status, body := upload(vip, "private.docx", syntheticDOCX(t), true)
	if status != 202 {
		t.Fatalf("upload=%d %s", status, body)
	}
	var created struct {
		Resume profile.Resume `json:"resume"`
	}
	if err = json.Unmarshal(body, &created); err != nil {
		t.Fatal(err)
	}
	id := created.Resume.ID
	f.run(t)
	status, body, _ = vip.request(t, "POST", "/api/v1/me/resumes/"+id+"/parse", map[string]any{"revision": 2, "consent": true}, true)
	var queued struct {
		Task task.Job `json:"task"`
	}
	if err = json.Unmarshal(body, &queued); err != nil || status != 202 || queued.Task.ID == id || queued.Task.Kind != "parse" || queued.Task.State != "queued" {
		t.Fatalf("parse task contract: %d %s %v", status, body, err)
	}
	f.run(t)
	for _, suffix := range []string{"", "/download"} {
		if status, _, _ := other.request(t, "GET", "/api/v1/me/resumes/"+id+suffix, nil, false); status != 404 {
			t.Fatalf("other %s=%d", suffix, status)
		}
		if status, _, _ := ordinary.request(t, "GET", "/api/v1/me/resumes/"+id+suffix, nil, false); status != 403 {
			t.Fatalf("ordinary %s=%d", suffix, status)
		}
	}
	if status, _, _ := other.request(t, "GET", "/api/v1/me/tasks/"+*created.Resume.TaskID, nil, false); status != 404 {
		t.Fatalf("other task=%d", status)
	}
	if status, body, _ := vip.request(t, "GET", "/api/v1/me/tasks/"+*created.Resume.TaskID, nil, false); status != 200 || bytes.Contains(body, []byte("lease_token")) || bytes.Contains(body, []byte("resource_id")) {
		t.Fatalf("task leaked payload: %d %s", status, body)
	}
	status, body, response := vip.request(t, "GET", "/api/v1/me/resumes/"+id+"/download", nil, false)
	if status != 200 || !bytes.Equal(body, syntheticDOCX(t)) || !strings.Contains(response.Header.Get("Content-Disposition"), "attachment") {
		t.Fatalf("authorized download=%d", status)
	}
	for _, suffix := range []string{"", "/download"} {
		if status, _, _ := admin.request(t, "GET", "/api/v1/admin/resumes/"+id+suffix, nil, false); status != 200 {
			t.Fatalf("admin %s=%d", suffix, status)
		}
	}
	var audits string
	if err = f.pool.QueryRow(t.Context(), `SELECT coalesce(jsonb_agg(jsonb_build_object('action',action,'metadata',metadata))::text,'[]') FROM audit_events WHERE action IN ('resume.read','resume.download','resume.upload')`).Scan(&audits); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(audits, "resume.read") || strings.Contains(audits, "张三") || strings.Contains(audits, "synthetic@example.com") || strings.Contains(audits, "private.docx") {
		t.Fatalf("audit missing or contains PII: %s", audits)
	}
	// HTTP authentication re-reads revocation; an old cookie cannot retrieve private text.
	if _, err = f.service.ChangeRole(t.Context(), f.admin, f.vip.ID, identity.RoleStandard, nil, "revoke"); err != nil {
		t.Fatal(err)
	}
	if status, _, _ := vip.request(t, "GET", "/api/v1/me/resumes/"+id, nil, false); status != 403 {
		t.Fatalf("old authorization cookie=%d", status)
	}
	vip.login(t, "s3-vip")
	if status, _, _ := vip.request(t, "GET", "/api/v1/me/resumes/"+id, nil, false); status != 403 {
		t.Fatalf("current ordinary detail=%d", status)
	}
	if status, _, _ := vip.request(t, "DELETE", "/api/v1/me/resumes/"+id, nil, true); status != 204 {
		t.Fatalf("ordinary owner deletion=%d", status)
	}
	if _, err = f.profiles.Detail(t.Context(), profile.Scope{ActorID: f.admin.ID, Admin: true}, id, "test", time.Now()); err != identity.ErrNotFound {
		t.Fatalf("admin read after delete=%v", err)
	}
}

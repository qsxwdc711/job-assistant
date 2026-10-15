package httpapi

import (
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"

	"jobassistant/backend/internal/profile"
)

func (h *Handler) decodePreview(w http.ResponseWriter, r *http.Request, target any) bool {
	media, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || media != "application/json" {
		h.writeError(w, r, 415, "unsupported_media_type", "application/json required")
		return false
	}
	r.Body = http.MaxBytesReader(w, r.Body, profile.MaxTextBytes*6+1024)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err = decoder.Decode(target); err != nil {
		h.writeError(w, r, 400, "invalid_json", "invalid or oversized preview")
		return false
	}
	if !errors.Is(decoder.Decode(&struct{}{}), io.EOF) {
		h.writeError(w, r, 400, "invalid_json", "one JSON object required")
		return false
	}
	return true
}

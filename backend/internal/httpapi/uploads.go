package httpapi

import (
	"bytes"
	"encoding/json"
	"errors"
	"image"
	_ "image/gif"
	"image/jpeg"
	"image/png"
	"io"
	"mime/multipart"
	"net/http"
	"path/filepath"
	"strings"

	"github.com/hackathon/otklik/backend/internal/store"
)

const maxAttachmentSize = 10 << 20

type createAppealRequest struct {
	ApplicantType string         `json:"applicantType"`
	CategoryCode  string         `json:"categoryCode"`
	Body          string         `json:"body"`
	Answers       map[string]any `json:"answers"`
	Email         string         `json:"email"`
}

type chatMessageRequest struct {
	Body string `json:"body"`
}

func decodeAppealRequest(w http.ResponseWriter, r *http.Request) (createAppealRequest, []store.AttachmentInput, bool) {
	var in createAppealRequest
	if !strings.HasPrefix(r.Header.Get("Content-Type"), "multipart/form-data") {
		return in, nil, decode(w, r, &in)
	}
	r.Body = http.MaxBytesReader(w, r.Body, 52<<20)
	if err := r.ParseMultipartForm(52 << 20); err != nil {
		writeError(w, 413, "attachments_too_large", "Можно приложить до 5 изображений размером до 10 МБ")
		return in, nil, false
	}
	defer r.MultipartForm.RemoveAll()
	if err := json.Unmarshal([]byte(r.FormValue("payload")), &in); err != nil {
		writeError(w, 400, "invalid_json", "Не получилось прочитать данные обращения")
		return in, nil, false
	}
	files := r.MultipartForm.File["files"]
	if len(files) > 5 {
		writeError(w, 422, "too_many_attachments", "Можно приложить не больше 5 изображений")
		return in, nil, false
	}
	attachments := make([]store.AttachmentInput, 0, len(files))
	for _, header := range files {
		item, err := sanitizeImage(header)
		if err != nil {
			writeError(w, 422, "invalid_attachment", "Подойдут изображения JPG или PNG размером до 10 МБ")
			return in, nil, false
		}
		attachments = append(attachments, item)
	}
	return in, attachments, true
}

func decodeChatMessageRequest(w http.ResponseWriter, r *http.Request) (chatMessageRequest, []store.AttachmentInput, bool) {
	var in chatMessageRequest
	if !strings.HasPrefix(r.Header.Get("Content-Type"), "multipart/form-data") {
		return in, nil, decode(w, r, &in)
	}
	r.Body = http.MaxBytesReader(w, r.Body, 52<<20)
	if err := r.ParseMultipartForm(52 << 20); err != nil {
		writeError(w, 413, "attachments_too_large", "Можно приложить до 5 изображений размером до 10 МБ")
		return in, nil, false
	}
	defer r.MultipartForm.RemoveAll()
	if err := json.Unmarshal([]byte(r.FormValue("payload")), &in); err != nil {
		writeError(w, 400, "invalid_json", "Не получилось прочитать сообщение")
		return in, nil, false
	}
	files := r.MultipartForm.File["files"]
	if len(files) > 5 {
		writeError(w, 422, "too_many_attachments", "К одному сообщению можно приложить не больше 5 изображений")
		return in, nil, false
	}
	attachments := make([]store.AttachmentInput, 0, len(files))
	for _, header := range files {
		item, err := sanitizeImage(header)
		if err != nil {
			writeError(w, 422, "invalid_attachment", "Подойдут изображения JPG или PNG размером до 10 МБ")
			return in, nil, false
		}
		attachments = append(attachments, item)
	}
	return in, attachments, true
}

// Decoding and re-encoding removes EXIF, GPS and other original metadata.
func sanitizeImage(header *multipart.FileHeader) (store.AttachmentInput, error) {
	if header.Size <= 0 || header.Size > maxAttachmentSize {
		return store.AttachmentInput{}, errors.New("invalid size")
	}
	file, err := header.Open()
	if err != nil {
		return store.AttachmentInput{}, err
	}
	defer file.Close()
	raw, err := io.ReadAll(io.LimitReader(file, maxAttachmentSize+1))
	if err != nil || len(raw) > maxAttachmentSize {
		return store.AttachmentInput{}, errors.New("invalid size")
	}
	config, _, err := image.DecodeConfig(bytes.NewReader(raw))
	if err != nil || config.Width < 1 || config.Height < 1 || int64(config.Width)*int64(config.Height) > 25_000_000 {
		return store.AttachmentInput{}, errors.New("invalid image dimensions")
	}
	img, format, err := image.Decode(bytes.NewReader(raw))
	if err != nil {
		return store.AttachmentInput{}, err
	}
	var clean bytes.Buffer
	contentType := "image/png"
	ext := strings.ToLower(filepath.Ext(header.Filename))
	if format == "jpeg" || ext == ".jpg" || ext == ".jpeg" {
		contentType = "image/jpeg"
		err = jpeg.Encode(&clean, img, &jpeg.Options{Quality: 90})
	} else {
		err = png.Encode(&clean, img)
	}
	if err != nil || clean.Len() == 0 || clean.Len() > maxAttachmentSize {
		return store.AttachmentInput{}, errors.New("cannot sanitize image")
	}
	name := strings.Map(func(r rune) rune {
		if r < 32 || r == '"' || r == '\\' || r == '/' {
			return -1
		}
		return r
	}, filepath.Base(header.Filename))
	if name == "" || name == "." {
		name = "screenshot"
	}
	if contentType == "image/jpeg" {
		name = strings.TrimSuffix(name, filepath.Ext(name)) + ".jpg"
	} else {
		name = strings.TrimSuffix(name, filepath.Ext(name)) + ".png"
	}
	return store.AttachmentInput{FileName: name, ContentType: contentType, Data: clean.Bytes()}, nil
}

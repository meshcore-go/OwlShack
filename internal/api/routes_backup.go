package api

import (
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"net/http"
	"strconv"
)

// maxBackupUpload bounds a restore upload. A full database export of a
// long-running node is a few MB; 256 MB is generous and stops an unbounded
// read into memory.
const maxBackupUpload = 256 << 20

// handleBackupExport builds a backup and streams it as a file download.
//
//	POST /api/backup   { companions, contacts, packetDays, ... }
//
// POST rather than GET because the selection is a document, not a query
// string; the browser downloads it from a blob.
func (s *Server) handleBackupExport(w http.ResponseWriter, r *http.Request) {
	b := s.backendRef()
	if b == nil {
		writeError(w, http.StatusServiceUnavailable, "backend not ready")
		return
	}
	opts, err := decodeBackupOptions(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	file, err := b.ExportBackup(r.Context(), opts)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	w.Header().Set("Content-Type", file.ContentType)
	w.Header().Set("Content-Length", strconv.Itoa(len(file.Data)))
	// quoted so a filename with a space can't truncate the header value
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", file.Name))
	// A backup carries channel keys and passwords; never let a proxy hold it.
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(file.Data)
}

// handleBackupEstimate reports what a selection would capture.
//
//	POST /api/backup/estimate   (same body as the export)
func (s *Server) handleBackupEstimate(w http.ResponseWriter, r *http.Request) {
	b := s.backendRef()
	if b == nil {
		writeError(w, http.StatusServiceUnavailable, "backend not ready")
		return
	}
	opts, err := decodeBackupOptions(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	est, err := b.EstimateBackup(r.Context(), opts)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, est)
}

// handleBackupImport restores an uploaded backup or config file.
//
//	POST /api/backup/import  (multipart field "file", or a raw body)
func (s *Server) handleBackupImport(w http.ResponseWriter, r *http.Request) {
	b := s.backendRef()
	if b == nil {
		writeError(w, http.StatusServiceUnavailable, "backend not ready")
		return
	}

	data, filename, err := readBackupUpload(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	res, err := b.ImportBackup(r.Context(), data, filename)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, res)
}

// decodeBackupOptions reads the selection. Every field is taken as sent — no
// defaults are filled in, because a default that means "include more" turns a
// caller's omission into a bigger backup than they asked for. Presence of
// companionIds is enforced in validateOptions.
func decodeBackupOptions(r *http.Request) (BackupOptions, error) {
	var opts BackupOptions
	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		return opts, fmt.Errorf("reading request: %w", err)
	}
	if len(body) == 0 {
		return opts, fmt.Errorf("a backup selection is required in the request body")
	}
	if err := json.Unmarshal(body, &opts); err != nil {
		return opts, fmt.Errorf("invalid backup options: %w", err)
	}
	return opts, nil
}

// readBackupUpload accepts either a multipart form (what a browser <input
// type=file> posts) or a raw body, so curl works too. The filename is only
// used to pick a config parser.
func readBackupUpload(r *http.Request) ([]byte, string, error) {
	if mr, err := r.MultipartReader(); err == nil {
		for {
			part, err := mr.NextPart()
			if err == io.EOF {
				return nil, "", fmt.Errorf("no file was uploaded")
			}
			if err != nil {
				return nil, "", fmt.Errorf("reading upload: %w", err)
			}
			if part.FormName() != "file" {
				part.Close()
				continue
			}
			name := part.FileName()
			data, err := io.ReadAll(io.LimitReader(part, maxBackupUpload+1))
			part.Close()
			if err != nil {
				return nil, "", fmt.Errorf("reading upload: %w", err)
			}
			data, err = checkUploadSize(data)
			return data, name, err
		}
	}

	data, err := io.ReadAll(io.LimitReader(r.Body, maxBackupUpload+1))
	if err != nil {
		return nil, "", fmt.Errorf("reading upload: %w", err)
	}
	data, err = checkUploadSize(data)
	// A raw POST can still name the file via Content-Disposition.
	name := ""
	if _, params, perr := mime.ParseMediaType(r.Header.Get("Content-Disposition")); perr == nil {
		name = params["filename"]
	}
	return data, name, err
}

func checkUploadSize(data []byte) ([]byte, error) {
	if len(data) > maxBackupUpload {
		return nil, fmt.Errorf("backup is larger than the %d MB limit", maxBackupUpload>>20)
	}
	if len(data) == 0 {
		return nil, fmt.Errorf("no file was uploaded")
	}
	return data, nil
}

package http

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/slabarcaf/flowcard-treasury/services/company-onboarding/internal/domain"
	"github.com/slabarcaf/flowcard-treasury/services/company-onboarding/internal/postgres"
	"github.com/slabarcaf/flowcard-treasury/services/company-onboarding/internal/storage"
)

const (
	maxDocumentSizeBytes = 10 * 1024 * 1024 // 10 MiB local size limit
	maxMultipartHeaderMB = 512 * 1024       // 512 KiB bounded form header limit
)

// handleUploadDocument handles streaming multipart upload for an application document.
// Enforces 10 MiB limit, content sniffing, safe filenames, and storage compensation on DB failure.
func (s *Server) handleUploadDocument(w http.ResponseWriter, r *http.Request) {
	actor, err := GetActor(r)
	if err != nil {
		writeJSON(w, http.StatusUnauthorized, ErrorResponse{Code: "UNAUTHENTICATED", Message: err.Error()})
		return
	}

	appID := r.PathValue("id")
	app, err := s.repo.GetApplication(r.Context(), appID)
	if err != nil {
		if errors.Is(err, postgres.ErrApplicationNotFound) {
			writeJSON(w, http.StatusNotFound, ErrorResponse{Code: "NOT_FOUND", Message: "application not found"})
			return
		}
		writeJSON(w, http.StatusInternalServerError, ErrorResponse{Code: "PERSISTENCE_ERROR", Message: "an internal persistence error occurred"})
		return
	}

	// Application state validation
	if app.Status() == domain.ApplicationStatusCompleted ||
		app.Status() == domain.ApplicationStatusRejected ||
		app.Status() == domain.ApplicationStatusCancelled {
		writeJSON(w, http.StatusConflict, ErrorResponse{Code: "APPLICATION_TERMINAL", Message: "cannot attach documents to terminal application"})
		return
	}
	if app.Status() != domain.ApplicationStatusDraft && app.Status() != domain.ApplicationStatusInformationRequired {
		writeJSON(w, http.StatusConflict, ErrorResponse{Code: "APPLICATION_NOT_EDITABLE", Message: "documents can only be attached in DRAFT or INFORMATION_REQUIRED status"})
		return
	}

	// Fetch CURRENT live company to validate person and identification references for both DRAFT and INFORMATION_REQUIRED
	comp, compErr := s.repo.GetCompany(r.Context(), app.Company().ID())
	if compErr != nil {
		if errors.Is(compErr, postgres.ErrCompanyNotFound) {
			writeJSON(w, http.StatusNotFound, ErrorResponse{Code: "NOT_FOUND", Message: "company not found"})
			return
		}
		writeJSON(w, http.StatusInternalServerError, ErrorResponse{Code: "PERSISTENCE_ERROR", Message: "an internal persistence error occurred"})
		return
	}

	// Bound overall request body to prevent unbounded streaming memory attacks
	r.Body = http.MaxBytesReader(w, r.Body, maxDocumentSizeBytes+maxMultipartHeaderMB)

	mr, err := r.MultipartReader()
	if err != nil {
		writeJSON(w, http.StatusBadRequest, ErrorResponse{Code: "MALFORMED_INPUT", Message: "invalid multipart request format"})
		return
	}

	// Spool upload into temporary file on disk rather than accumulating in memory
	tmpFile, err := os.CreateTemp("", "flowcard-doc-upload-*")
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, ErrorResponse{Code: "INTERNAL_ERROR", Message: "failed to create temporary upload buffer"})
		return
	}
	defer func() {
		_ = tmpFile.Close()
		_ = os.Remove(tmpFile.Name())
	}()

	var (
		docType          string
		personID         string
		identificationID string
		expectedRevision int = app.Revision()
		fileFound        bool
		cleanFileName    string
		sniffedMime      string
		fileSize         int64
		partCount        int
	)
	seenFields := make(map[string]bool)

	// Stream through all multipart parts to the terminal boundary (io.EOF) BEFORE writing to storage
	for {
		part, err := mr.NextPart()
		if err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			var maxBytesErr *http.MaxBytesError
			if errors.As(err, &maxBytesErr) {
				writeJSON(w, http.StatusUnprocessableEntity, ErrorResponse{Code: "DOCUMENT_SIZE_EXCEEDED", Message: "document exceeds maximum allowed size of 10 MiB"})
				return
			}
			writeJSON(w, http.StatusBadRequest, ErrorResponse{Code: "MALFORMED_INPUT", Message: "failed to read multipart stream"})
			return
		}

		partCount++
		if partCount > 20 {
			_ = part.Close()
			writeJSON(w, http.StatusBadRequest, ErrorResponse{Code: "MALFORMED_INPUT", Message: "too many multipart parts"})
			return
		}

		formName := part.FormName()
		if formName == "" {
			_ = part.Close()
			writeJSON(w, http.StatusBadRequest, ErrorResponse{Code: "MALFORMED_INPUT", Message: "empty form field name"})
			return
		}

		if seenFields[formName] {
			_ = part.Close()
			writeJSON(w, http.StatusBadRequest, ErrorResponse{Code: "MALFORMED_INPUT", Message: fmt.Sprintf("duplicate form field %q", formName)})
			return
		}
		seenFields[formName] = true

		if formName == "file" {
			fileFound = true
			cleanFileName = sanitizeFilename(part.FileName())

			// Read first 512 bytes for content sniffing
			headerBuf := make([]byte, 512)
			n, readErr := io.ReadFull(part, headerBuf)
			if readErr != nil && !errors.Is(readErr, io.EOF) && !errors.Is(readErr, io.ErrUnexpectedEOF) {
				_ = part.Close()
				var maxBytesErr *http.MaxBytesError
				if errors.As(readErr, &maxBytesErr) {
					writeJSON(w, http.StatusUnprocessableEntity, ErrorResponse{Code: "DOCUMENT_SIZE_EXCEEDED", Message: "document exceeds maximum allowed size of 10 MiB"})
					return
				}
				writeJSON(w, http.StatusBadRequest, ErrorResponse{Code: "MALFORMED_INPUT", Message: "file upload stream was truncated"})
				return
			}

			if n == 0 {
				_ = part.Close()
				writeJSON(w, http.StatusBadRequest, ErrorResponse{Code: "MISSING_FILE", Message: "uploaded file contains no data"})
				return
			}

			sniffed := sniffAllowedMIME(headerBuf[:n])
			if sniffed == "" {
				_ = part.Close()
				writeJSON(w, http.StatusUnprocessableEntity, ErrorResponse{Code: "UNSUPPORTED_DOCUMENT_TYPE", Message: "only PDF, JPEG, and PNG documents are supported"})
				return
			}

			// MIME spoof check: verify client-declared Content-Type does not contradict verified magic bytes
			declaredType := part.Header.Get("Content-Type")
			if declaredType != "" {
				mediatype, _, _ := mime.ParseMediaType(declaredType)
				if mediatype != "" && mediatype != "application/octet-stream" && mediatype != sniffed {
					_ = part.Close()
					writeJSON(w, http.StatusUnprocessableEntity, ErrorResponse{Code: "MIME_TYPE_MISMATCH", Message: "declared content type does not match verified binary content"})
					return
				}
			}

			sniffedMime = sniffed

			// Write sniffed header to tmpFile
			if _, err := tmpFile.Write(headerBuf[:n]); err != nil {
				_ = part.Close()
				writeJSON(w, http.StatusInternalServerError, ErrorResponse{Code: "INTERNAL_ERROR", Message: "failed to write to temporary file"})
				return
			}
			fileSize = int64(n)

			// Stream remainder of file into tmpFile, bounded by (maxDocumentSizeBytes - n + 1)
			remainingLimit := maxDocumentSizeBytes - int64(n) + 1
			lr := io.LimitReader(part, remainingLimit)
			copied, copyErr := io.Copy(tmpFile, lr)
			if copyErr != nil {
				_ = part.Close()
				var maxBytesErr *http.MaxBytesError
				if errors.As(copyErr, &maxBytesErr) {
					writeJSON(w, http.StatusUnprocessableEntity, ErrorResponse{Code: "DOCUMENT_SIZE_EXCEEDED", Message: "document exceeds maximum allowed size of 10 MiB"})
					return
				}
				writeJSON(w, http.StatusBadRequest, ErrorResponse{Code: "MALFORMED_INPUT", Message: "file upload stream was truncated"})
				return
			}
			fileSize += copied
			if fileSize > maxDocumentSizeBytes {
				_ = part.Close()
				writeJSON(w, http.StatusUnprocessableEntity, ErrorResponse{Code: "DOCUMENT_SIZE_EXCEEDED", Message: "document exceeds maximum allowed size of 10 MiB"})
				return
			}

			_ = part.Close()
			continue
		}

		// Check allowed non-file field names
		switch formName {
		case "type", "person_id", "identification_id", "expected_revision":
			// Allowed field
		default:
			_ = part.Close()
			writeJSON(w, http.StatusBadRequest, ErrorResponse{Code: "MALFORMED_INPUT", Message: fmt.Sprintf("unknown form field %q", formName)})
			return
		}

		// Read small form fields with bound
		fieldBytes, readErr := io.ReadAll(io.LimitReader(part, 4096))
		if readErr != nil {
			_ = part.Close()
			writeJSON(w, http.StatusBadRequest, ErrorResponse{Code: "MALFORMED_INPUT", Message: "failed to read form field"})
			return
		}
		_ = part.Close()

		val := strings.TrimSpace(string(fieldBytes))
		switch formName {
		case "type":
			docType = val
		case "person_id":
			personID = val
		case "identification_id":
			identificationID = val
		case "expected_revision":
			rev, pErr := strconv.Atoi(val)
			if pErr != nil || rev <= 0 {
				writeJSON(w, http.StatusBadRequest, ErrorResponse{Code: "MALFORMED_INPUT", Message: "expected_revision must be a positive integer"})
				return
			}
			expectedRevision = rev
		}
	}

	if !fileFound || fileSize == 0 {
		writeJSON(w, http.StatusBadRequest, ErrorResponse{Code: "MISSING_FILE", Message: "multipart request must contain a non-empty 'file' part"})
		return
	}

	if docType == "" {
		writeJSON(w, http.StatusUnprocessableEntity, ErrorResponse{Code: "VALIDATION_FAILED", Message: "document type is required"})
		return
	}

	// Validate references against live company
	if docType == "IDENTITY_DOCUMENT" {
		if personID == "" {
			writeJSON(w, http.StatusUnprocessableEntity, ErrorResponse{Code: "VALIDATION_FAILED", Message: "person_id is required for identity documents"})
			return
		}
		if identificationID == "" {
			writeJSON(w, http.StatusUnprocessableEntity, ErrorResponse{Code: "VALIDATION_FAILED", Message: "identification_id is required for identity documents"})
			return
		}
	}

	if identificationID != "" && personID == "" {
		writeJSON(w, http.StatusUnprocessableEntity, ErrorResponse{Code: "VALIDATION_FAILED", Message: "person_id is required when identification_id is provided"})
		return
	}

	if personID != "" {
		p, ok := comp.Person(personID)
		if !ok {
			writeJSON(w, http.StatusUnprocessableEntity, ErrorResponse{Code: "VALIDATION_FAILED", Message: fmt.Sprintf("person %q does not exist in company", personID)})
			return
		}
		if identificationID != "" {
			foundIdent := false
			for _, idn := range p.Identifications {
				if idn.ID == identificationID {
					foundIdent = true
					break
				}
			}
			if !foundIdent {
				writeJSON(w, http.StatusUnprocessableEntity, ErrorResponse{Code: "VALIDATION_FAILED", Message: fmt.Sprintf("identification %q does not exist on person %q", identificationID, personID)})
				return
			}
		}
	}

	docID, err := generateID("doc")
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, ErrorResponse{Code: "INTERNAL_ERROR", Message: "failed to generate document ID"})
		return
	}

	storageKey, err := storage.GenerateKey(app.ID(), docID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, ErrorResponse{Code: "INTERNAL_ERROR", Message: "failed to generate storage key"})
		return
	}

	// Rewind tempfile before streaming to storage
	if _, err := tmpFile.Seek(0, io.SeekStart); err != nil {
		writeJSON(w, http.StatusInternalServerError, ErrorResponse{Code: "INTERNAL_ERROR", Message: "failed to rewind temporary upload buffer"})
		return
	}

	// Step 1: Write bytes to object store BEFORE updating database
	putErr := s.storage.Put(r.Context(), storageKey, tmpFile, fileSize, sniffedMime)
	if putErr != nil {
		if errors.Is(putErr, storage.ErrObjectExists) {
			writeJSON(w, http.StatusConflict, ErrorResponse{Code: "OBJECT_EXISTS", Message: "storage key already exists; overwrite rejected"})
			return
		}
		if errors.Is(putErr, storage.ErrStorageUnconfigured) {
			writeJSON(w, http.StatusServiceUnavailable, ErrorResponse{Code: "STORAGE_UNAVAILABLE", Message: "document storage is unconfigured or unavailable"})
			return
		}
		writeJSON(w, http.StatusInternalServerError, ErrorResponse{Code: "STORAGE_ERROR", Message: "an internal storage error occurred"})
		return
	}

	// Step 2: Attach to domain aggregate
	doc := domain.Document{
		ID:               docID,
		Type:             docType,
		PersonID:         personID,
		IdentificationID: identificationID,
		FileName:         cleanFileName,
		ContentType:      sniffedMime,
		SizeBytes:        fileSize,
		StorageKey:       storageKey,
		UploadedBy:       actor,
		UploadedAt:       time.Now().UTC(),
	}

	if err := app.AttachDocumentWithCompany(doc, comp); err != nil {
		// Confirmed domain rejection before DB update: compensate using bounded context
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if delErr := s.storage.Delete(cleanupCtx, storageKey); delErr != nil {
			log.Printf("audit: storage cleanup failed for unreferenced key %s: %v", storageKey, delErr)
		}
		writeJSON(w, http.StatusBadRequest, ErrorResponse{Code: "DOMAIN_ERROR", Message: err.Error()})
		return
	}

	// Step 3: Persist application metadata in database
	if err := s.repo.UpdateApplication(r.Context(), app, expectedRevision); err != nil {
		// CRITICAL COMPENSATION: Delete newly written storage object ONLY on confirmed rollbacks (e.g. CAS conflict).
		// On ambiguous errors (context cancellation, DB network disconnection during commit, etc.),
		// the transaction might have committed on the server. Do NOT delete the object!
		if errors.Is(err, postgres.ErrOptimisticLockConflict) {
			cleanupCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if delErr := s.storage.Delete(cleanupCtx, storageKey); delErr != nil {
				log.Printf("audit: storage cleanup failed for unreferenced key %s: %v", storageKey, delErr)
			}
			writeJSON(w, http.StatusConflict, ErrorResponse{Code: "OPTIMISTIC_LOCK_CONFLICT", Message: "application revision mismatch; record updated concurrently"})
			return
		}

		// Ambiguous persistence failure: retain object in storage to prevent deleting committed files
		log.Printf("audit: application update returned ambiguous error, retaining storage key %s: %v", storageKey, err)
		writeJSON(w, http.StatusInternalServerError, ErrorResponse{Code: "PERSISTENCE_ERROR", Message: "an internal persistence error occurred"})
		return
	}

	// Audit log write without PII or file bytes - ONLY AFTER persisted DB metadata
	log.Printf("audit: document uploaded: actor=%s app_id=%s doc_id=%s doc_type=%s size_bytes=%d content_type=%s",
		actor, app.ID(), doc.ID, doc.Type, doc.SizeBytes, doc.ContentType)

	writeJSON(w, http.StatusCreated, toDocumentResponse(doc))
}

// handleDownloadDocument streams an encrypted private object directly to client with attachment disposition and nosniff.
// Enforces cross-application access denial and does not expose public URLs or signed links.
func (s *Server) handleDownloadDocument(w http.ResponseWriter, r *http.Request) {
	actor, err := GetActor(r)
	if err != nil {
		writeJSON(w, http.StatusUnauthorized, ErrorResponse{Code: "UNAUTHENTICATED", Message: err.Error()})
		return
	}

	appID := r.PathValue("id")
	docID := r.PathValue("doc_id")

	app, err := s.repo.GetApplication(r.Context(), appID)
	if err != nil {
		if errors.Is(err, postgres.ErrApplicationNotFound) {
			writeJSON(w, http.StatusNotFound, ErrorResponse{Code: "NOT_FOUND", Message: "application not found"})
			return
		}
		writeJSON(w, http.StatusInternalServerError, ErrorResponse{Code: "PERSISTENCE_ERROR", Message: "an internal persistence error occurred"})
		return
	}

	// Find document strictly within the application's owned documents (cross-app protection)
	var targetDoc *domain.Document
	for _, d := range app.Documents() {
		if d.ID == docID {
			targetDoc = &d
			break
		}
	}
	if targetDoc == nil {
		writeJSON(w, http.StatusNotFound, ErrorResponse{Code: "NOT_FOUND", Message: "document not found in application"})
		return
	}

	rc, _, err := s.storage.Get(r.Context(), targetDoc.StorageKey)
	if err != nil {
		if errors.Is(err, storage.ErrObjectNotFound) {
			writeJSON(w, http.StatusNotFound, ErrorResponse{Code: "NOT_FOUND", Message: "document content not found in storage"})
			return
		}
		if errors.Is(err, storage.ErrStorageUnconfigured) {
			writeJSON(w, http.StatusServiceUnavailable, ErrorResponse{Code: "STORAGE_UNAVAILABLE", Message: "document storage is unconfigured or unavailable"})
			return
		}
		writeJSON(w, http.StatusInternalServerError, ErrorResponse{Code: "STORAGE_ERROR", Message: "an internal storage error occurred"})
		return
	}
	defer rc.Close()

	// Safe attachment disposition and nosniff protection
	w.Header().Set("Content-Type", targetDoc.ContentType)
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s"`, targetDoc.FileName))
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Length", strconv.FormatInt(targetDoc.SizeBytes, 10))

	// Stream file bytes bounded to metadata size
	lr := io.LimitReader(rc, targetDoc.SizeBytes)
	n, copyErr := io.Copy(w, lr)
	if copyErr != nil {
		log.Printf("failed to stream document content: %v", copyErr)
		return
	}
	if n != targetDoc.SizeBytes {
		log.Printf("audit: document stream truncated or size mismatch: expected=%d actual=%d", targetDoc.SizeBytes, n)
		return
	}

	// Audit log read ONLY after successful stream
	log.Printf("audit: document accessed: actor=%s app_id=%s doc_id=%s", actor, appID, targetDoc.ID)
}

// handleListDocuments lists metadata for all documents attached to an application.
func (s *Server) handleListDocuments(w http.ResponseWriter, r *http.Request) {
	_, err := GetActor(r)
	if err != nil {
		writeJSON(w, http.StatusUnauthorized, ErrorResponse{Code: "UNAUTHENTICATED", Message: err.Error()})
		return
	}

	appID := r.PathValue("id")
	app, err := s.repo.GetApplication(r.Context(), appID)
	if err != nil {
		if errors.Is(err, postgres.ErrApplicationNotFound) {
			writeJSON(w, http.StatusNotFound, ErrorResponse{Code: "NOT_FOUND", Message: "application not found"})
			return
		}
		writeJSON(w, http.StatusInternalServerError, ErrorResponse{Code: "PERSISTENCE_ERROR", Message: "an internal persistence error occurred"})
		return
	}

	docs := app.Documents()
	resp := make([]DocumentResponse, len(docs))
	for i, d := range docs {
		resp[i] = toDocumentResponse(d)
	}

	writeJSON(w, http.StatusOK, resp)
}

// sniffAllowedMIME detects magic bytes and returns the verified MIME type.
// Strictly limits allowed container formats to PDF, JPEG, and PNG.
func sniffAllowedMIME(buf []byte) string {
	if len(buf) < 3 {
		return ""
	}
	// PDF: %PDF-
	if len(buf) >= 4 && bytes.HasPrefix(buf, []byte("%PDF-")) {
		return "application/pdf"
	}
	// JPEG: \xFF\xD8\xFF
	if len(buf) >= 3 && buf[0] == 0xFF && buf[1] == 0xD8 && buf[2] == 0xFF {
		return "image/jpeg"
	}
	// PNG: \x89PNG\r\n\x1a\n
	if len(buf) >= 8 && bytes.HasPrefix(buf, []byte("\x89PNG\r\n\x1a\n")) {
		return "image/png"
	}
	return ""
}

// sanitizeFilename strips directory traversal characters, backslashes, quotes, and control chars.
func sanitizeFilename(raw string) string {
	base := filepath.Base(raw)
	var clean strings.Builder
	for _, r := range base {
		if r >= 32 && r != 127 && r != '\\' && r != '/' && r != '"' {
			clean.WriteRune(r)
		}
	}
	res := strings.TrimSpace(clean.String())
	if len(res) > 255 {
		res = res[:255]
	}
	if res == "" || res == "." {
		return "document.bin"
	}
	return res
}

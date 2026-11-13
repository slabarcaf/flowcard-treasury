package http_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/textproto"
	"strings"
	"testing"
	"time"

	"github.com/slabarcaf/flowcard-treasury/services/company-onboarding/internal/domain"
	internalhttp "github.com/slabarcaf/flowcard-treasury/services/company-onboarding/internal/http"
	"github.com/slabarcaf/flowcard-treasury/services/company-onboarding/internal/postgres"
	"github.com/slabarcaf/flowcard-treasury/services/company-onboarding/internal/storage"
)

func createMultipartDocRequest(targetURL string, fields map[string]string, fileName, fileMime string, fileContent []byte) *http.Request {
	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)

	for k, v := range fields {
		_ = writer.WriteField(k, v)
	}

	if fileName != "" || len(fileContent) > 0 || fileMime != "" {
		h := make(textproto.MIMEHeader)
		h.Set("Content-Disposition", fmt.Sprintf(`form-data; name="file"; filename="%s"`, fileName))
		if fileMime != "" {
			h.Set("Content-Type", fileMime)
		}
		part, _ := writer.CreatePart(h)
		if len(fileContent) > 0 {
			_, _ = part.Write(fileContent)
		}
	}

	_ = writer.Close()

	req := httptest.NewRequest("POST", targetURL, body)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	req.Header.Set("X-Internal-Token", "valid_internal_test_token")
	req.Header.Set("X-Actor-Id", "operator_jane")
	return req
}

type formPart struct {
	name        string
	value       string
	isFile      bool
	fileName    string
	contentType string
	content     []byte
}

func createOrderedMultipartRequest(targetURL string, parts []formPart) *http.Request {
	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)
	for _, p := range parts {
		if p.isFile {
			h := make(textproto.MIMEHeader)
			h.Set("Content-Disposition", fmt.Sprintf(`form-data; name="%s"; filename="%s"`, p.name, p.fileName))
			if p.contentType != "" {
				h.Set("Content-Type", p.contentType)
			}
			partWriter, _ := writer.CreatePart(h)
			_, _ = partWriter.Write(p.content)
		} else {
			_ = writer.WriteField(p.name, p.value)
		}
	}
	_ = writer.Close()

	req := httptest.NewRequest("POST", targetURL, body)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	req.Header.Set("X-Internal-Token", "valid_internal_test_token")
	req.Header.Set("X-Actor-Id", "operator_jane")
	return req
}

// Helper to seed a Company with person and identification
func helperSeedCompanyAndDraftApp(t *testing.T, repo *postgres.Repository) (string, string, string, string) {
	t.Helper()
	ctx := context.Background()
	compID := fmt.Sprintf("comp_doc_%d", time.Now().UnixNano())
	comp, err := domain.NewDraftCompany(compID, "Document Tech Corp")
	if err != nil {
		t.Fatalf("NewDraftCompany failed: %v", err)
	}

	personID := fmt.Sprintf("p_doc_%d", time.Now().UnixNano())
	identID := fmt.Sprintf("ident_doc_%d", time.Now().UnixNano())
	_ = comp.AddPerson(domain.Person{
		ID:          personID,
		FirstName:   "Alice",
		LastName:    "Smith",
		Role:        "Director",
		DateOfBirth: domain.Date("1985-02-10"),
		Identifications: []domain.Identification{
			{
				ID:             identID,
				Type:           "PASSPORT",
				Number:         "P12345678",
				IssuingCountry: "US",
			},
		},
	})
	if err := repo.CreateCompany(ctx, comp); err != nil {
		t.Fatalf("CreateCompany failed: %v", err)
	}

	appID := fmt.Sprintf("app_doc_%d", time.Now().UnixNano())
	app, err := domain.NewDraftApplication(appID, comp, "operator_jane")
	if err != nil {
		t.Fatalf("NewDraftApplication failed: %v", err)
	}
	if err := repo.CreateApplication(ctx, app); err != nil {
		t.Fatalf("CreateApplication failed: %v", err)
	}

	return compID, appID, personID, identID
}

// 1. Document Upload & Download Success
func TestDocumentUploadAndDownload_Success(t *testing.T) {
	store := storage.NewMemoryStore()
	repo, handler := setupTestServerWithStore(t, store)
	_, appID, _, _ := helperSeedCompanyAndDraftApp(t, repo)

	pdfBytes := []byte("%PDF-1.4\n%test pdf content for onboarding document verification\n%%EOF")
	req := createMultipartDocRequest(
		"/api/onboarding-applications/"+appID+"/documents",
		map[string]string{"type": "BUSINESS_REGISTRATION"},
		"cert.pdf",
		"application/pdf",
		pdfBytes,
	)

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("POST /documents status = %d, want 201; body: %s", rec.Code, rec.Body.String())
	}

	var docResp internalhttp.DocumentResponse
	if err := json.NewDecoder(rec.Body).Decode(&docResp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if docResp.ID == "" {
		t.Fatalf("document ID is empty")
	}
	if docResp.Type != "BUSINESS_REGISTRATION" {
		t.Errorf("Type = %q, want BUSINESS_REGISTRATION", docResp.Type)
	}
	if docResp.ContentType != "application/pdf" {
		t.Errorf("ContentType = %q, want application/pdf", docResp.ContentType)
	}
	if docResp.SizeBytes != int64(len(pdfBytes)) {
		t.Errorf("SizeBytes = %d, want %d", docResp.SizeBytes, len(pdfBytes))
	}
	if docResp.FileName != "cert.pdf" {
		t.Errorf("FileName = %q, want cert.pdf", docResp.FileName)
	}

	// Verify storage contains object at scoped key
	expectedKey := fmt.Sprintf("apps/%s/docs/%s", appID, docResp.ID)
	exists, err := store.Exists(context.Background(), expectedKey)
	if err != nil || !exists {
		t.Errorf("expected storage key %q to exist in store", expectedKey)
	}

	// Download Document
	getReq := httptest.NewRequest("GET", fmt.Sprintf("/api/onboarding-applications/%s/documents/%s", appID, docResp.ID), nil)
	getReq.Header.Set("X-Internal-Token", "valid_internal_test_token")
	getReq.Header.Set("X-Actor-Id", "operator_jane")
	getRec := httptest.NewRecorder()
	handler.ServeHTTP(getRec, getReq)

	if getRec.Code != http.StatusOK {
		t.Fatalf("GET /documents/{id} status = %d, want 200; body: %s", getRec.Code, getRec.Body.String())
	}
	if getRec.Header().Get("Content-Type") != "application/pdf" {
		t.Errorf("Content-Type = %q, want application/pdf", getRec.Header().Get("Content-Type"))
	}
	if getRec.Header().Get("X-Content-Type-Options") != "nosniff" {
		t.Errorf("X-Content-Type-Options = %q, want nosniff", getRec.Header().Get("X-Content-Type-Options"))
	}
	if !strings.Contains(getRec.Header().Get("Content-Disposition"), "attachment") {
		t.Errorf("Content-Disposition header missing attachment: %q", getRec.Header().Get("Content-Disposition"))
	}
	if !bytes.Equal(getRec.Body.Bytes(), pdfBytes) {
		t.Errorf("downloaded content mismatch")
	}
}

// 2. Cross-Application Access Denial
func TestDocumentDownload_CrossApplicationAccessDenied(t *testing.T) {
	store := storage.NewMemoryStore()
	repo, handler := setupTestServerWithStore(t, store)
	_, app1, _, _ := helperSeedCompanyAndDraftApp(t, repo)
	_, app2, _, _ := helperSeedCompanyAndDraftApp(t, repo)

	// Upload document to app1
	pdfBytes := []byte("%PDF-1.4 document for app 1")
	req := createMultipartDocRequest(
		"/api/onboarding-applications/"+app1+"/documents",
		map[string]string{"type": "BUSINESS_REGISTRATION"},
		"app1_doc.pdf",
		"application/pdf",
		pdfBytes,
	)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("upload to app1 failed: %d", rec.Code)
	}
	var docResp internalhttp.DocumentResponse
	_ = json.NewDecoder(rec.Body).Decode(&docResp)

	// Attempt to download app1's document using app2's path
	crossReq := httptest.NewRequest("GET", fmt.Sprintf("/api/onboarding-applications/%s/documents/%s", app2, docResp.ID), nil)
	crossReq.Header.Set("X-Internal-Token", "valid_internal_test_token")
	crossReq.Header.Set("X-Actor-Id", "operator_jane")
	crossRec := httptest.NewRecorder()
	handler.ServeHTTP(crossRec, crossReq)

	if crossRec.Code != http.StatusNotFound {
		t.Errorf("cross-application access status = %d, want 404 NOT_FOUND", crossRec.Code)
	}
}

// 3. Denied Authentication on Upload and Read
func TestDocumentEndpoints_DeniedAuth(t *testing.T) {
	store := storage.NewMemoryStore()
	repo, handler := setupTestServerWithStore(t, store)
	_, appID, _, _ := helperSeedCompanyAndDraftApp(t, repo)

	pdfBytes := []byte("%PDF-1.4 document")

	// Missing auth header on upload
	req := createMultipartDocRequest(
		"/api/onboarding-applications/"+appID+"/documents",
		map[string]string{"type": "BUSINESS_REGISTRATION"},
		"doc.pdf",
		"application/pdf",
		pdfBytes,
	)
	req.Header.Del("X-Internal-Token")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("upload without auth status = %d, want 401", rec.Code)
	}

	// Missing auth header on download
	getReq := httptest.NewRequest("GET", fmt.Sprintf("/api/onboarding-applications/%s/documents/doc_123", appID), nil)
	getRec := httptest.NewRecorder()
	handler.ServeHTTP(getRec, getReq)
	if getRec.Code != http.StatusUnauthorized {
		t.Errorf("download without auth status = %d, want 401", getRec.Code)
	}

	// DenyAllAuthenticator server rejects all document requests
	denyServer := internalhttp.NewServer(repo, internalhttp.DenyAllAuthenticator{}, nil, store)
	denyHandler := denyServer.Routes()
	req = createMultipartDocRequest(
		"/api/onboarding-applications/"+appID+"/documents",
		map[string]string{"type": "BUSINESS_REGISTRATION"},
		"doc.pdf",
		"application/pdf",
		pdfBytes,
	)
	rec = httptest.NewRecorder()
	denyHandler.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("DenyAllAuthenticator upload status = %d, want 401", rec.Code)
	}
}

// 4. Malformed Multipart and Missing File
func TestDocumentUpload_MalformedMultipartAndMissingFile(t *testing.T) {
	repo, handler := setupTestServer(t)
	_, appID, _, _ := helperSeedCompanyAndDraftApp(t, repo)

	// Non-multipart body
	req := httptest.NewRequest("POST", "/api/onboarding-applications/"+appID+"/documents", strings.NewReader("not a multipart body"))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Internal-Token", "valid_internal_test_token")
	req.Header.Set("X-Actor-Id", "operator_jane")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("non-multipart payload status = %d, want 400", rec.Code)
	}

	// Multipart without file part
	req = createMultipartDocRequest(
		"/api/onboarding-applications/"+appID+"/documents",
		map[string]string{"type": "BUSINESS_REGISTRATION"},
		"", // no file
		"",
		nil,
	)
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("missing file part status = %d, want 400", rec.Code)
	}
	var errResp internalhttp.ErrorResponse
	_ = json.NewDecoder(rec.Body).Decode(&errResp)
	if errResp.Code != "MISSING_FILE" {
		t.Errorf("error code = %q, want MISSING_FILE", errResp.Code)
	}
}

// 5. MIME Spoofing and Unsupported Document Types
func TestDocumentUpload_MIMESpoofAndUnsupportedTypes(t *testing.T) {
	store := storage.NewMemoryStore()
	repo, handler := setupTestServerWithStore(t, store)
	_, appID, _, _ := helperSeedCompanyAndDraftApp(t, repo)

	// A. Declared as image/png but actually containing PDF bytes
	pdfBytes := []byte("%PDF-1.4 actual pdf content")
	req := createMultipartDocRequest(
		"/api/onboarding-applications/"+appID+"/documents",
		map[string]string{"type": "BUSINESS_REGISTRATION"},
		"spoof.png",
		"image/png", // Declared PNG
		pdfBytes,    // Actual PDF
	)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnprocessableEntity {
		t.Errorf("MIME spoof status = %d, want 422", rec.Code)
	}
	var errResp internalhttp.ErrorResponse
	_ = json.NewDecoder(rec.Body).Decode(&errResp)
	if errResp.Code != "MIME_TYPE_MISMATCH" {
		t.Errorf("error code = %q, want MIME_TYPE_MISMATCH", errResp.Code)
	}

	// B. Declared as image/jpeg but actually containing plain text/HTML
	htmlBytes := []byte("<html><body>hello</body></html>")
	req = createMultipartDocRequest(
		"/api/onboarding-applications/"+appID+"/documents",
		map[string]string{"type": "BUSINESS_REGISTRATION"},
		"index.html",
		"image/jpeg",
		htmlBytes,
	)
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnprocessableEntity {
		t.Errorf("unsupported HTML file status = %d, want 422", rec.Code)
	}
	_ = json.NewDecoder(rec.Body).Decode(&errResp)
	if errResp.Code != "UNSUPPORTED_DOCUMENT_TYPE" {
		t.Errorf("error code = %q, want UNSUPPORTED_DOCUMENT_TYPE", errResp.Code)
	}

	// C. Executable binary / zip rejected
	zipBytes := []byte("PK\x03\x04ziparchivepayload")
	req = createMultipartDocRequest(
		"/api/onboarding-applications/"+appID+"/documents",
		map[string]string{"type": "BUSINESS_REGISTRATION"},
		"archive.zip",
		"application/zip",
		zipBytes,
	)
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnprocessableEntity {
		t.Errorf("zip file status = %d, want 422", rec.Code)
	}
}

// 6. Size Boundaries: Exact 10 MiB Succeeds, 10 MiB + 1 Byte Fails
func TestDocumentUpload_SizeBoundaries(t *testing.T) {
	store := storage.NewMemoryStore()
	repo, handler := setupTestServerWithStore(t, store)
	_, appID, _, _ := helperSeedCompanyAndDraftApp(t, repo)

	maxBytes := 10 * 1024 * 1024

	// A. Exactly 10 MiB valid PDF succeeds
	exactPDF := make([]byte, maxBytes)
	copy(exactPDF, []byte("%PDF-1.4\n"))
	copy(exactPDF[len(exactPDF)-6:], []byte("%%EOF\n"))

	req := createMultipartDocRequest(
		"/api/onboarding-applications/"+appID+"/documents",
		map[string]string{"type": "BUSINESS_REGISTRATION"},
		"exact_10mb.pdf",
		"application/pdf",
		exactPDF,
	)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("exact 10 MiB upload status = %d, want 201; body: %s", rec.Code, rec.Body.String())
	}

	// B. 10 MiB + 1 byte fails with DOCUMENT_SIZE_EXCEEDED
	oversizedPDF := make([]byte, maxBytes+1)
	copy(oversizedPDF, []byte("%PDF-1.4\n"))
	copy(oversizedPDF[len(oversizedPDF)-6:], []byte("%%EOF\n"))

	req = createMultipartDocRequest(
		"/api/onboarding-applications/"+appID+"/documents",
		map[string]string{"type": "BUSINESS_REGISTRATION"},
		"oversized.pdf",
		"application/pdf",
		oversizedPDF,
	)
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("10 MiB + 1 byte upload status = %d, want 422; body: %s", rec.Code, rec.Body.String())
	}
	var errResp internalhttp.ErrorResponse
	_ = json.NewDecoder(rec.Body).Decode(&errResp)
	if errResp.Code != "DOCUMENT_SIZE_EXCEEDED" {
		t.Errorf("error code = %q, want DOCUMENT_SIZE_EXCEEDED", errResp.Code)
	}
}

// 7. Identity Document Person and Identification Reference Validation
func TestDocumentUpload_IdentityDocumentReferences(t *testing.T) {
	store := storage.NewMemoryStore()
	repo, handler := setupTestServerWithStore(t, store)
	_, appID, personID, identID := helperSeedCompanyAndDraftApp(t, repo)

	pngBytes := []byte("\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDRtestpngcontent")

	// Missing person_id returns 422
	req := createMultipartDocRequest(
		"/api/onboarding-applications/"+appID+"/documents",
		map[string]string{
			"type":              "IDENTITY_DOCUMENT",
			"identification_id": identID,
		},
		"passport.png",
		"image/png",
		pngBytes,
	)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnprocessableEntity {
		t.Errorf("missing person_id status = %d, want 422", rec.Code)
	}

	// Missing identification_id returns 422
	req = createMultipartDocRequest(
		"/api/onboarding-applications/"+appID+"/documents",
		map[string]string{
			"type":      "IDENTITY_DOCUMENT",
			"person_id": personID,
		},
		"passport.png",
		"image/png",
		pngBytes,
	)
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnprocessableEntity {
		t.Errorf("missing identification_id status = %d, want 422", rec.Code)
	}

	// Non-existent person_id returns 422
	req = createMultipartDocRequest(
		"/api/onboarding-applications/"+appID+"/documents",
		map[string]string{
			"type":              "IDENTITY_DOCUMENT",
			"person_id":         "non_existent_person",
			"identification_id": identID,
		},
		"passport.png",
		"image/png",
		pngBytes,
	)
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnprocessableEntity {
		t.Errorf("non-existent person status = %d, want 422", rec.Code)
	}

	// Valid references succeeds
	req = createMultipartDocRequest(
		"/api/onboarding-applications/"+appID+"/documents",
		map[string]string{
			"type":              "IDENTITY_DOCUMENT",
			"person_id":         personID,
			"identification_id": identID,
		},
		"passport.png",
		"image/png",
		pngBytes,
	)
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("valid identity document status = %d, want 201; body: %s", rec.Code, rec.Body.String())
	}
}

// 8. Application State Restrictions
func TestDocumentUpload_ApplicationStateRestrictions(t *testing.T) {
	ctx := context.Background()
	store := storage.NewMemoryStore()
	repo, handler := setupTestServerWithStore(t, store)
	compID, appID, _, _ := helperSeedCompanyAndDraftApp(t, repo)

	pdfBytes := []byte("%PDF-1.4 test document content")

	// Cancel application -> TERMINAL state
	app, _ := repo.GetApplication(ctx, appID)
	_ = app.Cancel("operator_jane")
	_ = repo.UpdateApplication(ctx, app, 1)

	req := createMultipartDocRequest(
		"/api/onboarding-applications/"+appID+"/documents",
		map[string]string{"type": "BUSINESS_REGISTRATION"},
		"doc.pdf",
		"application/pdf",
		pdfBytes,
	)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusConflict {
		t.Fatalf("cancelled app document upload status = %d, want 409", rec.Code)
	}
	var errResp internalhttp.ErrorResponse
	_ = json.NewDecoder(rec.Body).Decode(&errResp)
	if errResp.Code != "APPLICATION_TERMINAL" {
		t.Errorf("error code = %q, want APPLICATION_TERMINAL", errResp.Code)
	}

	// Information Required state -> allows document upload
	comp, _ := repo.GetCompany(ctx, compID)
	appRFI, _ := domain.NewDraftApplication("app_rfi_"+fmt.Sprintf("%d", time.Now().UnixNano()), comp, "creator")
	_ = repo.CreateApplication(ctx, appRFI)
	// Simulate transition to INFORMATION_REQUIRED
	_, _ = appRFI.InitiateInitialSubmission("sub_rfi_init", "creator", nil)
	_ = appRFI.RecordSubmissionAccepted("sub_rfi_init")
	_ = appRFI.ReceiveInformationRequest(domain.InformationRequest{
		ID:        "rfi_1",
		Questions: []domain.RFIQuestion{{ID: "q1", Text: "Please upload registration certificate"}},
	})
	_ = repo.UpdateApplication(ctx, appRFI, 1)

	req = createMultipartDocRequest(
		"/api/onboarding-applications/"+appRFI.ID()+"/documents",
		map[string]string{"type": "BUSINESS_REGISTRATION"},
		"doc_rfi.pdf",
		"application/pdf",
		pdfBytes,
	)
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("upload during INFORMATION_REQUIRED status = %d, want 201; body: %s", rec.Code, rec.Body.String())
	}
}

// 9. Storage Compensation on Database Failure
func TestDocumentUpload_StorageCompensationOnDBFailure(t *testing.T) {
	ctx := context.Background()
	store := storage.NewMemoryStore()
	repo, handler := setupTestServerWithStore(t, store)
	_, appID, _, _ := helperSeedCompanyAndDraftApp(t, repo)

	pdfBytes := []byte("%PDF-1.4 test document content for compensation")

	// Upload document with mismatched expected_revision (triggers CAS optimistic lock conflict in DB)
	req := createMultipartDocRequest(
		"/api/onboarding-applications/"+appID+"/documents",
		map[string]string{
			"type":              "BUSINESS_REGISTRATION",
			"expected_revision": "999", // Intentional revision mismatch
		},
		"comp.pdf",
		"application/pdf",
		pdfBytes,
	)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusConflict {
		t.Fatalf("expected 409 OPTIMISTIC_LOCK_CONFLICT, got %d; body: %s", rec.Code, rec.Body.String())
	}

	// Verify compensation: newly written key must have been deleted from storage
	deletedKeys := store.DeletedKeys()
	if len(deletedKeys) != 1 {
		t.Fatalf("expected 1 deleted key from storage compensation, got %d: %v", len(deletedKeys), deletedKeys)
	}
	if !strings.HasPrefix(deletedKeys[0], "apps/"+appID+"/docs/") {
		t.Errorf("deleted key %q does not match expected app key prefix", deletedKeys[0])
	}

	// Verify database has no document attached
	app, _ := repo.GetApplication(ctx, appID)
	if len(app.Documents()) != 0 {
		t.Errorf("application documents count = %d, want 0", len(app.Documents()))
	}
}

// 10. Storage Failure Aborts Before DB Modification
func TestDocumentUpload_StorageFailureAbortsCleanly(t *testing.T) {
	ctx := context.Background()
	store := storage.NewMemoryStore()
	store.SetFailPut(fmt.Errorf("simulated S3 500 internal error"))

	repo, handler := setupTestServerWithStore(t, store)
	_, appID, _, _ := helperSeedCompanyAndDraftApp(t, repo)

	pdfBytes := []byte("%PDF-1.4 test document")
	req := createMultipartDocRequest(
		"/api/onboarding-applications/"+appID+"/documents",
		map[string]string{"type": "BUSINESS_REGISTRATION"},
		"doc.pdf",
		"application/pdf",
		pdfBytes,
	)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", rec.Code)
	}

	app, _ := repo.GetApplication(ctx, appID)
	if len(app.Documents()) != 0 {
		t.Errorf("application documents count = %d, want 0", len(app.Documents()))
	}
}

// 11. Preservation of Previous Submission Document References
func TestDocumentUpload_PreservationPreviousSubmissionRefs(t *testing.T) {
	ctx := context.Background()
	store := storage.NewMemoryStore()
	repo, handler := setupTestServerWithStore(t, store)
	_, appID, personID, _ := helperSeedCompanyAndDraftApp(t, repo)

	// 1. Upload initial document doc_1
	pdf1 := []byte("%PDF-1.4 document 1")
	req1 := createMultipartDocRequest(
		"/api/onboarding-applications/"+appID+"/documents",
		map[string]string{"type": "BUSINESS_REGISTRATION"},
		"doc1.pdf",
		"application/pdf",
		pdf1,
	)
	rec1 := httptest.NewRecorder()
	handler.ServeHTTP(rec1, req1)
	if rec1.Code != http.StatusCreated {
		t.Fatalf("upload doc1 failed: %d", rec1.Code)
	}
	var doc1Resp internalhttp.DocumentResponse
	_ = json.NewDecoder(rec1.Body).Decode(&doc1Resp)

	// 2. Initiate submission (freezes current doc_1 reference)
	app, _ := repo.GetApplication(ctx, appID)
	_ = app.UpdateOnboardingDetails(domain.PlatformOnboardingDetails{
		PrimaryContactPersonID: personID,
		CustomerAgreements: domain.CustomerAgreements{
			AgreedToTermsAndConditions: true,
			AgreedToDataUsage:          true,
			TermsAndConditions: &domain.TermsAndConditionsAgreement{
				AgreedAt:             time.Now().UTC(),
				ServiceAgreementType: "FULL",
			},
		},
	})
	_ = repo.UpdateApplication(ctx, app, app.Revision()-1)
	app, _ = repo.GetApplication(ctx, appID)

	subID := fmt.Sprintf("sub_prev_%d", time.Now().UnixNano())
	subParams := postgres.InitiateSubmissionParams{
		ApplicationID:       app.ID(),
		ExpectedAppRevision: app.Revision(),
		SubmissionID:        subID,
		SubmittingActor:     "operator_jane",
		DocumentIDs:         []string{doc1Resp.ID},
		OutboxTopic:         "onboarding.submissions",
		OutboxPayload:       []byte(`{"event":"submission_initiated"}`),
		IdempotencyKey:      fmt.Sprintf("idem_%d", time.Now().UnixNano()),
		IdempotencyScope:    "test_scope",
		RequestFingerprint:  "fp_test",
		ResponseStatusCode:  201,
		ResponseBody:        []byte(`{}`),
	}
	_, err := repo.InitiateSubmission(ctx, subParams)
	if err != nil {
		t.Fatalf("InitiateSubmission failed: %v", err)
	}

	// Transition app to INFORMATION_REQUIRED so replacement doc can be uploaded
	app, _ = repo.GetApplication(ctx, appID)
	revBefore := app.Revision()
	if err := app.RecordSubmissionAccepted(subID); err != nil {
		t.Fatalf("RecordSubmissionAccepted failed: %v", err)
	}
	if err := app.ReceiveInformationRequest(domain.InformationRequest{
		ID:        "rfi_sub",
		Questions: []domain.RFIQuestion{{ID: "q1", Text: "Updated document needed"}},
	}); err != nil {
		t.Fatalf("ReceiveInformationRequest failed: %v", err)
	}
	if err := repo.UpdateApplication(ctx, app, revBefore); err != nil {
		t.Fatalf("UpdateApplication failed: %v", err)
	}

	// 3. Upload replacement/new document doc_2
	pdf2 := []byte("%PDF-1.4 replacement document 2")
	req2 := createMultipartDocRequest(
		"/api/onboarding-applications/"+appID+"/documents",
		map[string]string{"type": "BUSINESS_REGISTRATION"},
		"doc2.pdf",
		"application/pdf",
		pdf2,
	)
	rec2 := httptest.NewRecorder()
	handler.ServeHTTP(rec2, req2)
	if rec2.Code != http.StatusCreated {
		t.Fatalf("upload doc2 failed: %d; body: %s", rec2.Code, rec2.Body.String())
	}
	var doc2Resp internalhttp.DocumentResponse
	_ = json.NewDecoder(rec2.Body).Decode(&doc2Resp)

	// 4. Verify previous submission retains original document IDs only!
	sub, err := repo.GetSubmission(ctx, subID)
	if err != nil {
		t.Fatalf("GetSubmission failed: %v", err)
	}
	if len(sub.DocumentIDs) != 1 || sub.DocumentIDs[0] != doc1Resp.ID {
		t.Errorf("submission DocumentIDs = %v, want [%s]", sub.DocumentIDs, doc1Resp.ID)
	}

	// 5. Verify application currently contains both documents
	reloadedApp, _ := repo.GetApplication(ctx, appID)
	if len(reloadedApp.Documents()) != 2 {
		t.Errorf("application documents count = %d, want 2", len(reloadedApp.Documents()))
	}
}

// 12. List Documents Endpoint
func TestDocumentList_MetadataOnly(t *testing.T) {
	store := storage.NewMemoryStore()
	repo, handler := setupTestServerWithStore(t, store)
	_, appID, _, _ := helperSeedCompanyAndDraftApp(t, repo)

	// Upload 2 documents
	pdfBytes := []byte("%PDF-1.4 document content")
	req1 := createMultipartDocRequest(
		"/api/onboarding-applications/"+appID+"/documents",
		map[string]string{"type": "BUSINESS_REGISTRATION"},
		"doc1.pdf",
		"application/pdf",
		pdfBytes,
	)
	rec1 := httptest.NewRecorder()
	handler.ServeHTTP(rec1, req1)
	if rec1.Code != http.StatusCreated {
		t.Fatalf("doc1 upload failed: %d", rec1.Code)
	}

	pngBytes := []byte("\x89PNG\r\n\x1a\npngpayload")
	req2 := createMultipartDocRequest(
		"/api/onboarding-applications/"+appID+"/documents",
		map[string]string{"type": "BUSINESS_REGISTRATION"},
		"doc2.png",
		"image/png",
		pngBytes,
	)
	rec2 := httptest.NewRecorder()
	handler.ServeHTTP(rec2, req2)
	if rec2.Code != http.StatusCreated {
		t.Fatalf("doc2 upload failed: %d", rec2.Code)
	}

	// GET /api/onboarding-applications/{id}/documents
	listReq := httptest.NewRequest("GET", "/api/onboarding-applications/"+appID+"/documents", nil)
	listReq.Header.Set("X-Internal-Token", "valid_internal_test_token")
	listReq.Header.Set("X-Actor-Id", "operator_jane")
	listRec := httptest.NewRecorder()
	handler.ServeHTTP(listRec, listReq)

	if listRec.Code != http.StatusOK {
		t.Fatalf("GET /documents status = %d, want 200", listRec.Code)
	}
	var listResp []internalhttp.DocumentResponse
	if err := json.NewDecoder(listRec.Body).Decode(&listResp); err != nil {
		t.Fatalf("failed to decode list: %v", err)
	}
	if len(listResp) != 2 {
		t.Fatalf("list count = %d, want 2", len(listResp))
	}

	// Verify storage_key is NOT leaked in JSON response
	jsonStr := listRec.Body.String()
	if strings.Contains(jsonStr, "storage_key") {
		t.Errorf("wire response leaks internal storage_key: %s", jsonStr)
	}
}

// 13. File-first multipart ordering
func TestDocumentUpload_FileFirstOrdering(t *testing.T) {
	store := storage.NewMemoryStore()
	repo, handler := setupTestServerWithStore(t, store)
	_, appID, _, _ := helperSeedCompanyAndDraftApp(t, repo)

	pdfBytes := []byte("%PDF-1.4 file first test content\n%%EOF")
	// File comes first, then form fields
	parts := []formPart{
		{
			isFile:      true,
			name:        "file",
			fileName:    "file_first.pdf",
			contentType: "application/pdf",
			content:     pdfBytes,
		},
		{name: "type", value: "BUSINESS_REGISTRATION"},
	}
	req := createOrderedMultipartRequest("/api/onboarding-applications/"+appID+"/documents", parts)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("file-first upload failed: status = %d; body: %s", rec.Code, rec.Body.String())
	}
	var docResp internalhttp.DocumentResponse
	if err := json.NewDecoder(rec.Body).Decode(&docResp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if docResp.FileName != "file_first.pdf" || docResp.Type != "BUSINESS_REGISTRATION" {
		t.Errorf("unexpected docResp: %+v", docResp)
	}
}

// 14. Multiple file parts rejection (400 MALFORMED_INPUT)
func TestDocumentUpload_MultipleFilesRejected(t *testing.T) {
	store := storage.NewMemoryStore()
	repo, handler := setupTestServerWithStore(t, store)
	_, appID, _, _ := helperSeedCompanyAndDraftApp(t, repo)

	pdfBytes := []byte("%PDF-1.4 file content\n%%EOF")
	parts := []formPart{
		{name: "type", value: "BUSINESS_REGISTRATION"},
		{isFile: true, name: "file", fileName: "f1.pdf", contentType: "application/pdf", content: pdfBytes},
		{isFile: true, name: "file", fileName: "f2.pdf", contentType: "application/pdf", content: pdfBytes},
	}
	req := createOrderedMultipartRequest("/api/onboarding-applications/"+appID+"/documents", parts)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("multiple files status = %d, want 400; body: %s", rec.Code, rec.Body.String())
	}
	var errResp internalhttp.ErrorResponse
	_ = json.NewDecoder(rec.Body).Decode(&errResp)
	if errResp.Code != "MALFORMED_INPUT" {
		t.Errorf("error code = %q, want MALFORMED_INPUT", errResp.Code)
	}
}

// 15. Unknown, duplicate, and invalid form fields rejection (400 MALFORMED_INPUT)
func TestDocumentUpload_UnknownDuplicateInvalidFields(t *testing.T) {
	store := storage.NewMemoryStore()
	repo, handler := setupTestServerWithStore(t, store)
	_, appID, _, _ := helperSeedCompanyAndDraftApp(t, repo)
	pdfBytes := []byte("%PDF-1.4 file content\n%%EOF")

	// Unknown field
	unknownParts := []formPart{
		{name: "type", value: "BUSINESS_REGISTRATION"},
		{name: "unapproved_admin_override", value: "true"},
		{isFile: true, name: "file", fileName: "f.pdf", contentType: "application/pdf", content: pdfBytes},
	}
	req := createOrderedMultipartRequest("/api/onboarding-applications/"+appID+"/documents", unknownParts)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("unknown field status = %d, want 400", rec.Code)
	}

	// Duplicate field
	dupParts := []formPart{
		{name: "type", value: "BUSINESS_REGISTRATION"},
		{name: "type", value: "IDENTITY_DOCUMENT"},
		{isFile: true, name: "file", fileName: "f.pdf", contentType: "application/pdf", content: pdfBytes},
	}
	req = createOrderedMultipartRequest("/api/onboarding-applications/"+appID+"/documents", dupParts)
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("duplicate field status = %d, want 400", rec.Code)
	}

	// Invalid expected_revision (non-integer)
	badRevParts := []formPart{
		{name: "type", value: "BUSINESS_REGISTRATION"},
		{name: "expected_revision", value: "not_a_number"},
		{isFile: true, name: "file", fileName: "f.pdf", contentType: "application/pdf", content: pdfBytes},
	}
	req = createOrderedMultipartRequest("/api/onboarding-applications/"+appID+"/documents", badRevParts)
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("invalid expected_revision status = %d, want 400", rec.Code)
	}

	// Invalid expected_revision (negative)
	negRevParts := []formPart{
		{name: "type", value: "BUSINESS_REGISTRATION"},
		{name: "expected_revision", value: "-1"},
		{isFile: true, name: "file", fileName: "f.pdf", contentType: "application/pdf", content: pdfBytes},
	}
	req = createOrderedMultipartRequest("/api/onboarding-applications/"+appID+"/documents", negRevParts)
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("negative expected_revision status = %d, want 400", rec.Code)
	}
}

// 16. Trailing malformed multipart boundary
func TestDocumentUpload_TrailingMalformedBoundary(t *testing.T) {
	store := storage.NewMemoryStore()
	repo, handler := setupTestServerWithStore(t, store)
	_, appID, _, _ := helperSeedCompanyAndDraftApp(t, repo)

	boundary := "---------------------------974767299852498929531610575"
	rawBody := fmt.Sprintf("--%s\r\nContent-Disposition: form-data; name=\"type\"\r\n\r\nBUSINESS_REGISTRATION\r\n--%s\r\nContent-Disposition: form-data; name=\"file\"; filename=\"valid.pdf\"\r\nContent-Type: application/pdf\r\n\r\n%%PDF-1.4 truncated boundary here", boundary, boundary)
	// Notice: omitted closing boundary "--" + boundary + "--\r\n"

	req := httptest.NewRequest("POST", "/api/onboarding-applications/"+appID+"/documents", strings.NewReader(rawBody))
	req.Header.Set("Content-Type", "multipart/form-data; boundary="+boundary)
	req.Header.Set("X-Internal-Token", "valid_internal_test_token")
	req.Header.Set("X-Actor-Id", "operator_jane")

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("trailing malformed boundary status = %d, want 400; body: %s", rec.Code, rec.Body.String())
	}
	var errResp internalhttp.ErrorResponse
	_ = json.NewDecoder(rec.Body).Decode(&errResp)
	if errResp.Code != "MALFORMED_INPUT" {
		t.Errorf("error code = %q, want MALFORMED_INPUT", errResp.Code)
	}
}

// 17. Ambiguous DB failure retains storage object (no blind delete)
func TestDocumentUpload_AmbiguousDBErrorRetainsObject(t *testing.T) {
	store := storage.NewMemoryStore()
	repo, handler := setupTestServerWithStore(t, store)
	_, appID, _, _ := helperSeedCompanyAndDraftApp(t, repo)

	pdfBytes := []byte("%PDF-1.4 test document content for ambiguous failure")
	req := createMultipartDocRequest(
		"/api/onboarding-applications/"+appID+"/documents",
		map[string]string{"type": "BUSINESS_REGISTRATION"},
		"retained.pdf",
		"application/pdf",
		pdfBytes,
	)

	// Cancel context to simulate network drop / canceled request during DB commit
	ctx, cancel := context.WithCancel(req.Context())
	cancel() // pre-canceled context produces context.Canceled in DB
	req = req.WithContext(ctx)

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	// Ambiguous error must NOT delete from storage
	deletedKeys := store.DeletedKeys()
	if len(deletedKeys) != 0 {
		t.Errorf("ambiguous DB error deleted keys: %v, expected 0 (retain on ambiguous)", deletedKeys)
	}
}

// 18. RFI attaching corrected credential validated against LIVE company
func TestDocumentUpload_RFIAttachesOnLiveCompany(t *testing.T) {
	ctx := context.Background()
	store := storage.NewMemoryStore()
	repo, handler := setupTestServerWithStore(t, store)
	compID, appID, _, _ := helperSeedCompanyAndDraftApp(t, repo)

	// 1. Move application to INFORMATION_REQUIRED
	app, _ := repo.GetApplication(ctx, appID)
	_, _ = app.InitiateInitialSubmission("sub_rfi_test", "operator_jane", nil)
	_ = app.RecordSubmissionAccepted("sub_rfi_test")
	_ = app.ReceiveInformationRequest(domain.InformationRequest{
		ID:        "rfi_new_person",
		Questions: []domain.RFIQuestion{{ID: "q1", Text: "Please provide ID for new director"}},
	})
	_ = repo.UpdateApplication(ctx, app, 1)

	// 2. Add a NEW person and identification to the LIVE company in DB
	liveComp, err := repo.GetCompany(ctx, compID)
	if err != nil {
		t.Fatalf("GetCompany failed: %v", err)
	}
	newPersonID := fmt.Sprintf("p_new_%d", time.Now().UnixNano())
	newIdentID := fmt.Sprintf("ident_new_%d", time.Now().UnixNano())
	revBefore := liveComp.Revision()
	err = liveComp.AddPerson(domain.Person{
		ID:          newPersonID,
		FirstName:   "Bob",
		LastName:    "Jones",
		Role:        "Director",
		DateOfBirth: domain.Date("1980-05-15"),
		Identifications: []domain.Identification{
			{
				ID:             newIdentID,
				Type:           "NATIONAL_ID",
				Number:         "N98765432",
				IssuingCountry: "US",
			},
		},
	})
	if err != nil {
		t.Fatalf("AddPerson failed: %v", err)
	}
	if err := repo.UpdateCompany(ctx, liveComp, revBefore); err != nil {
		t.Fatalf("UpdateCompany failed: %v", err)
	}

	// 3. Upload document during INFORMATION_REQUIRED referencing the NEW person and identification on the live company
	pngBytes := []byte("\x89PNG\r\n\x1a\nnew_person_passport")
	req := createMultipartDocRequest(
		"/api/onboarding-applications/"+appID+"/documents",
		map[string]string{
			"type":              "IDENTITY_DOCUMENT",
			"person_id":         newPersonID,
			"identification_id": newIdentID,
		},
		"passport_new.png",
		"image/png",
		pngBytes,
	)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("RFI document upload on live company status = %d, want 201; body: %s", rec.Code, rec.Body.String())
	}
}

// 18b. RFI document upload for new identification roundtrip: upload -> GET app/doc -> respond RFI -> GET again
func TestDocumentUpload_RFILiveCredentialRoundtrip(t *testing.T) {
	ctx := context.Background()
	store := storage.NewMemoryStore()
	repo, handler := setupTestServerWithStore(t, store)
	compID, appID, personID, _ := helperSeedCompanyAndDraftApp(t, repo)

	// Step 1: Upload initial draft document (doc_hist)
	initialDocBytes := []byte("%PDF-1.4 initial historical document\n%%EOF")
	reqHist := createMultipartDocRequest(
		"/api/onboarding-applications/"+appID+"/documents",
		map[string]string{
			"type": "BUSINESS_REGISTRATION",
		},
		"initial_registration.pdf",
		"application/pdf",
		initialDocBytes,
	)
	recHist := httptest.NewRecorder()
	handler.ServeHTTP(recHist, reqHist)
	if recHist.Code != http.StatusCreated {
		t.Fatalf("initial doc upload status = %d, want 201; body: %s", recHist.Code, recHist.Body.String())
	}
	var histDocResp internalhttp.DocumentResponse
	_ = json.NewDecoder(recHist.Body).Decode(&histDocResp)
	histDocID := histDocResp.ID

	// Step 2: Submit initial application (freezes initial Company snapshot)
	app, err := repo.GetApplication(ctx, appID)
	if err != nil {
		t.Fatalf("GetApplication failed: %v", err)
	}
	_ = app.UpdateOnboardingDetails(domain.PlatformOnboardingDetails{
		PrimaryContactPersonID: personID,
		CustomerAgreements: domain.CustomerAgreements{
			AgreedToTermsAndConditions: true,
			AgreedToDataUsage:          true,
			TermsAndConditions: &domain.TermsAndConditionsAgreement{
				AgreedAt:             time.Now().UTC(),
				ServiceAgreementType: "FULL",
			},
		},
	})
	_ = repo.UpdateApplication(ctx, app, app.Revision()-1)
	app, _ = repo.GetApplication(ctx, appID)

	sub1ID := fmt.Sprintf("sub_init_%d", time.Now().UnixNano())
	subParams := postgres.InitiateSubmissionParams{
		ApplicationID:       app.ID(),
		ExpectedAppRevision: app.Revision(),
		SubmissionID:        sub1ID,
		SubmittingActor:     "operator_jane",
		DocumentIDs:         []string{histDocID},
		OutboxTopic:         "onboarding.submissions",
		OutboxPayload:       []byte(`{}`),
		IdempotencyKey:      fmt.Sprintf("idem_init_%d", time.Now().UnixNano()),
		IdempotencyScope:    "test_init",
		RequestFingerprint:  "fp_init",
		ResponseStatusCode:  201,
		ResponseBody:        []byte(`{}`),
	}
	_, err = repo.InitiateSubmission(ctx, subParams)
	if err != nil {
		t.Fatalf("InitiateSubmission initial failed: %v", err)
	}

	// Step 3: Transition application to INFORMATION_REQUIRED (open RFI)
	app, _ = repo.GetApplication(ctx, appID)
	revBefore := app.Revision()
	_ = app.RecordSubmissionAccepted(sub1ID)
	rfiID := fmt.Sprintf("rfi_%d", time.Now().UnixNano())
	qID := fmt.Sprintf("q_%d", time.Now().UnixNano())
	_ = app.ReceiveInformationRequest(domain.InformationRequest{
		ID:        rfiID,
		Questions: []domain.RFIQuestion{{ID: qID, Text: "Please provide passport for director"}},
	})
	if err := repo.UpdateApplication(ctx, app, revBefore); err != nil {
		t.Fatalf("UpdateApplication to RFI failed: %v", err)
	}

	// Step 4: Add NEW identification ID to the LIVE company (absent from initial Company snapshot)
	liveComp, err := repo.GetCompany(ctx, compID)
	if err != nil {
		t.Fatalf("GetCompany failed: %v", err)
	}
	newIdentID := fmt.Sprintf("ident_rfi_new_%d", time.Now().UnixNano())
	liveCompRev := liveComp.Revision()
	p, _ := liveComp.Person(personID)
	p.Identifications = append(p.Identifications, domain.Identification{
		ID:             newIdentID,
		Type:           "PASSPORT",
		Number:         "P99887766",
		IssuingCountry: "US",
	})
	_ = liveComp.UpdatePerson(p)
	if err := repo.UpdateCompany(ctx, liveComp, liveCompRev); err != nil {
		t.Fatalf("UpdateCompany failed: %v", err)
	}

	// Verify that the initial application snapshot still DOES NOT have this new identification ID
	appCheck, _ := repo.GetApplication(ctx, appID)
	pSnap, _ := appCheck.Company().Person(personID)
	for _, ident := range pSnap.Identifications {
		if ident.ID == newIdentID {
			t.Fatalf("initial company snapshot was unexpectedly mutated with new identification ID")
		}
	}

	// Step 5: Upload RFI doc referencing the NEW identification ID absent in initial Company
	rfiDocBytes := []byte("%PDF-1.4 new rfi passport document\n%%EOF")
	reqUploadRFI := createMultipartDocRequest(
		"/api/onboarding-applications/"+appID+"/documents",
		map[string]string{
			"type":              "IDENTITY_DOCUMENT",
			"person_id":         personID,
			"identification_id": newIdentID,
		},
		"rfi_passport.pdf",
		"application/pdf",
		rfiDocBytes,
	)
	recUploadRFI := httptest.NewRecorder()
	handler.ServeHTTP(recUploadRFI, reqUploadRFI)
	if recUploadRFI.Code != http.StatusCreated {
		t.Fatalf("upload RFI doc failed: status = %d; body: %s", recUploadRFI.Code, recUploadRFI.Body.String())
	}
	var rfiDocResp internalhttp.DocumentResponse
	_ = json.NewDecoder(recUploadRFI.Body).Decode(&rfiDocResp)
	rfiDocID := rfiDocResp.ID

	// Step 6: GET app -> Reconstitutes aggregate; must survive with original frozen Company
	reqGetApp := httptest.NewRequest("GET", "/api/onboarding-applications/"+appID, nil)
	reqGetApp.Header.Set("X-Internal-Token", "valid_internal_test_token")
	reqGetApp.Header.Set("X-Actor-Id", "operator_jane")
	recGetApp := httptest.NewRecorder()
	handler.ServeHTTP(recGetApp, reqGetApp)
	if recGetApp.Code != http.StatusOK {
		t.Fatalf("GET application after RFI doc upload failed: status = %d; body: %s", recGetApp.Code, recGetApp.Body.String())
	}

	// GET doc -> downloads the new RFI document
	reqGetDoc := httptest.NewRequest("GET", fmt.Sprintf("/api/onboarding-applications/%s/documents/%s", appID, rfiDocID), nil)
	reqGetDoc.Header.Set("X-Internal-Token", "valid_internal_test_token")
	reqGetDoc.Header.Set("X-Actor-Id", "operator_jane")
	recGetDoc := httptest.NewRecorder()
	handler.ServeHTTP(recGetDoc, reqGetDoc)
	if recGetDoc.Code != http.StatusOK {
		t.Fatalf("GET doc after RFI doc upload failed: status = %d; body: %s", recGetDoc.Code, recGetDoc.Body.String())
	}
	if !bytes.Equal(recGetDoc.Body.Bytes(), rfiDocBytes) {
		t.Errorf("downloaded RFI doc content mismatch")
	}

	// Step 7: Respond to RFI via InitiateSubmission (SubmissionType: RFI_RESPONSE)
	appUnderRFI, _ := repo.GetApplication(ctx, appID)
	rfiSubID := fmt.Sprintf("sub_rfi_%d", time.Now().UnixNano())
	rfiSubParams := postgres.InitiateSubmissionParams{
		ApplicationID:        appUnderRFI.ID(),
		ExpectedAppRevision:  appUnderRFI.Revision(),
		SubmissionID:         rfiSubID,
		SubmittingActor:      "operator_jane",
		DocumentIDs:          []string{rfiDocID},
		InformationRequestID: rfiID,
		Answers:              map[string]string{qID: "Uploaded new passport"},
		OutboxTopic:          "onboarding.submissions",
		OutboxPayload:        []byte(`{}`),
		IdempotencyKey:       fmt.Sprintf("idem_rfi_%d", time.Now().UnixNano()),
		IdempotencyScope:     "test_rfi",
		RequestFingerprint:   "fp_rfi",
		ResponseStatusCode:   201,
		ResponseBody:         []byte(`{}`),
	}
	_, err = repo.InitiateSubmission(ctx, rfiSubParams)
	if err != nil {
		t.Fatalf("InitiateSubmission RFI response failed: %v", err)
	}

	// Step 8: GET app again -> Reconstitutes aggregate; validates all docs against initial + submissions + live context
	reqGetAppAgain := httptest.NewRequest("GET", "/api/onboarding-applications/"+appID, nil)
	reqGetAppAgain.Header.Set("X-Internal-Token", "valid_internal_test_token")
	reqGetAppAgain.Header.Set("X-Actor-Id", "operator_jane")
	recGetAppAgain := httptest.NewRecorder()
	handler.ServeHTTP(recGetAppAgain, reqGetAppAgain)
	if recGetAppAgain.Code != http.StatusOK {
		t.Fatalf("GET application after RFI response failed: status = %d; body: %s", recGetAppAgain.Code, recGetAppAgain.Body.String())
	}

	// Verify all initial historical documents are preserved
	finalApp, err := repo.GetApplication(ctx, appID)
	if err != nil {
		t.Fatalf("GetApplication final failed: %v", err)
	}
	if len(finalApp.Documents()) != 2 {
		t.Fatalf("documents count = %d, want 2 (initial histDoc + rfiDoc)", len(finalApp.Documents()))
	}
	docIDs := map[string]bool{
		finalApp.Documents()[0].ID: true,
		finalApp.Documents()[1].ID: true,
	}
	if !docIDs[histDocID] || !docIDs[rfiDocID] {
		t.Errorf("missing expected documents: want histDocID %s and rfiDocID %s, got %v", histDocID, rfiDocID, docIDs)
	}

	// Verify original initial frozen Company snapshot was NEVER overwritten
	pFinal, _ := finalApp.Company().Person(personID)
	for _, ident := range pFinal.Identifications {
		if ident.ID == newIdentID {
			t.Errorf("application company snapshot was overwritten with new identification ID; expected initial snapshot preserved")
		}
	}

	// Verify RFI response submission contains the updated live company copy with newIdentID
	subRFI, err := repo.GetSubmission(ctx, rfiSubID)
	if err != nil {
		t.Fatalf("GetSubmission failed: %v", err)
	}
	pSub, ok := subRFI.CompanyCopy.Person(personID)
	if !ok {
		t.Fatalf("person not found on RFI submission CompanyCopy")
	}
	foundNewIdentInSub := false
	for _, ident := range pSub.Identifications {
		if ident.ID == newIdentID {
			foundNewIdentInSub = true
			break
		}
	}
	if !foundNewIdentInSub {
		t.Errorf("expected RFI submission CompanyCopy to contain new identification ID %s", newIdentID)
	}
}

// 19. Arbitrary document type cross-validates PersonID and IdentificationID if supplied
func TestDocumentUpload_ArbitraryDocumentTypeValidatesPerson(t *testing.T) {
	store := storage.NewMemoryStore()
	repo, handler := setupTestServerWithStore(t, store)
	_, appID, personID, identID := helperSeedCompanyAndDraftApp(t, repo)
	pdfBytes := []byte("%PDF-1.4 arbitrary doc type\n%%EOF")

	// A. Non-identity document with non-existent person_id returns 422
	req := createMultipartDocRequest(
		"/api/onboarding-applications/"+appID+"/documents",
		map[string]string{
			"type":      "BANK_STATEMENT",
			"person_id": "non_existent_person",
		},
		"bank.pdf",
		"application/pdf",
		pdfBytes,
	)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnprocessableEntity {
		t.Errorf("non-existent person on arbitrary doc type status = %d, want 422", rec.Code)
	}

	// B. Non-identity document with valid person_id but non-existent identification_id returns 422
	req = createMultipartDocRequest(
		"/api/onboarding-applications/"+appID+"/documents",
		map[string]string{
			"type":              "BANK_STATEMENT",
			"person_id":         personID,
			"identification_id": "fake_ident_id",
		},
		"bank.pdf",
		"application/pdf",
		pdfBytes,
	)
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnprocessableEntity {
		t.Errorf("non-existent ident on arbitrary doc type status = %d, want 422", rec.Code)
	}

	// C. Non-identity document with identification_id but missing person_id returns 422
	req = createMultipartDocRequest(
		"/api/onboarding-applications/"+appID+"/documents",
		map[string]string{
			"type":              "BANK_STATEMENT",
			"identification_id": identID,
		},
		"bank.pdf",
		"application/pdf",
		pdfBytes,
	)
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnprocessableEntity {
		t.Errorf("ident without person_id status = %d, want 422", rec.Code)
	}

	// D. Non-identity document with valid person_id and identification_id succeeds
	req = createMultipartDocRequest(
		"/api/onboarding-applications/"+appID+"/documents",
		map[string]string{
			"type":              "BANK_STATEMENT",
			"person_id":         personID,
			"identification_id": identID,
		},
		"bank.pdf",
		"application/pdf",
		pdfBytes,
	)
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("arbitrary doc with valid person/ident status = %d, want 201; body: %s", rec.Code, rec.Body.String())
	}
}

// 20. Streaming download bounded to metadata size
func TestDocumentDownload_BoundedStream(t *testing.T) {
	store := storage.NewMemoryStore()
	repo, handler := setupTestServerWithStore(t, store)
	_, appID, _, _ := helperSeedCompanyAndDraftApp(t, repo)

	pdfBytes := []byte("%PDF-1.4 bounded download test content\n%%EOF")
	req := createMultipartDocRequest(
		"/api/onboarding-applications/"+appID+"/documents",
		map[string]string{"type": "BUSINESS_REGISTRATION"},
		"doc.pdf",
		"application/pdf",
		pdfBytes,
	)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("upload failed: %d", rec.Code)
	}
	var docResp internalhttp.DocumentResponse
	_ = json.NewDecoder(rec.Body).Decode(&docResp)

	// Now download
	getReq := httptest.NewRequest("GET", fmt.Sprintf("/api/onboarding-applications/%s/documents/%s", appID, docResp.ID), nil)
	getReq.Header.Set("X-Internal-Token", "valid_internal_test_token")
	getReq.Header.Set("X-Actor-Id", "operator_jane")
	getRec := httptest.NewRecorder()
	handler.ServeHTTP(getRec, getReq)

	if getRec.Code != http.StatusOK {
		t.Fatalf("download failed: %d", getRec.Code)
	}
	if int64(getRec.Body.Len()) != docResp.SizeBytes {
		t.Errorf("downloaded length = %d, want %d", getRec.Body.Len(), docResp.SizeBytes)
	}
}

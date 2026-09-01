package service

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"ai-localbase/internal/model"
	"ai-localbase/internal/util"
)

const (
	stagedUploadStatusStaged      = "staged"
	stagedUploadStatusProcessing  = "processing"
	stagedUploadStatusConsumed    = "consumed"
	stagedUploadStatusDeleted     = "deleted"
	defaultStagedUploadTTL        = 30 * time.Minute
	defaultStagedProcessingLease  = 2 * time.Minute
	defaultStagedMaxFiles         = 8
	defaultStagedMaxBytesPerOwner = 256 * 1024 * 1024
	defaultStagedMaxBytes         = 1024 * 1024 * 1024
	stagedUploadManifestVersion   = 2
)

var ErrUploadStagingQuotaExceeded = errors.New("upload staging quota exceeded")

type UploadStagingLimits struct {
	MaxFilesPerPrincipal int
	MaxBytesPerPrincipal int64
	MaxBytes             int64
}

type stagingQuotaReservation struct {
	principalKey string
	size         int64
}

type UploadStagingService struct {
	rootDir      string
	manifestPath string
	ttl          time.Duration
	limits       UploadStagingLimits
	manifestErr  error

	mu           sync.RWMutex
	items        map[string]model.StagedUpload
	reservations map[string]stagingQuotaReservation
}

type persistedStagedUpload struct {
	ID                   string `json:"id"`
	FileName             string `json:"fileName"`
	StoredName           string `json:"storedName"`
	Size                 int64  `json:"size"`
	SizeLabel            string `json:"sizeLabel"`
	SHA256               string `json:"sha256"`
	CreatedAt            string `json:"createdAt"`
	ExpiresAt            string `json:"expiresAt"`
	Status               string `json:"status"`
	Source               string `json:"source,omitempty"`
	ConsumedAt           string `json:"consumedAt,omitempty"`
	OwnerUserID          string `json:"ownerUserId,omitempty"`
	OwnerAPIKeyID        string `json:"ownerApiKeyId,omitempty"`
	ProcessingOwner      string `json:"processingOwner,omitempty"`
	ProcessingAttempt    int    `json:"processingAttempt,omitempty"`
	ProcessingLeaseUntil string `json:"processingLeaseUntil,omitempty"`
}

type stagedUploadManifest struct {
	Version int                     `json:"version"`
	Items   []persistedStagedUpload `json:"items"`
}

func NewUploadStagingService(rootDir string, ttl time.Duration) *UploadStagingService {
	return NewUploadStagingServiceWithLimits(rootDir, ttl, UploadStagingLimits{
		MaxFilesPerPrincipal: defaultStagedMaxFiles,
		MaxBytesPerPrincipal: defaultStagedMaxBytesPerOwner,
		MaxBytes:             defaultStagedMaxBytes,
	})
}

func NewUploadStagingServiceWithLimits(rootDir string, ttl time.Duration, limits UploadStagingLimits) *UploadStagingService {
	trimmedRoot := strings.TrimSpace(rootDir)
	if trimmedRoot == "" {
		trimmedRoot = filepath.Join("data", "staging")
	}
	if ttl <= 0 {
		ttl = defaultStagedUploadTTL
	}
	service := &UploadStagingService{
		rootDir:      trimmedRoot,
		manifestPath: filepath.Join(trimmedRoot, "manifest.json"),
		ttl:          ttl,
		limits:       limits,
		items:        map[string]model.StagedUpload{},
		reservations: map[string]stagingQuotaReservation{},
	}
	service.manifestErr = service.loadManifest()
	return service
}

// ManifestLoadError is surfaced by AppService startup diagnostics without
// exposing the manifest contents to API clients.
func (s *UploadStagingService) ManifestLoadError() error {
	if s == nil {
		return nil
	}
	return s.manifestErr
}

func (s *UploadStagingService) StageMultipartFile(file *multipart.FileHeader, source string) (model.StagedUpload, error) {
	return s.StageMultipartFileAs(file, source, AuthPrincipal{})
}

func (s *UploadStagingService) StageMultipartFileAs(file *multipart.FileHeader, source string, owner AuthPrincipal) (model.StagedUpload, error) {
	if s == nil {
		return model.StagedUpload{}, fmt.Errorf("upload staging service is nil")
	}
	if file == nil {
		return model.StagedUpload{}, fmt.Errorf("staged file is nil")
	}

	opened, err := file.Open()
	if err != nil {
		return model.StagedUpload{}, fmt.Errorf("open staged file: %w", err)
	}
	defer opened.Close()

	return s.stageFromReader(file.Filename, file.Size, opened, source, owner)
}

func (s *UploadStagingService) StageBytes(fileName string, content []byte, source string) (model.StagedUpload, error) {
	return s.StageBytesAs(fileName, content, source, AuthPrincipal{})
}

func (s *UploadStagingService) StageBytesAs(fileName string, content []byte, source string, owner AuthPrincipal) (model.StagedUpload, error) {
	if s == nil {
		return model.StagedUpload{}, fmt.Errorf("upload staging service is nil")
	}
	return s.stageFromReader(fileName, int64(len(content)), strings.NewReader(string(content)), source, owner)
}

func (s *UploadStagingService) Get(uploadID string) (model.StagedUpload, error) {
	return s.get(uploadID, false)
}

// GetAs returns a staged upload after checking its owner. It is used by
// durable jobs to snapshot immutable metadata before the worker starts.
func (s *UploadStagingService) GetAs(uploadID string, owner AuthPrincipal) (model.StagedUpload, error) {
	item, err := s.get(uploadID, false)
	if err != nil {
		return model.StagedUpload{}, err
	}
	if !stagedUploadOwnerMatches(item, owner) {
		return model.StagedUpload{}, fmt.Errorf("staged upload is not owned by this principal")
	}
	return item, nil
}

func (s *UploadStagingService) get(uploadID string, allowProcessing bool) (model.StagedUpload, error) {
	if s == nil {
		return model.StagedUpload{}, fmt.Errorf("upload staging service is nil")
	}
	trimmedID := strings.TrimSpace(uploadID)
	if trimmedID == "" {
		return model.StagedUpload{}, fmt.Errorf("upload id is required")
	}

	s.mu.RLock()
	item, ok := s.items[trimmedID]
	s.mu.RUnlock()
	if !ok {
		return model.StagedUpload{}, fmt.Errorf("staged upload not found")
	}
	if isStagedUploadExpired(item) && !(item.Status == stagedUploadStatusProcessing && stagedUploadLeaseActive(item, time.Now().UTC())) {
		return model.StagedUpload{}, fmt.Errorf("staged upload expired")
	}
	if item.Status != stagedUploadStatusStaged && !(allowProcessing && item.Status == stagedUploadStatusProcessing) {
		return model.StagedUpload{}, fmt.Errorf("staged upload is not available")
	}
	if err := s.validateStagedPath(item.Path); err != nil {
		return model.StagedUpload{}, err
	}
	return item, nil
}

// Claim atomically reserves a staged upload for one indexing attempt.
func (s *UploadStagingService) Claim(uploadID string) (model.StagedUpload, error) {
	return s.ClaimAs(uploadID, AuthPrincipal{})
}

func (s *UploadStagingService) ClaimAs(uploadID string, owner AuthPrincipal) (model.StagedUpload, error) {
	return s.ClaimWithLeaseAs(uploadID, owner, util.NextID("staging-worker"), defaultStagedProcessingLease)
}

// ClaimWithLeaseAs atomically claims or renews a staged upload for one worker.
// The lease owner is intentionally separate from the authenticated principal:
// two jobs owned by the same user must not be able to overwrite each other's
// processing state.
func (s *UploadStagingService) ClaimWithLeaseAs(uploadID string, owner AuthPrincipal, leaseOwner string, leaseDuration time.Duration) (model.StagedUpload, error) {
	if s == nil {
		return model.StagedUpload{}, fmt.Errorf("staged upload service is nil")
	}
	trimmedID := strings.TrimSpace(uploadID)
	if trimmedID == "" {
		return model.StagedUpload{}, fmt.Errorf("upload id is required")
	}
	leaseOwner = strings.TrimSpace(leaseOwner)
	if leaseOwner == "" {
		return model.StagedUpload{}, fmt.Errorf("staged upload lease owner is required")
	}
	if leaseDuration <= 0 {
		leaseDuration = defaultStagedProcessingLease
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	item, ok := s.items[trimmedID]
	if !ok {
		return model.StagedUpload{}, fmt.Errorf("staged upload not found")
	}
	now := time.Now().UTC()
	if isStagedUploadExpired(item) && !(item.Status == stagedUploadStatusProcessing && stagedUploadLeaseActive(item, now)) {
		return model.StagedUpload{}, fmt.Errorf("staged upload expired")
	}
	if !stagedUploadOwnerMatches(item, owner) {
		return model.StagedUpload{}, fmt.Errorf("staged upload is not owned by this principal")
	}
	if item.Status == stagedUploadStatusProcessing {
		currentOwner := strings.TrimSpace(item.ProcessingOwner)
		if currentOwner != leaseOwner && stagedUploadLeaseActive(item, now) {
			return model.StagedUpload{}, fmt.Errorf("staged upload is being processed by another worker")
		}
		if currentOwner == leaseOwner {
			item.ProcessingLeaseUntil = now.Add(leaseDuration).Format(time.RFC3339Nano)
			item.ExpiresAt = extendStagedUploadExpiry(item.ExpiresAt, now, s.ttl)
			s.items[trimmedID] = item
			if err := s.saveManifestLocked(); err != nil {
				return model.StagedUpload{}, fmt.Errorf("persist staged upload lease renewal: %w", err)
			}
			return item, nil
		}
	} else if item.Status != stagedUploadStatusStaged {
		return model.StagedUpload{}, fmt.Errorf("staged upload is not available")
	}
	previous := item
	item.Status = stagedUploadStatusProcessing
	item.ProcessingOwner = leaseOwner
	item.ProcessingAttempt++
	item.ProcessingLeaseUntil = now.Add(leaseDuration).Format(time.RFC3339Nano)
	item.ExpiresAt = extendStagedUploadExpiry(item.ExpiresAt, now, s.ttl)
	s.items[trimmedID] = item
	if err := s.saveManifestLocked(); err != nil {
		s.items[trimmedID] = previous
		return model.StagedUpload{}, fmt.Errorf("persist staged upload claim: %w", err)
	}
	return item, nil
}

func (s *UploadStagingService) Release(uploadID string) error {
	return s.ReleaseWithLease(uploadID, "")
}

func (s *UploadStagingService) ReleaseWithLease(uploadID, leaseOwner string) error {
	if s == nil {
		return fmt.Errorf("staged upload service is nil")
	}
	trimmedID := strings.TrimSpace(uploadID)
	if trimmedID == "" {
		return fmt.Errorf("upload id is required")
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	item, ok := s.items[trimmedID]
	if !ok {
		return fmt.Errorf("staged upload not found")
	}
	if item.Status == stagedUploadStatusProcessing {
		if expected := strings.TrimSpace(leaseOwner); expected != "" && strings.TrimSpace(item.ProcessingOwner) != expected {
			return fmt.Errorf("staged upload lease is no longer owned by this worker")
		}
		previous := item
		item.Status = stagedUploadStatusStaged
		item.ProcessingOwner = ""
		item.ProcessingLeaseUntil = ""
		s.items[trimmedID] = item
		if err := s.saveManifestLocked(); err != nil {
			s.items[trimmedID] = previous
			return fmt.Errorf("persist staged upload release: %w", err)
		}
	}
	return nil
}

func (s *UploadStagingService) MarkConsumed(uploadID string) error {
	return s.MarkConsumedWithLease(uploadID, "")
}

func (s *UploadStagingService) MarkConsumedWithLease(uploadID, leaseOwner string) error {
	if s == nil {
		return fmt.Errorf("upload staging service is nil")
	}
	trimmedID := strings.TrimSpace(uploadID)
	if trimmedID == "" {
		return fmt.Errorf("upload id is required")
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	item, ok := s.items[trimmedID]
	if !ok {
		return fmt.Errorf("staged upload not found")
	}
	if expected := strings.TrimSpace(leaseOwner); expected != "" && strings.TrimSpace(item.ProcessingOwner) != expected {
		return fmt.Errorf("staged upload lease is no longer owned by this worker")
	}
	previous := item
	item.Status = stagedUploadStatusConsumed
	item.ConsumedAt = util.NowRFC3339()
	item.ProcessingOwner = ""
	item.ProcessingLeaseUntil = ""
	s.items[trimmedID] = item
	if err := s.saveManifestLocked(); err != nil {
		s.items[trimmedID] = previous
		return fmt.Errorf("persist staged upload consumption: %w", err)
	}
	return nil
}

func (s *UploadStagingService) Delete(uploadID string) error {
	if s == nil {
		return fmt.Errorf("upload staging service is nil")
	}
	trimmedID := strings.TrimSpace(uploadID)
	if trimmedID == "" {
		return fmt.Errorf("upload id is required")
	}

	s.mu.Lock()
	item, ok := s.items[trimmedID]
	if ok {
		delete(s.items, trimmedID)
		if err := s.saveManifestLocked(); err != nil {
			s.items[trimmedID] = item
			s.mu.Unlock()
			return fmt.Errorf("persist staged upload deletion: %w", err)
		}
	}
	s.mu.Unlock()

	if ok && strings.TrimSpace(item.Path) != "" {
		if err := os.Remove(item.Path); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("delete staged file: %w", err)
		}
	}
	return nil
}

// CopyTo copies a staged upload into a permanent application data directory.
// The staged source remains available until the caller completes indexing.
func (s *UploadStagingService) CopyTo(uploadID, destinationDir string) (string, error) {
	return s.CopyToWithLease(uploadID, destinationDir, "")
}

// CopyToWithLease copies the claimed upload and verifies both its size and
// SHA-256 while copying. This closes the gap between a durable claim and the
// filesystem read used for indexing.
func (s *UploadStagingService) CopyToWithLease(uploadID, destinationDir, leaseOwner string) (string, error) {
	if s == nil {
		return "", fmt.Errorf("upload staging service is nil")
	}
	staged, err := s.get(uploadID, true)
	if err != nil {
		return "", err
	}
	if expected := strings.TrimSpace(leaseOwner); expected != "" && strings.TrimSpace(staged.ProcessingOwner) != expected {
		return "", fmt.Errorf("staged upload lease is no longer owned by this worker")
	}
	trimmedDestinationDir := strings.TrimSpace(destinationDir)
	if trimmedDestinationDir == "" {
		return "", fmt.Errorf("destination directory is required")
	}
	if err := os.MkdirAll(trimmedDestinationDir, 0o755); err != nil {
		return "", fmt.Errorf("create permanent upload directory: %w", err)
	}

	fileName := util.SanitizeFilename(staged.FileName)
	if fileName == "" {
		fileName = "upload"
	}
	destination := filepath.Join(trimmedDestinationDir, fmt.Sprintf("%s_%s", util.NextID("upload"), fileName))
	temporary, err := os.CreateTemp(trimmedDestinationDir, ".staged-copy-*")
	if err != nil {
		return "", fmt.Errorf("create permanent upload file: %w", err)
	}
	temporaryPath := temporary.Name()
	cleanupTemporary := true
	defer func() {
		if cleanupTemporary {
			_ = os.Remove(temporaryPath)
		}
	}()

	source, err := os.Open(staged.Path)
	if err != nil {
		_ = temporary.Close()
		return "", fmt.Errorf("open staged upload: %w", err)
	}
	hasher := sha256.New()
	written, copyErr := io.Copy(io.MultiWriter(temporary, hasher), source)
	closeSourceErr := source.Close()
	closeTemporaryErr := temporary.Close()
	if copyErr != nil {
		return "", fmt.Errorf("copy staged upload: %w", copyErr)
	}
	if closeSourceErr != nil {
		return "", fmt.Errorf("close staged upload: %w", closeSourceErr)
	}
	if closeTemporaryErr != nil {
		return "", fmt.Errorf("close permanent upload: %w", closeTemporaryErr)
	}
	actualChecksum := hex.EncodeToString(hasher.Sum(nil))
	if staged.Size != written || (strings.TrimSpace(staged.SHA256) != "" && !strings.EqualFold(staged.SHA256, actualChecksum)) {
		return "", fmt.Errorf("staged upload checksum mismatch")
	}
	if err := os.Rename(temporaryPath, destination); err != nil {
		return "", fmt.Errorf("commit permanent upload: %w", err)
	}
	cleanupTemporary = false
	return destination, nil
}

func (s *UploadStagingService) CleanupExpired() error {
	if s == nil {
		return fmt.Errorf("upload staging service is nil")
	}

	type expiredItem struct {
		id   string
		path string
	}
	items := make([]expiredItem, 0)
	activePaths := map[string]struct{}{}

	now := time.Now().UTC()
	s.mu.Lock()
	removed := make(map[string]model.StagedUpload)
	released := make(map[string]model.StagedUpload)
	for id, item := range s.items {
		expiresAt, err := time.Parse(time.RFC3339, item.ExpiresAt)
		if item.Status == stagedUploadStatusProcessing && stagedUploadLeaseActive(item, now) {
			activePaths[filepath.Clean(item.Path)] = struct{}{}
			continue
		}
		if item.Status == stagedUploadStatusProcessing && err == nil && expiresAt.After(now) {
			item.Status = stagedUploadStatusStaged
			item.ProcessingOwner = ""
			item.ProcessingLeaseUntil = ""
			released[id] = item
			s.items[id] = item
			activePaths[filepath.Clean(item.Path)] = struct{}{}
			continue
		}
		if err != nil || !expiresAt.After(now) {
			items = append(items, expiredItem{id: id, path: item.Path})
			removed[id] = item
			delete(s.items, id)
			continue
		}
		activePaths[filepath.Clean(item.Path)] = struct{}{}
	}
	if len(removed) > 0 || len(released) > 0 {
		if err := s.saveManifestLocked(); err != nil {
			for id, item := range removed {
				s.items[id] = item
			}
			for id, item := range released {
				s.items[id] = item
			}
			s.mu.Unlock()
			return fmt.Errorf("persist staging cleanup: %w", err)
		}
	}
	s.mu.Unlock()

	for _, item := range items {
		if strings.TrimSpace(item.path) == "" {
			continue
		}
		if err := os.Remove(item.path); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("cleanup staged file %s: %w", item.id, err)
		}
	}

	entries, err := os.ReadDir(s.rootDir)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("scan staging directory: %w", err)
	}
	cutoff := now.Add(-s.ttl)
	for _, entry := range entries {
		if !entry.Type().IsRegular() {
			continue
		}
		if entry.Name() == filepath.Base(s.manifestPath) || entry.Name() == filepath.Base(s.manifestPath)+".tmp" {
			continue
		}
		path := filepath.Join(s.rootDir, entry.Name())
		if _, ok := activePaths[filepath.Clean(path)]; ok {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			return fmt.Errorf("inspect staged file %s: %w", entry.Name(), err)
		}
		if info.ModTime().After(cutoff) {
			continue
		}
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("cleanup orphaned staged file %s: %w", entry.Name(), err)
		}
	}
	return nil
}

func (s *UploadStagingService) stageFromReader(fileName string, sizeHint int64, reader io.Reader, source string, owner AuthPrincipal) (model.StagedUpload, error) {
	trimmedName := strings.TrimSpace(fileName)
	if trimmedName == "" {
		return model.StagedUpload{}, fmt.Errorf("file name is required")
	}
	if err := os.MkdirAll(s.rootDir, 0o755); err != nil {
		return model.StagedUpload{}, fmt.Errorf("create staging directory: %w", err)
	}

	uploadID, err := nextUploadID()
	if err != nil {
		return model.StagedUpload{}, err
	}
	principalKey := stagedUploadPrincipalKey(owner)
	reservedSize := sizeHint
	if reservedSize < 0 {
		reservedSize = 0
	}
	s.mu.Lock()
	if err := s.checkQuotaLocked(principalKey, reservedSize); err != nil {
		s.mu.Unlock()
		return model.StagedUpload{}, err
	}
	if s.reservations == nil {
		s.reservations = map[string]stagingQuotaReservation{}
	}
	s.reservations[uploadID] = stagingQuotaReservation{principalKey: principalKey, size: reservedSize}
	s.mu.Unlock()
	reservationActive := true
	defer func() {
		if !reservationActive {
			return
		}
		s.mu.Lock()
		delete(s.reservations, uploadID)
		s.mu.Unlock()
	}()
	temporary, err := os.CreateTemp(s.rootDir, ".staged-upload-*")
	if err != nil {
		return model.StagedUpload{}, fmt.Errorf("create temporary staged file: %w", err)
	}
	temporaryPath := temporary.Name()
	cleanupTemporary := true
	defer func() {
		if cleanupTemporary {
			_ = os.Remove(temporaryPath)
		}
	}()

	hasher := sha256.New()
	written, copyErr := io.Copy(io.MultiWriter(temporary, hasher), reader)
	closeErr := temporary.Close()
	if copyErr != nil {
		return model.StagedUpload{}, fmt.Errorf("write staged file: %w", copyErr)
	}
	if closeErr != nil {
		return model.StagedUpload{}, fmt.Errorf("close staged file: %w", closeErr)
	}
	if written == 0 && sizeHint == 0 {
		return model.StagedUpload{}, fmt.Errorf("staged file is empty")
	}

	createdAt := time.Now().UTC()
	staged := model.StagedUpload{
		ID:            uploadID,
		FileName:      trimmedName,
		Size:          written,
		SizeLabel:     util.FormatFileSize(written),
		SHA256:        hex.EncodeToString(hasher.Sum(nil)),
		CreatedAt:     createdAt.Format(time.RFC3339),
		ExpiresAt:     createdAt.Add(s.ttl).Format(time.RFC3339),
		Status:        stagedUploadStatusStaged,
		Source:        strings.TrimSpace(source),
		OwnerUserID:   strings.TrimSpace(owner.UserID),
		OwnerAPIKeyID: strings.TrimSpace(owner.APIKeyID),
	}

	s.mu.Lock()
	if s.items == nil {
		s.items = map[string]model.StagedUpload{}
	}
	if s.reservations == nil {
		s.reservations = map[string]stagingQuotaReservation{}
	}
	delete(s.reservations, uploadID)
	reservationActive = false
	if err := s.checkQuotaLocked(principalKey, written); err != nil {
		s.mu.Unlock()
		return model.StagedUpload{}, err
	}
	storedName := fmt.Sprintf("%s_%s", uploadID, util.SanitizeFilename(trimmedName))
	destination := filepath.Join(s.rootDir, storedName)
	if err := os.Rename(temporaryPath, destination); err != nil {
		s.mu.Unlock()
		return model.StagedUpload{}, fmt.Errorf("commit staged file: %w", err)
	}
	cleanupTemporary = false
	staged.Path = destination
	s.items[staged.ID] = staged
	if err := s.saveManifestLocked(); err != nil {
		delete(s.items, staged.ID)
		_ = os.Remove(destination)
		s.mu.Unlock()
		return model.StagedUpload{}, fmt.Errorf("persist staged upload: %w", err)
	}
	s.mu.Unlock()

	return staged, nil
}

func (s *UploadStagingService) loadManifest() error {
	content, err := os.ReadFile(s.manifestPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("read staging manifest: %w", err)
	}
	var manifest stagedUploadManifest
	if err := json.Unmarshal(content, &manifest); err != nil {
		return fmt.Errorf("decode staging manifest: %w", err)
	}
	if manifest.Version > stagedUploadManifestVersion {
		return fmt.Errorf("unsupported staging manifest version %d", manifest.Version)
	}
	for _, persisted := range manifest.Items {
		id := strings.TrimSpace(persisted.ID)
		storedName := strings.TrimSpace(persisted.StoredName)
		if id == "" || !isSafeStagedStoredName(storedName) {
			continue
		}
		if storedName == filepath.Base(s.manifestPath) {
			continue
		}
		status := strings.TrimSpace(persisted.Status)
		processingOwner := strings.TrimSpace(persisted.ProcessingOwner)
		if status == stagedUploadStatusProcessing && processingOwner == "" {
			// Version 1 did not persist a processing lease. Treat such entries as
			// abandoned so a restart can safely retry them.
			status = stagedUploadStatusStaged
		}
		s.items[id] = model.StagedUpload{
			ID:                   id,
			FileName:             persisted.FileName,
			Path:                 filepath.Join(s.rootDir, storedName),
			Size:                 persisted.Size,
			SizeLabel:            persisted.SizeLabel,
			SHA256:               persisted.SHA256,
			CreatedAt:            persisted.CreatedAt,
			ExpiresAt:            persisted.ExpiresAt,
			Status:               status,
			Source:               persisted.Source,
			ConsumedAt:           persisted.ConsumedAt,
			OwnerUserID:          persisted.OwnerUserID,
			OwnerAPIKeyID:        persisted.OwnerAPIKeyID,
			ProcessingOwner:      processingOwner,
			ProcessingAttempt:    persisted.ProcessingAttempt,
			ProcessingLeaseUntil: persisted.ProcessingLeaseUntil,
		}
	}
	return nil
}

func (s *UploadStagingService) saveManifestLocked() error {
	if s == nil {
		return fmt.Errorf("upload staging service is nil")
	}
	if err := os.MkdirAll(s.rootDir, 0o755); err != nil {
		return fmt.Errorf("create staging directory for manifest: %w", err)
	}
	ids := make([]string, 0, len(s.items))
	for id := range s.items {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	manifest := stagedUploadManifest{Version: stagedUploadManifestVersion, Items: make([]persistedStagedUpload, 0, len(ids))}
	for _, id := range ids {
		item := s.items[id]
		storedName := filepath.Base(strings.TrimSpace(item.Path))
		if !isSafeStagedStoredName(storedName) {
			continue
		}
		rootPath, rootErr := filepath.Abs(s.rootDir)
		itemPath, itemErr := filepath.Abs(strings.TrimSpace(item.Path))
		relativePath, relativeErr := filepath.Rel(rootPath, itemPath)
		if rootErr != nil || itemErr != nil || relativeErr != nil || relativePath != storedName {
			continue
		}
		manifest.Items = append(manifest.Items, persistedStagedUpload{
			ID:                   item.ID,
			FileName:             item.FileName,
			StoredName:           storedName,
			Size:                 item.Size,
			SizeLabel:            item.SizeLabel,
			SHA256:               item.SHA256,
			CreatedAt:            item.CreatedAt,
			ExpiresAt:            item.ExpiresAt,
			Status:               item.Status,
			Source:               item.Source,
			ConsumedAt:           item.ConsumedAt,
			OwnerUserID:          item.OwnerUserID,
			OwnerAPIKeyID:        item.OwnerAPIKeyID,
			ProcessingOwner:      item.ProcessingOwner,
			ProcessingAttempt:    item.ProcessingAttempt,
			ProcessingLeaseUntil: item.ProcessingLeaseUntil,
		})
	}
	content, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return fmt.Errorf("encode staging manifest: %w", err)
	}
	temporaryPath := s.manifestPath + ".tmp"
	if err := os.WriteFile(temporaryPath, content, 0o600); err != nil {
		return fmt.Errorf("write staging manifest: %w", err)
	}
	if err := os.Rename(temporaryPath, s.manifestPath); err != nil {
		_ = os.Remove(temporaryPath)
		return fmt.Errorf("replace staging manifest: %w", err)
	}
	return nil
}

func isSafeStagedStoredName(name string) bool {
	trimmed := strings.TrimSpace(name)
	if trimmed == "" || trimmed == "." || trimmed == ".." || filepath.IsAbs(trimmed) {
		return false
	}
	if filepath.Base(trimmed) != trimmed || strings.ContainsAny(trimmed, `/\\`) {
		return false
	}
	return trimmed != "manifest.json" && trimmed != "manifest.json.tmp"
}

func (s *UploadStagingService) validateStagedPath(path string) error {
	if s == nil {
		return fmt.Errorf("upload staging service is nil")
	}
	rootPath, err := filepath.Abs(s.rootDir)
	if err != nil {
		return fmt.Errorf("resolve staging root: %w", err)
	}
	itemPath, err := filepath.Abs(strings.TrimSpace(path))
	if err != nil {
		return fmt.Errorf("resolve staged file: %w", err)
	}
	relativePath, err := filepath.Rel(rootPath, itemPath)
	if err != nil || !isSafeStagedStoredName(relativePath) {
		return fmt.Errorf("staged upload path is outside the staging directory")
	}
	info, err := os.Lstat(itemPath)
	if err != nil {
		return fmt.Errorf("staged upload file unavailable: %w", err)
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("staged upload file is not a regular file")
	}
	return nil
}

func (s *UploadStagingService) checkQuotaLocked(principalKey string, size int64) error {
	if s == nil {
		return fmt.Errorf("upload staging service is nil")
	}
	activeFiles := 0
	activeBytes := int64(0)
	totalBytes := int64(0)
	now := time.Now().UTC()
	for _, item := range s.items {
		if item.Status != stagedUploadStatusStaged && item.Status != stagedUploadStatusProcessing {
			continue
		}
		if expiresAt, err := time.Parse(time.RFC3339, item.ExpiresAt); err == nil && !expiresAt.After(now) && !stagedUploadLeaseActive(item, now) {
			continue
		}
		totalBytes += item.Size
		if stagedUploadPrincipalKeyFromItem(item) != principalKey {
			continue
		}
		activeFiles++
		activeBytes += item.Size
	}
	for _, reservation := range s.reservations {
		totalBytes += reservation.size
		if reservation.principalKey != principalKey {
			continue
		}
		activeFiles++
		activeBytes += reservation.size
	}

	if s.limits.MaxFilesPerPrincipal > 0 && activeFiles >= s.limits.MaxFilesPerPrincipal {
		return fmt.Errorf("%w: principal file limit reached (%d)", ErrUploadStagingQuotaExceeded, s.limits.MaxFilesPerPrincipal)
	}
	if s.limits.MaxBytesPerPrincipal > 0 && activeBytes > s.limits.MaxBytesPerPrincipal-size {
		return fmt.Errorf("%w: principal byte limit reached (%s)", ErrUploadStagingQuotaExceeded, util.FormatFileSize(s.limits.MaxBytesPerPrincipal))
	}
	if s.limits.MaxBytes > 0 && totalBytes > s.limits.MaxBytes-size {
		return fmt.Errorf("%w: global byte limit reached (%s)", ErrUploadStagingQuotaExceeded, util.FormatFileSize(s.limits.MaxBytes))
	}
	return nil
}

func stagedUploadPrincipalKey(owner AuthPrincipal) string {
	if apiKeyID := strings.TrimSpace(owner.APIKeyID); apiKeyID != "" {
		return "api-key:" + apiKeyID
	}
	if userID := strings.TrimSpace(owner.UserID); userID != "" {
		return "user:" + userID
	}
	if authType := strings.TrimSpace(owner.AuthType); authType != "" {
		return "auth:" + authType
	}
	return "anonymous"
}

func stagedUploadPrincipalKeyFromItem(item model.StagedUpload) string {
	if apiKeyID := strings.TrimSpace(item.OwnerAPIKeyID); apiKeyID != "" {
		return "api-key:" + apiKeyID
	}
	if userID := strings.TrimSpace(item.OwnerUserID); userID != "" {
		return "user:" + userID
	}
	return "anonymous"
}

func stagedUploadOwnerMatches(item model.StagedUpload, owner AuthPrincipal) bool {
	if strings.TrimSpace(owner.AuthType) == "" {
		return true
	}
	if hasScope(owner.Scopes, "mcp:admin") {
		return true
	}
	if strings.TrimSpace(owner.APIKeyID) != "" {
		return strings.TrimSpace(item.OwnerAPIKeyID) == strings.TrimSpace(owner.APIKeyID)
	}
	if strings.EqualFold(strings.TrimSpace(owner.AuthType), "api_key") || strings.TrimSpace(owner.UserID) == "" {
		return false
	}
	return strings.TrimSpace(item.OwnerAPIKeyID) == "" && strings.TrimSpace(item.OwnerUserID) == strings.TrimSpace(owner.UserID)
}

func hasScope(scopes []string, required string) bool {
	required = strings.ToLower(strings.TrimSpace(required))
	for _, scope := range scopes {
		if strings.ToLower(strings.TrimSpace(scope)) == required {
			return true
		}
	}
	return false
}

func stagedUploadLeaseActive(item model.StagedUpload, now time.Time) bool {
	if item.Status != stagedUploadStatusProcessing || strings.TrimSpace(item.ProcessingOwner) == "" {
		return false
	}
	leaseUntil, err := time.Parse(time.RFC3339, strings.TrimSpace(item.ProcessingLeaseUntil))
	if err != nil {
		return false
	}
	return leaseUntil.After(now.UTC())
}

func extendStagedUploadExpiry(value string, now time.Time, ttl time.Duration) string {
	existing, err := time.Parse(time.RFC3339, strings.TrimSpace(value))
	if err == nil && existing.After(now.UTC()) {
		return existing.Format(time.RFC3339Nano)
	}
	if ttl <= 0 {
		ttl = defaultStagedUploadTTL
	}
	return now.UTC().Add(ttl).Format(time.RFC3339Nano)
}

func isStagedUploadExpired(item model.StagedUpload) bool {
	expiresAt, err := time.Parse(time.RFC3339, item.ExpiresAt)
	if err != nil {
		return true
	}
	return !expiresAt.After(time.Now().UTC())
}

func nextUploadID() (string, error) {
	buffer := make([]byte, 8)
	if _, err := rand.Read(buffer); err != nil {
		return "", fmt.Errorf("generate upload id: %w", err)
	}
	return "upl_" + hex.EncodeToString(buffer), nil
}

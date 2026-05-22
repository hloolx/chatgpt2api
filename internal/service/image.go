package service

import (
	"context"
	"encoding/json"
	"errors"
	"image"
	"image/color"
	"image/draw"
	_ "image/gif"
	"image/jpeg"
	_ "image/png"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"chatgpt2api/internal/storage"
)

const (
	ThumbnailSize         = 480
	thumbnailQuality      = 72
	thumbnailCacheVersion = 3
	thumbnailExtension    = ".jpg"
	imageReferencePrefix  = "references"
	imageReferenceMarker  = ".refs/"

	ImageVisibilityPrivate = "private"
	ImageVisibilityPublic  = "public"
)

type ImageConfig interface {
	ImagesDir() string
	ImageThumbnailsDir() string
	ImageMetadataDir() string
	ImageRetentionDays() int
	ImageStorageLimitBytes() int64
}

type ImageAccessScope struct {
	OwnerID string
	All     bool
	Public  bool
}

type imageMetadata struct {
	OwnerID           string
	OwnerName         string
	Visibility        string
	PublishedAt       string
	Prompt            string
	Model             string
	Quality           string
	ResolutionPreset  string
	RequestedSize     string
	OutputFormat      string
	OutputCompression *int
	Background        string
	Moderation        string
	Style             string
	PartialImages     *int
	InputImageMask    string
	ReferenceImages   []imageReferenceMetadata
	SharePromptParams bool
	ShareReferences   bool
}

type GeneratedImageMetadata struct {
	Prompt            string
	Model             string
	Quality           string
	ResolutionPreset  string
	RequestedSize     string
	OutputFormat      string
	OutputCompression *int
	Background        string
	Moderation        string
	Style             string
	PartialImages     *int
	InputImageMask    string
	ReferenceImages   []GeneratedImageReference
	SharePromptParams bool
	ShareReferences   bool
}

type GeneratedImageReference struct {
	Filename    string
	ContentType string
	Data        []byte
}

type ImageStorageCleanupOptions struct {
	RetentionDays   int
	MaxBytes        int64
	ClearThumbnails bool
	IncludePublic   bool
}

type ImageStorageGovernanceSummary struct {
	TotalBytes         int64  `json:"total_bytes"`
	ImagesBytes        int64  `json:"images_bytes"`
	ThumbnailsBytes    int64  `json:"thumbnails_bytes"`
	MetadataBytes      int64  `json:"metadata_bytes"`
	ReferenceBytes     int64  `json:"reference_bytes"`
	ImagesCount        int    `json:"images_count"`
	PublicImagesCount  int    `json:"public_images_count"`
	PrivateImagesCount int    `json:"private_images_count"`
	ThumbnailFiles     int    `json:"thumbnail_files"`
	MetadataFiles      int    `json:"metadata_files"`
	ReferenceFiles     int    `json:"reference_files"`
	LimitBytes         int64  `json:"limit_bytes"`
	OverLimitBytes     int64  `json:"over_limit_bytes"`
	OldestImageAt      string `json:"oldest_image_at,omitempty"`
	LatestImageAt      string `json:"latest_image_at,omitempty"`
}

type ImageStorageCleanupResult struct {
	RetentionDays         int    `json:"retention_days,omitempty"`
	MaxBytes              int64  `json:"max_bytes,omitempty"`
	IncludePublic         bool   `json:"include_public,omitempty"`
	DeletedImages         int    `json:"deleted_images"`
	DeletedThumbnails     int    `json:"deleted_thumbnails"`
	DeletedMetadataFiles  int    `json:"deleted_metadata_files"`
	DeletedReferenceFiles int    `json:"deleted_reference_files"`
	DeletedBytes          int64  `json:"deleted_bytes"`
	RemainingBytes        int64  `json:"remaining_bytes"`
	OverLimitBytes        int64  `json:"over_limit_bytes"`
	PreservedPublicImages int    `json:"preserved_public_images,omitempty"`
	Action                string `json:"action,omitempty"`
}

type imageReferenceMetadata struct {
	Path        string
	Filename    string
	ContentType string
	Size        int64
}

type ImageFileAccess struct {
	Rel        string
	Path       string
	Info       os.FileInfo
	Visibility string
	OwnerID    string
}

type ImageReferenceFileAccess struct {
	Rel         string
	SourceRel   string
	Path        string
	ContentType string
	Visibility  string
	OwnerID     string
	Shared      bool
}

type ImageVisibilityUpdateOptions struct {
	SharePromptParams bool
	ShareReferences   bool
}

type ImageService struct {
	config          ImageConfig
	store           storage.JSONDocumentBackend
	thumbnailMu     sync.Mutex
	thumbnailJobs   map[string]*thumbnailJob
	cloudStorageRef interface {
		GetRecord(ctx context.Context, imageRel string) (*CloudImageRecord, error)
	}
}

type imageFileRef struct {
	rel  string
	path string
	info os.FileInfo
}

type thumbnailJob struct {
	done   chan struct{}
	result map[string]any
}

type imageCleanupCandidate struct {
	rel       string
	path      string
	info      os.FileInfo
	meta      imageMetadata
	groupSize int64
}

type imageStorageRemovalStats struct {
	bytes          int64
	images         int
	thumbnails     int
	metadataFiles  int
	referenceFiles int
}

func NewImageService(config ImageConfig, backend ...storage.Backend) *ImageService {
	return &ImageService{config: config, store: firstJSONDocumentStore(backend)}
}

// SetCloudStorageRef sets the cloud storage reference for gallery listing.
func (s *ImageService) SetCloudStorageRef(ref interface {
	GetRecord(ctx context.Context, imageRel string) (*CloudImageRecord, error)
}) {
	s.cloudStorageRef = ref
}

func (s *ImageService) StorageGovernance() ImageStorageGovernanceSummary {
	summary := ImageStorageGovernanceSummary{LimitBytes: s.config.ImageStorageLimitBytes()}
	candidates := s.imageCleanupCandidates()
	for _, candidate := range candidates {
		summary.ImagesCount++
		summary.ImagesBytes += candidate.info.Size()
		if candidate.meta.Visibility == ImageVisibilityPublic {
			summary.PublicImagesCount++
		} else {
			summary.PrivateImagesCount++
		}
		created := candidate.info.ModTime().Format("2006-01-02 15:04:05")
		if summary.OldestImageAt == "" || created < summary.OldestImageAt {
			summary.OldestImageAt = created
		}
		if summary.LatestImageAt == "" || created > summary.LatestImageAt {
			summary.LatestImageAt = created
		}
	}
	summary.ThumbnailsBytes, summary.ThumbnailFiles, _ = thumbnailCacheStats(s.config.ImageThumbnailsDir())
	summary.MetadataBytes, summary.MetadataFiles = directorySize(s.config.ImageMetadataDir(), "")
	summary.ReferenceBytes, summary.ReferenceFiles = directorySize(s.imageReferencesDir(), "")
	summary.TotalBytes = summary.ImagesBytes + summary.ThumbnailsBytes + summary.MetadataBytes
	if summary.LimitBytes > 0 && summary.TotalBytes > summary.LimitBytes {
		summary.OverLimitBytes = summary.TotalBytes - summary.LimitBytes
	}
	return summary
}

func (s *ImageService) CleanupStorage(options ImageStorageCleanupOptions) (ImageStorageCleanupResult, error) {
	result := ImageStorageCleanupResult{
		RetentionDays: options.RetentionDays,
		MaxBytes:      options.MaxBytes,
		IncludePublic: options.IncludePublic,
	}
	if options.ClearThumbnails {
		stats, err := s.clearThumbnailCache()
		if err != nil {
			return result, err
		}
		result.Action = "thumbnails"
		result.DeletedThumbnails += stats.thumbnails
		result.DeletedMetadataFiles += stats.metadataFiles
		result.DeletedBytes += stats.bytes
	}
	if options.RetentionDays > 0 {
		stats, preserved, err := s.cleanupByRetention(options.RetentionDays, options.IncludePublic)
		if err != nil {
			return result, err
		}
		if result.Action == "" {
			result.Action = "retention"
		}
		result.addRemovalStats(stats)
		result.PreservedPublicImages += preserved
	}
	if options.MaxBytes > 0 {
		stats, preserved, err := s.cleanupByStorageLimit(options.MaxBytes, options.IncludePublic)
		if err != nil {
			return result, err
		}
		if result.Action == "" {
			result.Action = "quota"
		}
		result.addRemovalStats(stats)
		result.PreservedPublicImages += preserved
	}
	summary := s.StorageGovernance()
	result.RemainingBytes = summary.TotalBytes
	result.OverLimitBytes = summary.OverLimitBytes
	return result, nil
}

func (r *ImageStorageCleanupResult) addRemovalStats(stats imageStorageRemovalStats) {
	r.DeletedBytes += stats.bytes
	r.DeletedImages += stats.images
	r.DeletedThumbnails += stats.thumbnails
	r.DeletedMetadataFiles += stats.metadataFiles
	r.DeletedReferenceFiles += stats.referenceFiles
}

func (s *ImageService) ListImages(baseURL, startDate, endDate string, scope ImageAccessScope) map[string]any {
	_, _ = s.CleanupStorage(ImageStorageCleanupOptions{
		RetentionDays: s.config.ImageRetentionDays(),
		MaxBytes:      s.config.ImageStorageLimitBytes(),
	})
	root := s.config.ImagesDir()
	items := make([]map[string]any, 0)
	_ = filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return nil
		}
		rel = filepath.ToSlash(rel)
		info, err := d.Info()
		if err != nil {
			return nil
		}
		parts := strings.Split(rel, "/")
		day := info.ModTime().Format("2006-01-02")
		if len(parts) >= 4 {
			day = strings.Join(parts[:3], "-")
		}
		if startDate != "" && day < startDate {
			return nil
		}
		if endDate != "" && day > endDate {
			return nil
		}
		meta := s.imageMetadata(rel)
		ownerID := meta.OwnerID
		if scope.Public {
			if meta.Visibility != ImageVisibilityPublic {
				return nil
			}
		} else if !scope.All && (scope.OwnerID == "" || ownerID != scope.OwnerID) {
			return nil
		}
		thumb := s.thumbnailInfo(rel, info)
		storageLocation := "local"
		if s.cloudStorageRef != nil {
			if record, _ := s.cloudStorageRef.GetRecord(context.Background(), rel); record != nil && record.StorageLocation == "cloud" {
				storageLocation = "cloud"
			}
		}
		item := map[string]any{
			"name":             filepath.Base(path),
			"path":             rel,
			"date":             day,
			"size":             info.Size(),
			"url":              publicAssetURL(baseURL, "images", rel),
			"created_at":       info.ModTime().Format("2006-01-02 15:04:05"),
			"visibility":       meta.Visibility,
			"storage_location": storageLocation,
		}
		addImageMetadataFields(item, meta, imageMetadataFieldOptions{
			BaseURL:                baseURL,
			IncludeReusableFields:  !scope.Public || meta.SharePromptParams,
			IncludeReferenceImages: !scope.Public || meta.ShareReferences,
		})
		if thumbRel, ok := thumb["thumbnail_rel"].(string); ok && thumbRel != "" {
			item["thumbnail_url"] = thumbnailURL(baseURL, thumbRel, info.ModTime())
		} else {
			item["thumbnail_url"] = ""
		}
		if !setImageItemDimensions(item, thumb["width"], thumb["height"]) {
			if width, height, ok := imageFileDimensions(path); ok {
				setImageItemDimensions(item, width, height)
			}
		}
		items = append(items, item)
		return nil
	})
	sort.Slice(items, func(i, j int) bool {
		left := toString(items[i]["created_at"])
		right := toString(items[j]["created_at"])
		if scope.Public {
			left = firstNonEmptyString(toString(items[i]["published_at"]), left)
			right = firstNonEmptyString(toString(items[j]["published_at"]), right)
		}
		return strings.Compare(left, right) > 0
	})
	groupMap := map[string][]map[string]any{}
	var order []string
	for _, item := range items {
		day := toString(item["date"])
		if _, ok := groupMap[day]; !ok {
			order = append(order, day)
		}
		groupMap[day] = append(groupMap[day], item)
	}
	groups := make([]map[string]any, 0, len(order))
	for _, day := range order {
		groups = append(groups, map[string]any{"date": day, "items": groupMap[day]})
	}
	return map[string]any{"items": items, "groups": groups}
}

func (s *ImageService) UpdateImageVisibility(value, visibility string, scope ImageAccessScope, optionValues ...ImageVisibilityUpdateOptions) (map[string]any, error) {
	visibility, err := NormalizeImageVisibility(visibility)
	if err != nil {
		return nil, err
	}
	options := ImageVisibilityUpdateOptions{}
	if len(optionValues) > 0 {
		options = optionValues[0]
	}
	if visibility != ImageVisibilityPublic {
		options = ImageVisibilityUpdateOptions{}
	}
	rel, err := imageRelativePathFromValue(value)
	if err != nil {
		return nil, err
	}
	imageRoot, err := filepath.Abs(s.config.ImagesDir())
	if err != nil {
		return nil, err
	}
	ref, err := s.imageFileRef(imageRoot, rel)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, errors.New("image not found")
		}
		return nil, err
	}
	meta := s.imageMetadata(ref.rel)
	if !scope.All && (scope.OwnerID == "" || meta.OwnerID != scope.OwnerID) {
		return nil, errors.New("image not found")
	}
	if err := s.writeImageMetadataForRef(ref, "", "", visibility, GeneratedImageMetadata{
		SharePromptParams: options.SharePromptParams,
		ShareReferences:   options.ShareReferences,
	}); err != nil {
		return nil, err
	}
	nextMeta := s.imageMetadata(ref.rel)
	item := map[string]any{
		"name":       filepath.Base(ref.path),
		"path":       ref.rel,
		"date":       imageDay(ref.rel, ref.info.ModTime()),
		"size":       ref.info.Size(),
		"visibility": nextMeta.Visibility,
		"created_at": ref.info.ModTime().Format("2006-01-02 15:04:05"),
	}
	addImageMetadataFields(item, nextMeta)
	if width, height, ok := imageFileDimensions(ref.path); ok {
		setImageItemDimensions(item, width, height)
	}
	return item, nil
}

func (s *ImageService) ImageFileAccess(value string, scope ImageAccessScope) (ImageFileAccess, error) {
	rel, err := imageRelativePathFromValue(value)
	if err != nil {
		return ImageFileAccess{}, err
	}
	imageRoot, err := filepath.Abs(s.config.ImagesDir())
	if err != nil {
		return ImageFileAccess{}, err
	}
	ref, err := s.imageFileRef(imageRoot, rel)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return ImageFileAccess{}, errors.New("image not found")
		}
		return ImageFileAccess{}, err
	}
	meta := s.imageMetadata(ref.rel)
	if !imageMetadataAllowsAccess(meta, scope) {
		return ImageFileAccess{}, errors.New("image not found")
	}
	return ImageFileAccess{
		Rel:        ref.rel,
		Path:       ref.path,
		Info:       ref.info,
		Visibility: meta.Visibility,
		OwnerID:    meta.OwnerID,
	}, nil
}

func (s *ImageService) ImageReferenceFileAccess(value string) (ImageReferenceFileAccess, error) {
	rel, err := imageReferenceRelativePathFromValue(value)
	if err != nil {
		return ImageReferenceFileAccess{}, err
	}
	sourceRel, err := sourceImageRelativePathFromReference(rel)
	if err != nil {
		return ImageReferenceFileAccess{}, err
	}
	meta := s.imageMetadata(sourceRel)
	var metadata imageReferenceMetadata
	for _, ref := range meta.ReferenceImages {
		if ref.Path == rel {
			metadata = ref
			break
		}
	}
	if metadata.Path == "" {
		return ImageReferenceFileAccess{}, errors.New("image not found")
	}
	root, err := filepath.Abs(s.imageReferencesDir())
	if err != nil {
		return ImageReferenceFileAccess{}, err
	}
	refPath := filepath.Join(root, filepath.FromSlash(rel))
	if !pathInsideRoot(root, refPath) {
		return ImageReferenceFileAccess{}, errors.New("invalid image path")
	}
	info, err := os.Stat(refPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return ImageReferenceFileAccess{}, errors.New("image not found")
		}
		return ImageReferenceFileAccess{}, err
	}
	if info.IsDir() {
		return ImageReferenceFileAccess{}, errors.New("image not found")
	}
	return ImageReferenceFileAccess{
		Rel:         rel,
		SourceRel:   sourceRel,
		Path:        refPath,
		ContentType: metadata.ContentType,
		Visibility:  meta.Visibility,
		OwnerID:     meta.OwnerID,
		Shared:      meta.ShareReferences,
	}, nil
}

func (s *ImageService) DeleteImages(paths []string, scope ImageAccessScope) (map[string]any, error) {
	if len(paths) == 0 {
		return nil, errors.New("paths is required")
	}
	imageRoot, err := filepath.Abs(s.config.ImagesDir())
	if err != nil {
		return nil, err
	}

	seen := make(map[string]struct{}, len(paths))
	deleted := 0
	missing := 0
	removedPaths := make([]string, 0, len(paths))
	for _, value := range paths {
		rel, err := cleanImageRelativePath(value)
		if err != nil {
			return nil, err
		}
		if _, ok := seen[rel]; ok {
			continue
		}
		seen[rel] = struct{}{}

		if _, err := s.imageFileRef(imageRoot, rel); err != nil {
			if errors.Is(err, os.ErrNotExist) {
				missing++
				continue
			}
			return nil, err
		}
		if !pathInsideRoot(imageRoot, filepath.Join(imageRoot, filepath.FromSlash(rel))) {
			return nil, errors.New("invalid image path")
		}
		if !scope.All && (scope.OwnerID == "" || s.imageOwner(rel) != scope.OwnerID) {
			missing++
			continue
		}
		stats, err := s.removeImageGroup(rel)
		if err != nil {
			return nil, err
		}
		if stats.images == 0 {
			missing++
		} else {
			deleted++
		}
		removedPaths = append(removedPaths, rel)
	}
	return map[string]any{"deleted": deleted, "missing": missing, "paths": removedPaths}, nil
}

func (s *ImageService) RecordImageOwners(values []string, ownerID string) {
	ownerID = strings.TrimSpace(ownerID)
	if ownerID == "" {
		return
	}
	for _, ref := range s.imageFileRefs(values) {
		_ = s.writeImageMetadataForRef(ref, ownerID, "", "")
	}
}

func (s *ImageService) RecordGeneratedImages(values []string, ownerID, ownerName, visibility string, metadataValues ...GeneratedImageMetadata) {
	ownerID = strings.TrimSpace(ownerID)
	ownerName = strings.TrimSpace(ownerName)
	metadata := GeneratedImageMetadata{}
	if len(metadataValues) > 0 {
		metadata = metadataValues[0]
	}
	visibility, err := NormalizeImageVisibility(visibility)
	if err != nil {
		visibility = ImageVisibilityPrivate
	}
	for _, ref := range s.imageFileRefs(values) {
		s.ensureThumbnailForRef(ref)
		if ownerID != "" && ownerID != "anonymous" {
			_ = s.writeImageMetadataForRef(ref, ownerID, ownerName, visibility, metadata)
		}
	}
}

func (s *ImageService) EnsureThumbnails(values []string) {
	for _, ref := range s.imageFileRefs(values) {
		s.ensureThumbnailForRef(ref)
	}
}

func (s *ImageService) SourceImageRelativePathFromThumbnail(thumbnailRel string) (string, error) {
	return sourceImageRelativePathFromThumbnail(thumbnailRel)
}

func (s *ImageService) EnsureThumbnail(thumbnailRel string) error {
	sourceRel, err := s.SourceImageRelativePathFromThumbnail(thumbnailRel)
	if err != nil {
		return err
	}
	imageRoot, err := filepath.Abs(s.config.ImagesDir())
	if err != nil {
		return err
	}
	ref, err := s.imageFileRef(imageRoot, sourceRel)
	if err != nil {
		return err
	}
	thumb := s.ensureThumbnailForRef(ref)
	if toString(thumb["thumbnail_rel"]) == "" {
		return errors.New("thumbnail unavailable")
	}
	return nil
}

func (s *ImageService) thumbnailInfo(rel string, sourceInfo os.FileInfo) map[string]any {
	_, result, _ := s.thumbnailCacheInfo(rel, sourceInfo.ModTime())
	return result
}

func (s *ImageService) ensureThumbnailForRef(ref imageFileRef) map[string]any {
	if _, result, ok := s.thumbnailCacheInfo(ref.rel, ref.info.ModTime()); ok {
		return result
	}
	return s.withThumbnailJob(ref.rel, func() map[string]any {
		if _, result, ok := s.thumbnailCacheInfo(ref.rel, ref.info.ModTime()); ok {
			return result
		}
		return s.generateThumbnail(ref)
	})
}

func (s *ImageService) withThumbnailJob(rel string, run func() map[string]any) map[string]any {
	s.thumbnailMu.Lock()
	if s.thumbnailJobs == nil {
		s.thumbnailJobs = make(map[string]*thumbnailJob)
	}
	if job, ok := s.thumbnailJobs[rel]; ok {
		done := job.done
		s.thumbnailMu.Unlock()
		<-done
		return job.result
	}
	job := &thumbnailJob{done: make(chan struct{})}
	s.thumbnailJobs[rel] = job
	s.thumbnailMu.Unlock()

	job.result = run()

	s.thumbnailMu.Lock()
	delete(s.thumbnailJobs, rel)
	close(job.done)
	s.thumbnailMu.Unlock()
	return job.result
}

func (s *ImageService) thumbnailCacheInfo(rel string, sourceModTime time.Time) (string, map[string]any, bool) {
	thumbPath := s.thumbnailPath(rel)
	thumbRel := thumbnailRelativePath(s.config.ImageThumbnailsDir(), thumbPath)
	result := map[string]any{"thumbnail_rel": thumbRel}
	thumbInfo, err := os.Stat(thumbPath)
	if err != nil || thumbInfo.ModTime().Before(sourceModTime) {
		return thumbPath, result, false
	}
	meta := s.readThumbnailMetadata(rel, thumbPath+".json", sourceModTime)
	if !isCurrentThumbnailMetadata(meta) {
		return thumbPath, result, false
	}
	for key, value := range meta {
		result[key] = value
	}
	return thumbPath, result, true
}

func (s *ImageService) generateThumbnail(ref imageFileRef) map[string]any {
	thumbPath, result, _ := s.thumbnailCacheInfo(ref.rel, ref.info.ModTime())
	file, err := os.Open(ref.path)
	if err != nil {
		return map[string]any{}
	}
	defer file.Close()
	img, _, err := image.Decode(file)
	if err != nil {
		return map[string]any{}
	}
	bounds := img.Bounds()
	width, height := bounds.Dx(), bounds.Dy()
	thumb := resizeToFit(flattenImage(img), ThumbnailSize, ThumbnailSize)
	if err := writeJPEGThumbnail(thumbPath, thumb); err != nil {
		return map[string]any{}
	}
	_ = s.writeThumbnailMetadata(ref.rel, thumbPath+".json", map[string]any{
		"width":             width,
		"height":            height,
		"thumbnail_format":  "jpeg",
		"thumbnail_quality": thumbnailQuality,
		"thumbnail_size":    ThumbnailSize,
		"thumbnail_version": thumbnailCacheVersion,
	})
	result["width"] = width
	result["height"] = height
	return result
}

func (s *ImageService) imageFileRefs(values []string) []imageFileRef {
	if len(values) == 0 {
		return nil
	}
	imageRoot, err := filepath.Abs(s.config.ImagesDir())
	if err != nil {
		return nil
	}
	seen := make(map[string]struct{}, len(values))
	refs := make([]imageFileRef, 0, len(values))
	for _, value := range values {
		rel, err := imageRelativePathFromValue(value)
		if err != nil {
			continue
		}
		if _, ok := seen[rel]; ok {
			continue
		}
		seen[rel] = struct{}{}
		ref, err := s.imageFileRef(imageRoot, rel)
		if err != nil {
			continue
		}
		refs = append(refs, ref)
	}
	return refs
}

func (s *ImageService) imageFileRef(imageRoot, rel string) (imageFileRef, error) {
	rel, err := cleanImageRelativePath(rel)
	if err != nil {
		return imageFileRef{}, err
	}
	imagePath := filepath.Join(imageRoot, filepath.FromSlash(rel))
	if !pathInsideRoot(imageRoot, imagePath) {
		return imageFileRef{}, errors.New("invalid image path")
	}
	info, err := os.Stat(imagePath)
	if err != nil {
		return imageFileRef{}, err
	}
	if info.IsDir() {
		return imageFileRef{}, errors.New("image path is not a file")
	}
	return imageFileRef{rel: rel, path: imagePath, info: info}, nil
}

func (s *ImageService) thumbnailPath(rel string) string {
	return filepath.Join(s.config.ImageThumbnailsDir(), filepath.FromSlash(rel)+thumbnailExtension)
}

func (s *ImageService) imageOwner(rel string) string {
	return s.imageMetadata(rel).OwnerID
}

func imageMetadataAllowsAccess(meta imageMetadata, scope ImageAccessScope) bool {
	if meta.Visibility == ImageVisibilityPublic {
		return true
	}
	if scope.All {
		return true
	}
	return scope.OwnerID != "" && meta.OwnerID == scope.OwnerID
}

func (s *ImageService) imageMetadata(rel string) imageMetadata {
	metaPath, err := s.imageOwnerMetadataPath(rel)
	if err != nil {
		return imageMetadata{Visibility: ImageVisibilityPrivate}
	}
	var raw map[string]any
	if s.store != nil {
		value, err := s.store.LoadJSONDocument(imageOwnerDocumentName(rel))
		if err == nil {
			if meta, ok := value.(map[string]any); ok {
				raw = meta
			}
		}
	}
	if raw == nil {
		data, err := os.ReadFile(metaPath)
		if err != nil {
			return imageMetadata{Visibility: ImageVisibilityPrivate}
		}
		if json.Unmarshal(data, &raw) != nil {
			return imageMetadata{Visibility: ImageVisibilityPrivate}
		}
	}
	return normalizeImageMetadata(raw)
}

func normalizeImageMetadata(raw map[string]any) imageMetadata {
	visibility := strings.TrimSpace(toString(raw["visibility"]))
	if visibility != ImageVisibilityPublic {
		visibility = ImageVisibilityPrivate
	}
	return imageMetadata{
		OwnerID:           strings.TrimSpace(toString(raw["owner_id"])),
		OwnerName:         strings.TrimSpace(toString(raw["owner_name"])),
		Visibility:        visibility,
		PublishedAt:       strings.TrimSpace(toString(raw["published_at"])),
		Prompt:            strings.TrimSpace(toString(raw["prompt"])),
		Model:             strings.TrimSpace(toString(raw["model"])),
		Quality:           strings.TrimSpace(toString(raw["quality"])),
		ResolutionPreset:  NormalizeImageResolutionPreset(toString(raw["resolution_preset"])),
		RequestedSize:     strings.TrimSpace(toString(raw["requested_size"])),
		OutputFormat:      NormalizeImageOutputFormat(strings.TrimSpace(toString(raw["output_format"]))),
		OutputCompression: imageOutputCompressionMetadata(raw["output_compression"]),
		Background:        strings.TrimSpace(toString(raw["background"])),
		Moderation:        strings.TrimSpace(toString(raw["moderation"])),
		Style:             strings.TrimSpace(toString(raw["style"])),
		PartialImages:     positiveImageMetadataInt(raw["partial_images"]),
		InputImageMask:    strings.TrimSpace(toString(raw["input_image_mask"])),
		ReferenceImages:   normalizeImageReferenceMetadata(raw["reference_images"]),
		SharePromptParams: boolMetadataValue(raw["share_prompt_parameters"]),
		ShareReferences:   boolMetadataValue(raw["share_reference_images"]),
	}
}

func (s *ImageService) writeImageMetadataForRef(ref imageFileRef, ownerID, ownerName, visibility string, metadataValues ...GeneratedImageMetadata) error {
	meta := s.imageMetadata(ref.rel)
	if ownerID = strings.TrimSpace(ownerID); ownerID != "" {
		meta.OwnerID = ownerID
	}
	if ownerName = strings.TrimSpace(ownerName); ownerName != "" {
		meta.OwnerName = ownerName
	}
	if visibility = strings.TrimSpace(visibility); visibility != "" {
		normalized, err := NormalizeImageVisibility(visibility)
		if err != nil {
			return err
		}
		if normalized == ImageVisibilityPublic {
			if meta.PublishedAt == "" || meta.Visibility != ImageVisibilityPublic {
				meta.PublishedAt = time.Now().UTC().Format(time.RFC3339Nano)
			}
		} else {
			meta.PublishedAt = ""
		}
		meta.Visibility = normalized
	}
	if len(metadataValues) > 0 {
		metadata := metadataValues[0]
		if prompt := strings.TrimSpace(metadata.Prompt); prompt != "" {
			meta.Prompt = prompt
		}
		if model := strings.TrimSpace(metadata.Model); model != "" {
			meta.Model = model
		}
		if quality := strings.TrimSpace(metadata.Quality); quality != "" {
			meta.Quality = quality
		}
		if preset := NormalizeImageResolutionPreset(metadata.ResolutionPreset); preset != "" {
			meta.ResolutionPreset = preset
		}
		if requestedSize := strings.TrimSpace(metadata.RequestedSize); requestedSize != "" {
			meta.RequestedSize = requestedSize
		}
		if outputFormat := NormalizeImageOutputFormat(metadata.OutputFormat); outputFormat != "" {
			meta.OutputFormat = outputFormat
		}
		if metadata.OutputCompression != nil {
			compression := *metadata.OutputCompression
			if compression < 0 {
				compression = 0
			} else if compression > 100 {
				compression = 100
			}
			meta.OutputCompression = &compression
		}
		if background := strings.TrimSpace(metadata.Background); background != "" {
			meta.Background = background
		}
		if moderation := strings.TrimSpace(metadata.Moderation); moderation != "" {
			meta.Moderation = moderation
		}
		if style := strings.TrimSpace(metadata.Style); style != "" {
			meta.Style = style
		}
		if metadata.PartialImages != nil && *metadata.PartialImages > 0 {
			partialImages := *metadata.PartialImages
			meta.PartialImages = &partialImages
		}
		if inputImageMask := strings.TrimSpace(metadata.InputImageMask); inputImageMask != "" {
			meta.InputImageMask = inputImageMask
		}
		if len(metadata.ReferenceImages) > 0 {
			meta.ReferenceImages = s.writeImageReferencesForRef(ref, metadata.ReferenceImages)
		}
		meta.SharePromptParams = metadata.SharePromptParams
		meta.ShareReferences = metadata.ShareReferences
	}
	if meta.Visibility == "" {
		meta.Visibility = ImageVisibilityPrivate
	}
	return s.writeImageMetadata(ref.rel, meta)
}

func (s *ImageService) writeImageMetadata(rel string, meta imageMetadata) error {
	metaPath, err := s.imageOwnerMetadataPath(rel)
	if err != nil {
		return err
	}
	value := map[string]any{
		"visibility": meta.Visibility,
		"updated_at": time.Now().UTC().Format(time.RFC3339Nano),
	}
	if meta.OwnerID != "" {
		value["owner_id"] = meta.OwnerID
	}
	if meta.OwnerName != "" {
		value["owner_name"] = meta.OwnerName
	}
	if meta.PublishedAt != "" {
		value["published_at"] = meta.PublishedAt
	}
	if meta.Prompt != "" {
		value["prompt"] = meta.Prompt
	}
	if meta.Model != "" {
		value["model"] = meta.Model
	}
	if meta.Quality != "" {
		value["quality"] = meta.Quality
	}
	if meta.ResolutionPreset != "" {
		value["resolution_preset"] = meta.ResolutionPreset
	}
	if meta.RequestedSize != "" {
		value["requested_size"] = meta.RequestedSize
	}
	if meta.OutputFormat != "" {
		value["output_format"] = meta.OutputFormat
	}
	if meta.OutputCompression != nil {
		value["output_compression"] = *meta.OutputCompression
	}
	if meta.Background != "" {
		value["background"] = meta.Background
	}
	if meta.Moderation != "" {
		value["moderation"] = meta.Moderation
	}
	if meta.Style != "" {
		value["style"] = meta.Style
	}
	if meta.PartialImages != nil {
		value["partial_images"] = *meta.PartialImages
	}
	if meta.InputImageMask != "" {
		value["input_image_mask"] = meta.InputImageMask
	}
	if meta.SharePromptParams {
		value["share_prompt_parameters"] = true
	}
	if meta.ShareReferences {
		value["share_reference_images"] = true
	}
	if len(meta.ReferenceImages) > 0 {
		refs := make([]map[string]any, 0, len(meta.ReferenceImages))
		for _, ref := range meta.ReferenceImages {
			if ref.Path == "" {
				continue
			}
			item := map[string]any{"path": ref.Path}
			if ref.Filename != "" {
				item["filename"] = ref.Filename
			}
			if ref.ContentType != "" {
				item["content_type"] = ref.ContentType
			}
			if ref.Size > 0 {
				item["size"] = ref.Size
			}
			refs = append(refs, item)
		}
		if len(refs) > 0 {
			value["reference_images"] = refs
		}
	}
	if s.store != nil {
		return s.store.SaveJSONDocument(imageOwnerDocumentName(rel), value)
	}
	if err := os.MkdirAll(filepath.Dir(metaPath), 0o755); err != nil {
		return err
	}
	return writeJSONFile(metaPath, value)
}

func (s *ImageService) removeImageOwner(rel string) error {
	metaPath, err := s.imageOwnerMetadataPath(rel)
	if err != nil {
		return err
	}
	if s.store != nil {
		return s.store.DeleteJSONDocument(imageOwnerDocumentName(rel))
	}
	removeErr := os.Remove(metaPath)
	if removeErr != nil && !errors.Is(removeErr, os.ErrNotExist) {
		return removeErr
	}
	removeEmptyParentDirs(s.config.ImageMetadataDir(), filepath.Dir(metaPath))
	return nil
}

func (s *ImageService) imageReferencesDir() string {
	return filepath.Join(s.config.ImageMetadataDir(), imageReferencePrefix)
}

func (s *ImageService) writeImageReferencesForRef(ref imageFileRef, refs []GeneratedImageReference) []imageReferenceMetadata {
	if len(refs) == 0 {
		return nil
	}
	if err := s.removeImageReferences(ref.rel); err != nil {
		return nil
	}
	root, err := filepath.Abs(s.imageReferencesDir())
	if err != nil {
		return nil
	}
	dir := filepath.Join(root, filepath.FromSlash(ref.rel+".refs"))
	if !pathInsideRoot(root, dir) {
		return nil
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil
	}
	result := make([]imageReferenceMetadata, 0, len(refs))
	for index, source := range refs {
		if len(source.Data) == 0 {
			continue
		}
		filename := safeImageReferenceFilename(source.Filename, index)
		rel := filepath.ToSlash(filepath.Join(ref.rel+".refs", strconv.Itoa(index+1)+"-"+filename))
		if _, err := cleanImageReferenceRelativePath(rel); err != nil {
			continue
		}
		path := filepath.Join(root, filepath.FromSlash(rel))
		if !pathInsideRoot(root, path) {
			continue
		}
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			continue
		}
		if err := os.WriteFile(path, source.Data, 0o644); err != nil {
			continue
		}
		result = append(result, imageReferenceMetadata{
			Path:        rel,
			Filename:    strings.TrimSpace(source.Filename),
			ContentType: strings.TrimSpace(source.ContentType),
			Size:        int64(len(source.Data)),
		})
	}
	if len(result) == 0 {
		_ = os.Remove(dir)
		removeEmptyParentDirs(root, filepath.Dir(dir))
	}
	return result
}

func (s *ImageService) removeImageReferences(sourceRel string) error {
	sourceRel, err := cleanImageRelativePath(sourceRel)
	if err != nil {
		return err
	}
	root, err := filepath.Abs(s.imageReferencesDir())
	if err != nil {
		return err
	}
	dir := filepath.Join(root, filepath.FromSlash(sourceRel+".refs"))
	if !pathInsideRoot(root, dir) {
		return errors.New("invalid image path")
	}
	removeErr := os.RemoveAll(dir)
	if removeErr != nil && !errors.Is(removeErr, os.ErrNotExist) {
		return removeErr
	}
	removeEmptyParentDirs(root, filepath.Dir(dir))
	return nil
}

func (s *ImageService) imageCleanupCandidates() []imageCleanupCandidate {
	root, err := filepath.Abs(s.config.ImagesDir())
	if err != nil {
		return nil
	}
	candidates := make([]imageCleanupCandidate, 0)
	_ = filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		rel, relErr := filepath.Rel(root, path)
		if relErr != nil {
			return nil
		}
		rel = filepath.ToSlash(rel)
		info, statErr := d.Info()
		if statErr != nil {
			return nil
		}
		meta := s.imageMetadata(rel)
		candidates = append(candidates, imageCleanupCandidate{
			rel:       rel,
			path:      path,
			info:      info,
			meta:      meta,
			groupSize: s.imageGroupSize(rel, info.Size()),
		})
		return nil
	})
	return candidates
}

func (s *ImageService) cleanupByRetention(retentionDays int, includePublic bool) (imageStorageRemovalStats, int, error) {
	if retentionDays < 1 {
		retentionDays = 1
	}
	cutoff := time.Now().Add(-time.Duration(retentionDays) * 24 * time.Hour)
	var total imageStorageRemovalStats
	preservedPublic := 0
	for _, candidate := range s.imageCleanupCandidates() {
		if !candidate.info.ModTime().Before(cutoff) {
			continue
		}
		if candidate.meta.Visibility == ImageVisibilityPublic && !includePublic {
			preservedPublic++
			continue
		}
		stats, err := s.removeImageGroup(candidate.rel)
		if err != nil {
			return total, preservedPublic, err
		}
		total.add(stats)
	}
	return total, preservedPublic, nil
}

func (s *ImageService) cleanupByStorageLimit(maxBytes int64, includePublic bool) (imageStorageRemovalStats, int, error) {
	if maxBytes <= 0 {
		return imageStorageRemovalStats{}, 0, nil
	}
	summary := s.StorageGovernance()
	if summary.TotalBytes <= maxBytes {
		return imageStorageRemovalStats{}, 0, nil
	}
	candidates := s.imageCleanupCandidates()
	sort.Slice(candidates, func(i, j int) bool {
		leftPublic := candidates[i].meta.Visibility == ImageVisibilityPublic
		rightPublic := candidates[j].meta.Visibility == ImageVisibilityPublic
		if leftPublic != rightPublic {
			return !leftPublic
		}
		return candidates[i].info.ModTime().Before(candidates[j].info.ModTime())
	})
	current := summary.TotalBytes
	var total imageStorageRemovalStats
	preservedPublic := 0
	for _, candidate := range candidates {
		if current <= maxBytes {
			break
		}
		if candidate.meta.Visibility == ImageVisibilityPublic && !includePublic {
			preservedPublic++
			continue
		}
		stats, err := s.removeImageGroup(candidate.rel)
		if err != nil {
			return total, preservedPublic, err
		}
		total.add(stats)
		if stats.bytes > 0 {
			current -= stats.bytes
		} else {
			current -= candidate.groupSize
		}
	}
	return total, preservedPublic, nil
}

func (s *ImageService) removeImageGroup(rel string) (imageStorageRemovalStats, error) {
	rel, err := cleanImageRelativePath(rel)
	if err != nil {
		return imageStorageRemovalStats{}, err
	}
	var stats imageStorageRemovalStats
	thumbnailRoot, err := filepath.Abs(s.config.ImageThumbnailsDir())
	if err != nil {
		return stats, err
	}
	if removed, bytes, err := s.removeImageThumbnailWithStats(thumbnailRoot, rel); err != nil {
		return stats, err
	} else if removed > 0 {
		stats.thumbnails++
		if removed > 1 {
			stats.metadataFiles += removed - 1
		}
		stats.bytes += bytes
	}
	if removed, bytes, err := s.removeImageReferencesWithStats(rel); err != nil {
		return stats, err
	} else {
		stats.referenceFiles += removed
		stats.bytes += bytes
	}
	if removed, bytes, err := s.removeImageOwnerWithStats(rel); err != nil {
		return stats, err
	} else {
		stats.metadataFiles += removed
		stats.bytes += bytes
	}
	imageRoot, err := filepath.Abs(s.config.ImagesDir())
	if err != nil {
		return stats, err
	}
	imagePath := filepath.Join(imageRoot, filepath.FromSlash(rel))
	if !pathInsideRoot(imageRoot, imagePath) {
		return stats, errors.New("invalid image path")
	}
	if removed, bytes, err := removeFileWithStats(imagePath); err != nil {
		return stats, err
	} else if removed {
		stats.images++
		stats.bytes += bytes
	}
	removeEmptyParentDirs(imageRoot, filepath.Dir(imagePath))
	return stats, nil
}

func (s *ImageService) removeImageOwnerWithStats(rel string) (int, int64, error) {
	if s.store != nil {
		if err := s.store.DeleteJSONDocument(imageOwnerDocumentName(rel)); err != nil {
			return 0, 0, err
		}
		return 1, 0, nil
	}
	metaPath, err := s.imageOwnerMetadataPath(rel)
	if err != nil {
		return 0, 0, err
	}
	removed, bytes, err := removeFileWithStats(metaPath)
	if err != nil {
		return 0, 0, err
	}
	if removed {
		removeEmptyParentDirs(s.config.ImageMetadataDir(), filepath.Dir(metaPath))
		return 1, bytes, nil
	}
	return 0, 0, nil
}

func (s *ImageService) removeImageReferencesWithStats(sourceRel string) (int, int64, error) {
	sourceRel, err := cleanImageRelativePath(sourceRel)
	if err != nil {
		return 0, 0, err
	}
	root, err := filepath.Abs(s.imageReferencesDir())
	if err != nil {
		return 0, 0, err
	}
	dir := filepath.Join(root, filepath.FromSlash(sourceRel+".refs"))
	if !pathInsideRoot(root, dir) {
		return 0, 0, errors.New("invalid image path")
	}
	bytes, files := directorySize(dir, "")
	removeErr := os.RemoveAll(dir)
	if removeErr != nil && !errors.Is(removeErr, os.ErrNotExist) {
		return 0, 0, removeErr
	}
	removeEmptyParentDirs(root, filepath.Dir(dir))
	return files, bytes, nil
}

func (s *ImageService) removeImageThumbnailWithStats(root, rel string) (int, int64, error) {
	thumbPath := filepath.Join(root, filepath.FromSlash(rel)+thumbnailExtension)
	if !pathInsideRoot(root, thumbPath) {
		return 0, 0, errors.New("invalid image path")
	}
	removed := 0
	var bytes int64
	if didRemove, size, err := removeFileWithStats(thumbPath); err != nil {
		return 0, 0, err
	} else if didRemove {
		removed++
		bytes += size
	}
	if didRemove, size, err := removeFileWithStats(thumbPath + ".json"); err != nil {
		return 0, 0, err
	} else if didRemove {
		removed++
		bytes += size
	}
	if s.store != nil {
		if err := s.store.DeleteJSONDocument(thumbnailMetadataDocumentName(rel)); err != nil {
			return 0, 0, err
		}
	}
	removeEmptyParentDirs(root, filepath.Dir(thumbPath))
	return removed, bytes, nil
}

func (s *ImageService) clearThumbnailCache() (imageStorageRemovalStats, error) {
	root := s.config.ImageThumbnailsDir()
	bytes, thumbnails, metadataFiles := thumbnailCacheStats(root)
	if err := os.RemoveAll(root); err != nil && !errors.Is(err, os.ErrNotExist) {
		return imageStorageRemovalStats{}, err
	}
	if err := os.MkdirAll(root, 0o755); err != nil {
		return imageStorageRemovalStats{}, err
	}
	return imageStorageRemovalStats{bytes: bytes, thumbnails: thumbnails, metadataFiles: metadataFiles}, nil
}

func (s *ImageService) imageGroupSize(rel string, imageSize int64) int64 {
	total := imageSize
	thumbPath := s.thumbnailPath(rel)
	for _, path := range []string{thumbPath, thumbPath + ".json"} {
		if info, err := os.Stat(path); err == nil && !info.IsDir() {
			total += info.Size()
		}
	}
	metaPath, err := s.imageOwnerMetadataPath(rel)
	if err == nil {
		if info, statErr := os.Stat(metaPath); statErr == nil && !info.IsDir() {
			total += info.Size()
		}
	}
	refDir := filepath.Join(s.imageReferencesDir(), filepath.FromSlash(rel+".refs"))
	refBytes, _ := directorySize(refDir, "")
	total += refBytes
	return total
}

func (s *ImageService) imageOwnerMetadataPath(rel string) (string, error) {
	rel, err := cleanImageRelativePath(rel)
	if err != nil {
		return "", err
	}
	root, err := filepath.Abs(s.config.ImageMetadataDir())
	if err != nil {
		return "", err
	}
	metaPath := filepath.Join(root, filepath.FromSlash(rel)+".json")
	if !pathInsideRoot(root, metaPath) {
		return "", errors.New("invalid image path")
	}
	return metaPath, nil
}

func (s *ImageService) readThumbnailMetadata(rel, metaPath string, sourceMtime time.Time) map[string]any {
	if s.store != nil {
		raw, err := s.store.LoadJSONDocument(thumbnailMetadataDocumentName(rel))
		if err == nil {
			if meta, ok := raw.(map[string]any); ok && meta["width"] != nil && meta["height"] != nil {
				return meta
			}
		}
	}
	return readImageMetadata(metaPath, sourceMtime)
}

func (s *ImageService) writeThumbnailMetadata(rel, metaPath string, value map[string]any) error {
	if s.store != nil {
		return s.store.SaveJSONDocument(thumbnailMetadataDocumentName(rel), value)
	}
	return writeJSONFile(metaPath, value)
}

func (s *ImageService) removeImageThumbnail(root, rel string) error {
	if s.store != nil {
		if err := s.store.DeleteJSONDocument(thumbnailMetadataDocumentName(rel)); err != nil {
			return err
		}
	}
	return removeImageThumbnail(root, rel)
}

func imageOwnerDocumentName(rel string) string {
	return "image_metadata/" + filepath.ToSlash(rel) + ".json"
}

type imageMetadataFieldOptions struct {
	BaseURL                string
	IncludeReusableFields  bool
	IncludeReferenceImages bool
}

func addImageMetadataFields(item map[string]any, meta imageMetadata, optionsValues ...imageMetadataFieldOptions) {
	options := imageMetadataFieldOptions{IncludeReusableFields: true, IncludeReferenceImages: true}
	if len(optionsValues) > 0 {
		options = optionsValues[0]
	}
	if meta.OwnerID != "" {
		item["owner_id"] = meta.OwnerID
	}
	if meta.OwnerName != "" {
		item["owner_name"] = meta.OwnerName
	}
	if meta.PublishedAt != "" {
		item["published_at"] = meta.PublishedAt
	}
	item["share_prompt_parameters"] = meta.SharePromptParams
	item["share_reference_images"] = meta.ShareReferences
	if options.IncludeReusableFields {
		if meta.Prompt != "" {
			item["prompt"] = meta.Prompt
		}
		if meta.Model != "" {
			item["model"] = meta.Model
		}
		if meta.Quality != "" {
			item["quality"] = meta.Quality
		}
		if meta.ResolutionPreset != "" {
			item["resolution_preset"] = meta.ResolutionPreset
		}
		if meta.RequestedSize != "" {
			item["requested_size"] = meta.RequestedSize
		}
		if meta.OutputFormat != "" {
			item["output_format"] = meta.OutputFormat
		}
		if meta.OutputCompression != nil {
			item["output_compression"] = *meta.OutputCompression
		}
		if meta.Background != "" {
			item["background"] = meta.Background
		}
		if meta.Moderation != "" {
			item["moderation"] = meta.Moderation
		}
		if meta.Style != "" {
			item["style"] = meta.Style
		}
		if meta.PartialImages != nil {
			item["partial_images"] = *meta.PartialImages
		}
		if meta.InputImageMask != "" {
			item["input_image_mask"] = meta.InputImageMask
		}
	}
	if options.IncludeReferenceImages && len(meta.ReferenceImages) > 0 {
		baseURL := strings.TrimSpace(options.BaseURL)
		referenceItems := make([]map[string]any, 0, len(meta.ReferenceImages))
		referenceURLs := make([]string, 0, len(meta.ReferenceImages))
		for _, ref := range meta.ReferenceImages {
			if ref.Path == "" {
				continue
			}
			refItem := map[string]any{"path": ref.Path}
			if ref.Filename != "" {
				refItem["filename"] = ref.Filename
			}
			if ref.ContentType != "" {
				refItem["content_type"] = ref.ContentType
			}
			if ref.Size > 0 {
				refItem["size"] = ref.Size
			}
			if baseURL != "" {
				url := publicAssetURL(baseURL, "image-references", ref.Path)
				refItem["url"] = url
				referenceURLs = append(referenceURLs, url)
			}
			referenceItems = append(referenceItems, refItem)
		}
		if len(referenceItems) > 0 {
			item["reference_images"] = referenceItems
		}
		if len(referenceURLs) > 0 {
			item["reference_image_urls"] = referenceURLs
		}
	}
}

func NormalizeImageVisibility(value string) (string, error) {
	switch strings.TrimSpace(value) {
	case "", ImageVisibilityPrivate:
		return ImageVisibilityPrivate, nil
	case ImageVisibilityPublic:
		return ImageVisibilityPublic, nil
	default:
		return "", errors.New("visibility must be private or public")
	}
}

func NormalizeImageResolutionPreset(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "1080p":
		return "1080p"
	case "2k":
		return "2k"
	case "4k":
		return "4k"
	default:
		return ""
	}
}

func imageDay(rel string, modTime time.Time) string {
	parts := strings.Split(rel, "/")
	if len(parts) >= 4 {
		return strings.Join(parts[:3], "-")
	}
	return modTime.Format("2006-01-02")
}

func thumbnailMetadataDocumentName(rel string) string {
	return "image_thumbnails/" + filepath.ToSlash(rel) + thumbnailExtension + ".json"
}

func sourceImageRelativePathFromThumbnail(value string) (string, error) {
	thumbnailRel, err := cleanImageRelativePath(value)
	if err != nil {
		return "", err
	}
	if !strings.HasSuffix(thumbnailRel, thumbnailExtension) {
		return "", errors.New("invalid thumbnail path")
	}
	return cleanImageRelativePath(strings.TrimSuffix(thumbnailRel, thumbnailExtension))
}

func setImageItemDimensions(item map[string]any, widthValue, heightValue any) bool {
	width, height, ok := imageDimensionsFromValues(widthValue, heightValue)
	if !ok {
		return false
	}
	item["width"] = width
	item["height"] = height
	item["resolution"] = strconv.Itoa(width) + "x" + strconv.Itoa(height)
	item["aspect_ratio"] = simplifiedAspectRatio(width, height)
	item["orientation"] = imageOrientation(width, height)
	item["megapixels"] = float64(width) * float64(height) / 1_000_000
	return true
}

func imageDimensionsFromValues(widthValue, heightValue any) (int, int, bool) {
	width := numericMetaValue(widthValue)
	height := numericMetaValue(heightValue)
	if width <= 0 || height <= 0 {
		return 0, 0, false
	}
	return width, height, true
}

func imageFileDimensions(path string) (int, int, bool) {
	file, err := os.Open(path)
	if err != nil {
		return 0, 0, false
	}
	defer file.Close()
	config, _, err := image.DecodeConfig(file)
	if err != nil || config.Width <= 0 || config.Height <= 0 {
		return 0, 0, false
	}
	return config.Width, config.Height, true
}

func simplifiedAspectRatio(width, height int) string {
	divisor := greatestCommonDivisor(width, height)
	if divisor <= 0 {
		return ""
	}
	return strconv.Itoa(width/divisor) + ":" + strconv.Itoa(height/divisor)
}

func imageOrientation(width, height int) string {
	if width == height {
		return "square"
	}
	if width > height {
		return "landscape"
	}
	return "portrait"
}

func greatestCommonDivisor(a, b int) int {
	if a < 0 {
		a = -a
	}
	if b < 0 {
		b = -b
	}
	for b != 0 {
		a, b = b, a%b
	}
	return a
}

func thumbnailRelativePath(root, thumbPath string) string {
	rel, err := filepath.Rel(root, thumbPath)
	if err != nil {
		return ""
	}
	return filepath.ToSlash(rel)
}

func publicAssetURL(baseURL, prefix, rel string) string {
	parts := strings.Split(filepath.ToSlash(rel), "/")
	for i, part := range parts {
		parts[i] = url.PathEscape(part)
	}
	return strings.TrimRight(baseURL, "/") + "/" + strings.Trim(prefix, "/") + "/" + strings.Join(parts, "/")
}

func thumbnailURL(baseURL, thumbRel string, sourceModTime time.Time) string {
	return publicAssetURL(baseURL, "image-thumbnails", thumbRel) +
		"?v=" + strconv.Itoa(thumbnailCacheVersion) + "-" + strconv.FormatInt(sourceModTime.UnixNano(), 10)
}

func cleanImageRelativePath(value string) (string, error) {
	rel := filepath.ToSlash(strings.TrimSpace(value))
	if rel == "" || strings.ContainsRune(rel, 0) || strings.HasPrefix(rel, "/") || filepath.IsAbs(filepath.FromSlash(rel)) {
		return "", errors.New("invalid image path")
	}
	if path.Clean(rel) != rel {
		return "", errors.New("invalid image path")
	}
	for _, part := range strings.Split(rel, "/") {
		if part == "" || part == "." || part == ".." || strings.Contains(part, ":") {
			return "", errors.New("invalid image path")
		}
	}
	return rel, nil
}

func imageRelativePathFromValue(value string) (string, error) {
	text := strings.TrimSpace(value)
	if text == "" {
		return "", errors.New("invalid image path")
	}
	if parsed, err := url.Parse(text); err == nil {
		pathValue := parsed.EscapedPath()
		if pathValue == "" {
			pathValue = parsed.Path
		}
		if parsed.Scheme != "" || strings.HasPrefix(pathValue, "/") {
			const imagePrefix = "/images/"
			index := strings.Index(pathValue, imagePrefix)
			if index < 0 {
				return "", errors.New("invalid image path")
			}
			rel, err := url.PathUnescape(pathValue[index+len(imagePrefix):])
			if err != nil {
				return "", errors.New("invalid image path")
			}
			return cleanImageRelativePath(rel)
		}
	}
	return cleanImageRelativePath(text)
}

func cleanImageReferenceRelativePath(value string) (string, error) {
	rel, err := cleanImageRelativePath(value)
	if err != nil {
		return "", err
	}
	if _, err := sourceImageRelativePathFromReference(rel); err != nil {
		return "", err
	}
	return rel, nil
}

func imageReferenceRelativePathFromValue(value string) (string, error) {
	text := strings.TrimSpace(value)
	if text == "" {
		return "", errors.New("invalid image path")
	}
	if parsed, err := url.Parse(text); err == nil {
		pathValue := parsed.EscapedPath()
		if pathValue == "" {
			pathValue = parsed.Path
		}
		if parsed.Scheme != "" || strings.HasPrefix(pathValue, "/") {
			const imageReferencePrefix = "/image-references/"
			index := strings.Index(pathValue, imageReferencePrefix)
			if index < 0 {
				return "", errors.New("invalid image path")
			}
			rel, err := url.PathUnescape(pathValue[index+len(imageReferencePrefix):])
			if err != nil {
				return "", errors.New("invalid image path")
			}
			return cleanImageReferenceRelativePath(rel)
		}
	}
	return cleanImageReferenceRelativePath(text)
}

func sourceImageRelativePathFromReference(value string) (string, error) {
	rel, err := cleanImageRelativePath(value)
	if err != nil {
		return "", err
	}
	index := strings.LastIndex(rel, imageReferenceMarker)
	if index <= 0 || index+len(imageReferenceMarker) >= len(rel) {
		return "", errors.New("invalid image path")
	}
	return cleanImageRelativePath(rel[:index])
}

func normalizeImageReferenceMetadata(value any) []imageReferenceMetadata {
	items := imageReferenceMetadataItems(value)
	if len(items) == 0 {
		return nil
	}
	refs := make([]imageReferenceMetadata, 0, len(items))
	for _, item := range items {
		rel, err := cleanImageReferenceRelativePath(toString(item["path"]))
		if err != nil {
			continue
		}
		refs = append(refs, imageReferenceMetadata{
			Path:        rel,
			Filename:    strings.TrimSpace(toString(item["filename"])),
			ContentType: strings.TrimSpace(toString(item["content_type"])),
			Size:        int64(numericMetaValue(item["size"])),
		})
	}
	return refs
}

func safeImageReferenceFilename(value string, index int) string {
	name := filepath.Base(filepath.ToSlash(strings.TrimSpace(value)))
	if name == "." || name == "/" || name == "" {
		name = "reference-" + strconv.Itoa(index+1) + ".png"
	}
	var b strings.Builder
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z':
			b.WriteRune(r)
		case r >= 'A' && r <= 'Z':
			b.WriteRune(r)
		case r >= '0' && r <= '9':
			b.WriteRune(r)
		case r == '.', r == '-', r == '_':
			b.WriteRune(r)
		default:
			b.WriteByte('-')
		}
	}
	clean := strings.Trim(b.String(), ".- _")
	if clean == "" {
		clean = "reference-" + strconv.Itoa(index+1) + ".png"
	}
	if !strings.Contains(filepath.Base(clean), ".") {
		clean += ".png"
	}
	if len(clean) > 96 {
		ext := filepath.Ext(clean)
		stem := strings.TrimSuffix(clean, ext)
		limit := 96 - len(ext)
		if limit < 1 {
			return clean[:96]
		}
		if len(stem) > limit {
			stem = stem[:limit]
		}
		clean = stem + ext
	}
	return clean
}

func imageReferenceMetadataItems(value any) []map[string]any {
	switch v := value.(type) {
	case []map[string]any:
		return v
	case []any:
		items := make([]map[string]any, 0, len(v))
		for _, item := range v {
			if m, ok := item.(map[string]any); ok {
				items = append(items, m)
			}
		}
		return items
	default:
		return nil
	}
}

func removeImageThumbnail(root, rel string) error {
	thumbPath := filepath.Join(root, filepath.FromSlash(rel)+thumbnailExtension)
	if !pathInsideRoot(root, thumbPath) {
		return errors.New("invalid image path")
	}
	removeErr := os.Remove(thumbPath)
	if removeErr != nil && !errors.Is(removeErr, os.ErrNotExist) {
		return removeErr
	}
	metaErr := os.Remove(thumbPath + ".json")
	if metaErr != nil && !errors.Is(metaErr, os.ErrNotExist) {
		return metaErr
	}
	removeEmptyParentDirs(root, filepath.Dir(thumbPath))
	return nil
}

func (s *imageStorageRemovalStats) add(next imageStorageRemovalStats) {
	s.bytes += next.bytes
	s.images += next.images
	s.thumbnails += next.thumbnails
	s.metadataFiles += next.metadataFiles
	s.referenceFiles += next.referenceFiles
}

func removeFileWithStats(path string) (bool, int64, error) {
	info, err := os.Stat(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return false, 0, nil
		}
		return false, 0, err
	}
	if info.IsDir() {
		return false, 0, nil
	}
	size := info.Size()
	if err := os.Remove(path); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return false, 0, nil
		}
		return false, 0, err
	}
	return true, size, nil
}

func directorySize(root, skipPrefix string) (int64, int) {
	root = strings.TrimSpace(root)
	if root == "" {
		return 0, 0
	}
	if skipPrefix != "" {
		if abs, err := filepath.Abs(skipPrefix); err == nil {
			skipPrefix = abs
		}
	}
	var total int64
	files := 0
	_ = filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if skipPrefix != "" {
			if abs, absErr := filepath.Abs(path); absErr == nil && (abs == skipPrefix || strings.HasPrefix(abs, skipPrefix+string(os.PathSeparator))) {
				if d.IsDir() && abs != root {
					return filepath.SkipDir
				}
				return nil
			}
		}
		if d.IsDir() {
			return nil
		}
		info, statErr := d.Info()
		if statErr != nil {
			return nil
		}
		total += info.Size()
		files++
		return nil
	})
	return total, files
}

func thumbnailCacheStats(root string) (int64, int, int) {
	var bytes int64
	thumbnails := 0
	metadataFiles := 0
	_ = filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		info, statErr := d.Info()
		if statErr != nil {
			return nil
		}
		bytes += info.Size()
		if strings.HasSuffix(path, ".json") {
			metadataFiles++
		} else {
			thumbnails++
		}
		return nil
	})
	return bytes, thumbnails, metadataFiles
}

func writeJPEGThumbnail(path string, img image.Image) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, "."+filepath.Base(path)+".*.tmp")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	encodeErr := jpeg.Encode(tmp, img, &jpeg.Options{Quality: thumbnailQuality})
	closeErr := tmp.Close()
	if encodeErr != nil || closeErr != nil {
		_ = os.Remove(tmpPath)
		if encodeErr != nil {
			return encodeErr
		}
		return closeErr
	}
	if err := os.Rename(tmpPath, path); err != nil {
		if removeErr := os.Remove(path); removeErr != nil && !errors.Is(removeErr, os.ErrNotExist) {
			_ = os.Remove(tmpPath)
			return err
		}
		if renameErr := os.Rename(tmpPath, path); renameErr != nil {
			_ = os.Remove(tmpPath)
			return renameErr
		}
	}
	return nil
}

func pathInsideRoot(root, target string) bool {
	targetAbs, err := filepath.Abs(target)
	if err != nil {
		return false
	}
	rel, err := filepath.Rel(root, targetAbs)
	if err != nil {
		return false
	}
	return rel != "." && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel)
}

func removeEmptyParentDirs(root, start string) {
	current, err := filepath.Abs(start)
	if err != nil {
		return
	}
	for pathInsideRoot(root, current) {
		err := os.Remove(current)
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return
		}
		current = filepath.Dir(current)
	}
}

func readImageMetadata(path string, sourceMtime time.Time) map[string]any {
	info, err := os.Stat(path)
	if err != nil || info.ModTime().Before(sourceMtime) {
		return nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var meta map[string]any
	if json.Unmarshal(data, &meta) != nil {
		return nil
	}
	if meta["width"] == nil || meta["height"] == nil {
		return nil
	}
	return meta
}

func isCurrentThumbnailMetadata(meta map[string]any) bool {
	return numericMetaValue(meta["thumbnail_version"]) == thumbnailCacheVersion &&
		numericMetaValue(meta["thumbnail_size"]) == ThumbnailSize &&
		numericMetaValue(meta["thumbnail_quality"]) == thumbnailQuality
}

func numericMetaValue(value any) int {
	n, _ := imageMetadataIntValue(value)
	return n
}

func imageMetadataIntValue(value any) (int, bool) {
	switch v := value.(type) {
	case int:
		return v, true
	case int64:
		return int(v), true
	case float64:
		return int(v), true
	case json.Number:
		n, err := v.Int64()
		if err == nil {
			return int(n), true
		}
	case string:
		text := strings.TrimSpace(v)
		if text == "" {
			return 0, false
		}
		n, err := strconv.Atoi(text)
		if err == nil {
			return n, true
		}
	default:
		return 0, false
	}
	return 0, false
}

func imageOutputCompressionMetadata(value any) *int {
	compression, ok := imageMetadataIntValue(value)
	if !ok {
		return nil
	}
	if compression < 0 {
		compression = 0
	} else if compression > 100 {
		compression = 100
	}
	return &compression
}

func positiveImageMetadataInt(value any) *int {
	count, ok := imageMetadataIntValue(value)
	if !ok {
		return nil
	}
	if count <= 0 {
		return nil
	}
	return &count
}

func boolMetadataValue(value any) bool {
	switch v := value.(type) {
	case bool:
		return v
	case string:
		switch strings.ToLower(strings.TrimSpace(v)) {
		case "1", "true", "yes", "on":
			return true
		default:
			return false
		}
	case float64:
		return v != 0
	case int:
		return v != 0
	case json.Number:
		n, err := v.Int64()
		return err == nil && n != 0
	default:
		return false
	}
}

func flattenImage(src image.Image) image.Image {
	b := src.Bounds()
	dst := image.NewRGBA(b)
	draw.Draw(dst, b, &image.Uniform{C: color.White}, image.Point{}, draw.Src)
	draw.Draw(dst, b, src, b.Min, draw.Over)
	return dst
}

func resizeToFit(src image.Image, maxW, maxH int) image.Image {
	b := src.Bounds()
	w, h := b.Dx(), b.Dy()
	if w <= 0 || h <= 0 {
		return src
	}
	scale := float64(maxW) / float64(w)
	if sh := float64(maxH) / float64(h); sh < scale {
		scale = sh
	}
	if scale > 1 {
		scale = 1
	}
	nw, nh := int(float64(w)*scale), int(float64(h)*scale)
	if nw < 1 {
		nw = 1
	}
	if nh < 1 {
		nh = 1
	}
	dst := image.NewRGBA(image.Rect(0, 0, nw, nh))
	for y := 0; y < nh; y++ {
		fy := (float64(y)+0.5)*float64(h)/float64(nh) - 0.5
		y0 := int(fy)
		dy := fy - float64(y0)
		if y0 < 0 {
			y0 = 0
			dy = 0
		}
		y1 := y0 + 1
		if y1 >= h {
			y1 = h - 1
		}
		for x := 0; x < nw; x++ {
			fx := (float64(x)+0.5)*float64(w)/float64(nw) - 0.5
			x0 := int(fx)
			dx := fx - float64(x0)
			if x0 < 0 {
				x0 = 0
				dx = 0
			}
			x1 := x0 + 1
			if x1 >= w {
				x1 = w - 1
			}
			dst.Set(x, y, bilinearColor(
				src.At(b.Min.X+x0, b.Min.Y+y0),
				src.At(b.Min.X+x1, b.Min.Y+y0),
				src.At(b.Min.X+x0, b.Min.Y+y1),
				src.At(b.Min.X+x1, b.Min.Y+y1),
				dx,
				dy,
			))
		}
	}
	return dst
}

func bilinearColor(c00, c10, c01, c11 color.Color, dx, dy float64) color.RGBA {
	r00, g00, b00, a00 := c00.RGBA()
	r10, g10, b10, a10 := c10.RGBA()
	r01, g01, b01, a01 := c01.RGBA()
	r11, g11, b11, a11 := c11.RGBA()
	return color.RGBA{
		R: uint8(bilinearChannel(r00, r10, r01, r11, dx, dy) >> 8),
		G: uint8(bilinearChannel(g00, g10, g01, g11, dx, dy) >> 8),
		B: uint8(bilinearChannel(b00, b10, b01, b11, dx, dy) >> 8),
		A: uint8(bilinearChannel(a00, a10, a01, a11, dx, dy) >> 8),
	}
}

func bilinearChannel(c00, c10, c01, c11 uint32, dx, dy float64) uint32 {
	top := float64(c00)*(1-dx) + float64(c10)*dx
	bottom := float64(c01)*(1-dx) + float64(c11)*dx
	return uint32(top*(1-dy) + bottom*dy + 0.5)
}

func writeJSONFile(path string, value any) error {
	data, err := json.Marshal(value)
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}

func toString(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	return ""
}

func firstNonEmptyString(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

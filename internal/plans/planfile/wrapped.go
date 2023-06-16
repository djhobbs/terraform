package planfile

import "github.com/hashicorp/terraform/internal/cloud/cloudplan"

// WrappedPlanFile is a sum type that represents a saved plan, loaded from a
// file path passed on the command line. If the specified file was a thick local
// plan file, the Local field will be populated; if it was a bookmark for a
// remote cloud plan, the Cloud field will be populated. In both cases, the
// other field is expected to be nil. Finally, the outer struct is also expected
// to be used as a pointer, so that a nil value can represent the absence of any
// plan file.
type WrappedPlanFile struct {
	local *Reader
	cloud *cloudplan.SavedPlanBookmark
}

func (w *WrappedPlanFile) IsLocal() bool {
	return w != nil && w.local != nil
}

func (w *WrappedPlanFile) IsCloud() bool {
	return w != nil && w.cloud != nil
}

// Local checks whether the wrapped value is a local plan file, and returns it if available.
func (w *WrappedPlanFile) Local() (*Reader, bool) {
	if w != nil && w.local != nil {
		return w.local, true
	} else {
		return nil, false
	}
}

// Cloud checks whether the wrapped value is a cloud plan file, and returns it if available.
func (w *WrappedPlanFile) Cloud() (*cloudplan.SavedPlanBookmark, bool) {
	if w != nil && w.cloud != nil {
		return w.cloud, true
	} else {
		return nil, false
	}
}

// NewWrappedLocal constructs a WrappedPlanFile from an already loaded local
// plan file reader. Most cases should use OpenWrapped to load from disk
// instead. If the provided reader is nil, the returned pointer is nil.
func NewWrappedLocal(l *Reader) *WrappedPlanFile {
	if l != nil {
		return &WrappedPlanFile{local: l}
	} else {
		return nil
	}
}

// NewWrappedCloud constructs a WrappedPlanFile from an already loaded cloud
// plan file. Most cases should use OpenWrapped to load from disk
// instead. If the provided plan file is nil, the returned pointer is nil.
func NewWrappedCloud(c *cloudplan.SavedPlanBookmark) *WrappedPlanFile {
	if c != nil {
		return &WrappedPlanFile{cloud: c}
	} else {
		return nil
	}
}

// OpenWrapped loads a local or cloud plan file from a specified file path, or
// returns an error if the file doesn't seem to be a plan file of either kind.
// Most consumers should use this and switch behaviors based on the kind of plan
// they expected, rather than directly using Open.
func OpenWrapped(filename string) (*WrappedPlanFile, error) {
	// First, try to load it as a local planfile.
	local, localErr := Open(filename)
	if localErr == nil {
		return &WrappedPlanFile{local: local}, nil
	}
	// Then, try to load it as a cloud plan.
	cloud, cloudErr := cloudplan.LoadSavedPlanBookmark(filename)
	if cloudErr == nil {
		return &WrappedPlanFile{cloud: &cloud}, nil
	}
	// If neither worked, return the error from trying to handle it as a local
	// planfile, since that might have more context. Cloud plans are an opaque
	// format, so we don't care to give any advice about how to fix an internal
	// problem in one.
	return nil, localErr
}

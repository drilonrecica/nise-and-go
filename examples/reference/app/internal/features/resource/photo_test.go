// Generated once by nise; owned by this application. Nise will not overwrite it.

package resource

import (
	"context"
	"errors"
	"testing"

	"workbench/internal/features/uploads"
)

// fakePhotoStore stands in for the upload lifecycle.
//
// A fake rather than the real one: what this feature is responsible for is the
// *order* — resource checked, upload finalized, key recorded — and the uploads
// package has its own twenty tests for what finalization actually decides.
// Driving the real lifecycle here would test that package again and would not
// make the ordering any more visible.
type fakePhotoStore struct {
	upload      uploads.Upload
	finalizeErr error
	// finalized records the calls, so a test can assert that finalization did
	// not happen at all on the paths where it must not.
	finalized []string
}

func (f *fakePhotoStore) Finalize(_ context.Context, id, _ string) (uploads.Upload, error) {
	f.finalized = append(f.finalized, id)
	if f.finalizeErr != nil {
		return uploads.Upload{}, f.finalizeErr
	}
	return f.upload, nil
}

func (f *fakePhotoStore) Get(context.Context, string, string) (uploads.Upload, error) {
	return f.upload, nil
}

func availableUpload() uploads.Upload {
	return uploads.Upload{ID: "upload-1", State: "available", StorageKey: "objects/abc123"}
}

func TestAttachingAPhotoFinalizesTheUploadAndRecordsItsKey(t *testing.T) {
	h := newHarness(t)
	created := h.create(t, "Microscope 2")
	store := &fakePhotoStore{upload: availableUpload()}

	updated, err := h.resources.AttachPhoto(h.ctx, store, h.orgID, created.ID, "upload-1", "user-1")
	if err != nil {
		t.Fatalf("AttachPhoto: %v", err)
	}
	if updated.PhotoKey != "objects/abc123" {
		t.Errorf("photoKey = %q, want the finalized object's key", updated.PhotoKey)
	}
	if len(store.finalized) != 1 {
		t.Errorf("Finalize was called %d time(s), want 1", len(store.finalized))
	}
}

// An upload that finalization refuses must leave no trace on the resource. The
// alternative — recording the key and finalizing afterwards — leaves a window
// in which a resource points at an object still in quarantine.
func TestAnUploadThatFinalizationRefusesIsNotAttached(t *testing.T) {
	h := newHarness(t)
	created := h.create(t, "Microscope 2")
	store := &fakePhotoStore{finalizeErr: errors.New("the declared type is not the stored type")}

	_, err := h.resources.AttachPhoto(h.ctx, store, h.orgID, created.ID, "upload-1", "user-1")
	if !errors.Is(err, ErrPhotoUnavailable) {
		t.Fatalf("err = %v, want ErrPhotoUnavailable", err)
	}
	after, err := h.resources.Get(h.ctx, h.orgID, created.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if after.PhotoKey != "" {
		t.Errorf("photoKey = %q; a refused upload was attached anyway", after.PhotoKey)
	}
}

// Finalization can succeed while leaving the object unservable — a scanner
// that could not run leaves it that way, which ADR 0027 distinguishes from
// "checked and refused". Either way nothing may point at it.
func TestAnUploadThatIsNotAvailableIsNotAttached(t *testing.T) {
	h := newHarness(t)
	created := h.create(t, "Microscope 2")
	store := &fakePhotoStore{upload: uploads.Upload{ID: "upload-1", State: "quarantined", StorageKey: "objects/abc123"}}

	_, err := h.resources.AttachPhoto(h.ctx, store, h.orgID, created.ID, "upload-1", "user-1")
	if !errors.Is(err, ErrPhotoUnavailable) {
		t.Fatalf("err = %v, want ErrPhotoUnavailable", err)
	}
	after, err := h.resources.Get(h.ctx, h.orgID, created.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if after.PhotoKey != "" {
		t.Errorf("photoKey = %q; an unavailable object was attached", after.PhotoKey)
	}
}

// The resource is checked before anything is finalized. Finalizing first would
// promote an object out of quarantine for a resource that turns out not to
// exist or to be retired, leaving a servable orphan nothing refers to.
func TestNothingIsFinalizedForAResourceThatCannotHaveAPhoto(t *testing.T) {
	h := newHarness(t)

	t.Run("a resource that does not exist", func(t *testing.T) {
		store := &fakePhotoStore{upload: availableUpload()}
		_, err := h.resources.AttachPhoto(h.ctx, store, h.orgID,
			"44444444-4444-4444-4444-444444444444", "upload-1", "user-1")
		if !errors.Is(err, ErrNotFound) {
			t.Errorf("err = %v, want ErrNotFound", err)
		}
		if len(store.finalized) != 0 {
			t.Error("an upload was finalized for a resource that does not exist")
		}
	})

	t.Run("a retired resource", func(t *testing.T) {
		created := h.create(t, "Centrifuge")
		if _, err := h.resources.Retire(h.ctx, h.orgID, created.ID); err != nil {
			t.Fatalf("Retire: %v", err)
		}
		store := &fakePhotoStore{upload: availableUpload()}
		_, err := h.resources.AttachPhoto(h.ctx, store, h.orgID, created.ID, "upload-1", "user-1")
		if !errors.Is(err, ErrRetired) {
			t.Errorf("err = %v, want ErrRetired", err)
		}
		if len(store.finalized) != 0 {
			t.Error("an upload was finalized for a retired resource")
		}
	})

	t.Run("another tenant's resource", func(t *testing.T) {
		created := h.create(t, "Spectrometer")
		other := h.newOrg(t, "other")
		store := &fakePhotoStore{upload: availableUpload()}
		_, err := h.resources.AttachPhoto(h.ctx, store, other, created.ID, "upload-1", "user-1")
		if !errors.Is(err, ErrNotFound) {
			t.Errorf("err = %v, want ErrNotFound", err)
		}
		if len(store.finalized) != 0 {
			t.Error("an upload was finalized for another tenant's resource")
		}
	})
}

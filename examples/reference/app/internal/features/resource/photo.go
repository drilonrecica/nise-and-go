// Generated once by nise; owned by this application. Nise will not overwrite it.

package resource

import (
	"context"
	"errors"
	"fmt"

	"workbench/internal/features/uploads"
)

// PhotoStore is the part of the upload lifecycle this feature needs.
//
// An interface declared here rather than a dependency on the whole uploads use
// case: what a resource needs to know about a photo is whether it is finalized
// and where it lives, and a feature that imported the entire lifecycle would
// be coupled to every future change in it.
type PhotoStore interface {
	// Finalize promotes a staged upload out of quarantine, checking the
	// stored bytes against what was declared. It is the step that decides
	// whether an object is servable at all.
	Finalize(ctx context.Context, id string, ownerUserID string) (uploads.Upload, error)
	// Get reads an upload's current state.
	Get(ctx context.Context, id string, ownerUserID string) (uploads.Upload, error)
}

// ErrPhotoUnavailable reports an upload that is not, or not yet, servable.
var ErrPhotoUnavailable = errors.New("resource: that upload cannot be used as a photo")

// AttachPhoto finalizes an upload and records it as a resource's photo.
//
// The order is the whole point, and it is the order the upload lifecycle
// exists to enforce: an object is staged, written into quarantine, and only
// then finalized — where its declared type and size are checked against the
// bytes that actually arrived and any configured scanner is consulted (ADR
// 0027). Recording the key first and finalizing afterwards would attach a
// photo that the checks might still reject, and there would be a window in
// which a resource pointed at a quarantined object.
//
// Finalization is also where ownership is checked: the upload must belong to
// the person attaching it. That is not the same question as "may this person
// edit this resource" — the caller answers that one with the authorization
// catalog — and neither substitutes for the other. Somebody who may edit a
// resource must not be able to attach an object staged by somebody else, and
// somebody who staged an object must not be able to attach it to a resource
// they may not edit.
func (r *Resources) AttachPhoto(
	ctx context.Context, store PhotoStore, orgID, resourceID, uploadID, ownerUserID string,
) (Resource, error) {
	if store == nil {
		return Resource{}, fmt.Errorf("%w: no upload store is configured", ErrPhotoUnavailable)
	}

	// The resource must exist and be in service before anything is
	// finalized. Finalizing first would promote an object out of quarantine
	// for a resource that turns out to be retired, leaving a servable orphan.
	existing, err := r.Get(ctx, orgID, resourceID)
	if err != nil {
		return Resource{}, err
	}
	if !existing.InService() {
		return Resource{}, ErrRetired
	}

	upload, err := store.Finalize(ctx, uploadID, ownerUserID)
	if err != nil {
		// Every refusal is one value. Which of "no such upload", "somebody
		// else's upload", "the wrong type", and "rejected by the scanner" it
		// was, is information about other people's objects.
		return Resource{}, fmt.Errorf("%w: %w", ErrPhotoUnavailable, err)
	}
	if !upload.Available() {
		// Finalize succeeded and the object is still not servable — a
		// scanner that could not run leaves it that way (ADR 0027 draws the
		// line between "could not check" and "checked and refused"). Either
		// way, nothing points at it.
		return Resource{}, fmt.Errorf("%w: it is %s", ErrPhotoUnavailable, upload.State)
	}

	return r.SetPhoto(ctx, orgID, resourceID, upload.StorageKey)
}

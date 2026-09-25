package wizard

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// Permissions for what the wizard creates. They match the rest of Feat's own files,
// because a project configuration names paths in the user's home.
const (
	configDirPerm  os.FileMode = 0o700
	configFilePerm os.FileMode = 0o600
)

// Write writes the reviewed configuration and returns the file it wrote. It renders
// and validates again rather than taking the text a caller was shown, so a caller
// cannot display one review and write something else.
//
// The create is exclusive, and that is the whole of the check. An existing
// configuration is never overwritten, including one that appeared during the
// conversation, and there is no force flag, because the user authored the file it
// would replace.
func (w *Wizard) Write() (string, error) {
	review, err := w.Review()
	if err != nil {
		return "", err
	}

	if err := os.MkdirAll(filepath.Dir(review.Path), configDirPerm); err != nil {
		return "", fmt.Errorf("creating the configuration directory %s: %w",
			filepath.Dir(review.Path), err)
	}

	// #nosec G304 -- the path is the configuration directory joined with a
	// validated project identifier, which is what config.File enforces
	handle, err := os.OpenFile(review.Path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, configFilePerm)
	if err != nil {
		if errors.Is(err, os.ErrExist) {
			return "", fmt.Errorf("%s already exists, and nothing was written to it", review.Path)
		}
		return "", fmt.Errorf("creating %s: %w", review.Path, err)
	}

	if _, err := handle.Write(review.Text); err != nil {
		_ = handle.Close()
		// A half-written file would make the next command report a parse error
		// about a configuration the user never wrote.
		if removeErr := os.Remove(review.Path); removeErr != nil {
			return "", fmt.Errorf("writing %s: %w (and it could not be removed either: %w)",
				review.Path, err, removeErr)
		}
		return "", fmt.Errorf("writing %s: %w", review.Path, err)
	}
	if err := handle.Close(); err != nil {
		return "", fmt.Errorf("writing %s: %w", review.Path, err)
	}
	return review.Path, nil
}

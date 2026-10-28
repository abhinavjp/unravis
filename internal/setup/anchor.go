package setup

import (
	"errors"
	"fmt"
	"path/filepath"

	"api-audit-explorer/internal/datadir"
)

// AnchorReader reads the installer's record through reg exactly the way datadir.ReadAnchor reads it from the real registry: the three
// values must be REG_SZ, a missing key or value is datadir.ErrNoAnchor, and the record carries the key's own owner and access list. It
// lets the tests send what setup wrote through datadir.TrustSharedFolder, the check Unravis and the keeper make.
func AnchorReader(reg Registry) func() (datadir.Anchor, error) {
	return func() (datadir.Anchor, error) {
		var a datadir.Anchor
		ok, err := reg.KeyExists(HKLM, AnchorKey)
		if err != nil {
			return a, err
		}
		if !ok {
			return a, datadir.ErrNoAnchor
		}
		for _, v := range []struct {
			name string
			into *string
		}{{AnchorRootValue, &a.Root}, {AnchorRootIDValue, &a.RootID}, {AnchorCasesIDValue, &a.CasesID}} {
			s, err := reg.GetString(HKLM, AnchorKey, v.name)
			switch {
			case errors.Is(err, ErrNotFound):
				return datadir.Anchor{}, datadir.ErrNoAnchor
			case errors.Is(err, ErrWrongType):
				return datadir.Anchor{}, fmt.Errorf("%s is not a plain text value", v.name)
			case err != nil:
				return datadir.Anchor{}, err
			}
			*v.into = s
		}
		sec, err := reg.KeySecurity(HKLM, AnchorKey)
		if err != nil {
			return datadir.Anchor{}, err
		}
		a.Security = sec
		return a, nil
	}
}

// TrustWith checks root with datadir.TrustSharedFolder, reading folders through sec and the record through reg.
func TrustWith(sec FileSecurity, reg Registry) func(root string) error {
	return func(root string) error {
		return datadir.TrustSharedFolder(root, sec.Read, AnchorReader(reg))
	}
}

// sharedRootRecorded reports whether the record names root (any case).
func sharedRootRecorded(reg Registry, root string) bool {
	got, err := reg.GetString(HKLM, AnchorKey, AnchorRootValue)
	return err == nil && samePath(filepath.Clean(got), root)
}

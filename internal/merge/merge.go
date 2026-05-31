package merge

import "github.com/jophira/weft/internal/profile"

// Strategy resolves conflicts when two sources define the same file.
type Strategy func(base, overlay []byte) ([]byte, error)

// ForOverlay returns the merge strategy for a given overlay mode.
func ForOverlay(o profile.Overlay) Strategy {
	switch o {
	case profile.OverlayMerge:
		return AppendStrategy
	case profile.OverlayLastWins:
		return LastWinsStrategy
	default:
		return CascadeStrategy
	}
}

// CascadeStrategy: overlay wins on conflict; base entries absent from overlay are kept.
func CascadeStrategy(base, overlay []byte) ([]byte, error) {
	if len(overlay) > 0 {
		return overlay, nil
	}
	return base, nil
}

// LastWinsStrategy: overlay always replaces base.
func LastWinsStrategy(_, overlay []byte) ([]byte, error) {
	return overlay, nil
}

// AppendStrategy: concatenates base and overlay with a newline separator.
func AppendStrategy(base, overlay []byte) ([]byte, error) {
	if len(base) == 0 {
		return overlay, nil
	}
	if len(overlay) == 0 {
		return base, nil
	}
	result := make([]byte, 0, len(base)+1+len(overlay))
	result = append(result, base...)
	if base[len(base)-1] != '\n' {
		result = append(result, '\n')
	}
	result = append(result, overlay...)
	return result, nil
}

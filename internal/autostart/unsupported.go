//go:build !linux && !darwin && !windows

package autostart

// newService reports that the platform has no autostart mechanism weft knows
// how to drive. The CLI turns ErrUnsupported into a plain message rather than a
// stack trace, and `weft` remains fully usable without autostart.
func newService(string) (Service, error) {
	return nil, ErrUnsupported
}

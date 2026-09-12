//go:build !linux

package runstate

// pidNamespace returns "" on platforms with no PID namespaces. Every process
// then agrees on what a pid means, which is exactly what the empty-equals-empty
// comparison in Read expresses.
func pidNamespace() string { return "" }

package harness

// Wirer is an optional extension of Harness for tools that will not read what
// weft writes until a pointer to it exists in the tool's own config.
//
// Most harnesses need nothing here: they load a well-known path, so writing the
// file is the whole job. Aider is the exception. It has no default conventions
// path, so a conventions file it has not been told about is inert, and an apply
// that reports success leaves the user with a profile that does nothing.
type Wirer interface {
	// Wire records the pointer in the harness's own config. It must be
	// idempotent, since every apply calls it.
	Wire(ctx ApplyCtx) error
}

// ProjectWiring runs a harness's wiring step, if it has one.
//
// It is a no-op for harnesses that need no pointer, and when the profile's
// harness_sync config withholds the instructions class, since the pointer exists
// only to deliver instructions.
func ProjectWiring(h Harness, ctx ApplyCtx) error {
	w, ok := h.(Wirer)
	if !ok {
		return nil
	}
	if !ctx.classAllowed(ClassInstructions) {
		return nil
	}
	return w.Wire(ctx)
}

package harness

// FileKind names what a file inside a harness config root is for.
//
// Wider than Class deliberately. Class answers "where does weft put this",
// which only covers what weft already projects. FileKind answers "what is this
// file", which is what a coverage report needs: the interesting entries are the
// ones weft does not manage yet.
type FileKind string

const (
	FileInstructions FileKind = "instructions"
	FileSettings     FileKind = "settings"
	FileHooks        FileKind = "hooks"
	FileCommands     FileKind = "commands"
	FileAgents       FileKind = "agents"
	FileSkills       FileKind = "skills"
	FileMCP          FileKind = "mcp"
	FileOutputStyles FileKind = "output-styles"
	FileKeybindings  FileKind = "keybindings"
	FilePlugins      FileKind = "plugins"
	FileStatusline   FileKind = "statusline"
	FileIgnore       FileKind = "ignore"
)

// KnownFile is one entry weft recognises inside a harness config root.
//
// Recognising a file is not the same as managing it. The point of declaring
// these is to make "does weft handle all of Claude Code's instruction files" a
// report rather than an argument, which means naming the ones it does not
// handle just as precisely as the ones it does.
type KnownFile struct {
	// Rel is the path relative to the harness config root.
	Rel string
	// Dir marks an entry that is a directory of files rather than one file.
	Dir bool
	// Kind is what the entry is for.
	Kind FileKind
	// Desc is a short phrase describing what it carries, shown in the report.
	Desc string
}

// FileAware is an optional Harness extension declaring what weft recognises in
// that harness's config root. A harness that does not implement it reports no
// known files, which is honest: weft genuinely does not know its layout.
type FileAware interface {
	KnownFiles() []KnownFile
	// StateEntries names top-level entries that are the harness's own runtime
	// state (sessions, caches, logs). They are excluded from the unrecognised
	// count so it stays small enough to act on.
	//
	// The list going stale is a cosmetic failure, not a correctness one: a
	// missed entry only inflates the "other" count. That is deliberate, because
	// the alternative is weft asserting knowledge of a layout it does not own.
	StateEntries() []string
}

// KnownFilesOf returns the declared entries for a harness, or nil.
func KnownFilesOf(h Harness) []KnownFile {
	if fa, ok := h.(FileAware); ok {
		return fa.KnownFiles()
	}
	return nil
}

// StateEntriesOf returns the declared state entries for a harness, or nil.
func StateEntriesOf(h Harness) []string {
	if fa, ok := h.(FileAware); ok {
		return fa.StateEntries()
	}
	return nil
}

// sensitiveEntries are never listed by name in a report and never read.
//
// A coverage report is the sort of output that gets pasted into an issue. Even
// naming a credentials file invites someone to go and look at it, and weft has
// no reason to care whether it exists.
var sensitiveEntries = map[string]bool{
	".credentials.json":    true,
	"oauth_creds.json":     true,
	"google_accounts.json": true,
	"auth.json":            true,
	".netrc":               true,
}

// IsSensitive reports whether a top-level entry name must be excluded from
// reporting entirely.
func IsSensitive(name string) bool { return sensitiveEntries[name] }

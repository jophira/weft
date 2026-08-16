package advice

// Advice codes.
//
// A code is a promise. Once a release ships one, it appears in user config as a
// mute entry and in documentation as a heading, so it must keep meaning the same
// thing. Retire a code by leaving the constant in place with a comment rather
// than reusing the number for something else.
//
// Numbering follows the area, which keeps related hints together in a mute list:
//
//	W0xx  targets and harnesses
//	W1xx  instruction content and size
//	W2xx  sources and profiles
//	W3xx  deprecations
//	W4xx  project scope
const (
	// CodeUntargetedHarness: a harness is installed but the active profile does
	// not target it.
	CodeUntargetedHarness = "W001"
	// CodeUntargetedDetected: `weft target detect` found harnesses the profile
	// does not target yet.
	CodeUntargetedDetected = "W002"

	// CodeInstructionSize: the assembled instruction file is past the warn
	// threshold.
	CodeInstructionSize = "W101"
	// CodeDuplicateBlock: the same paragraph appears more than once in the
	// assembled instruction file.
	CodeDuplicateBlock = "W102"

	// CodeNoSources: no sources are registered, so there is nothing to assemble.
	CodeNoSources = "W201"
	// CodeSourceNotSynced: a source file sits outside every managed directory,
	// so projection skipped it.
	CodeSourceNotSynced = "W202"
	// CodeWriteBackFailed: a write-back could not be completed.
	CodeWriteBackFailed = "W203"
	// CodeConflictScanFailed: the conflict scan could not run.
	CodeConflictScanFailed = "W204"

	// CodeNoProjectInstructions: the repository has no instruction file any
	// detected harness would read, so there is nothing project-scoped to fan in.
	CodeNoProjectInstructions = "W401"
	// CodeProjectWriteOff: a harness needing inline delivery is in use here, but
	// writing a tracked project file has not been opted into.
	CodeProjectWriteOff = "W402"
	// CodeProjectExcludeFailed: weft's repo-local state could not be excluded
	// from git, so it will show up in git status.
	CodeProjectExcludeFailed = "W403"

	// CodeProjectsSnippetDeprecated: the source still uses the legacy
	// <!-- weft:projects --> placeholder.
	CodeProjectsSnippetDeprecated = "W301"
	// CodeSourcesSnippetDeprecated: the source still uses the legacy
	// <!-- weft:sources --> placeholder.
	CodeSourcesSnippetDeprecated = "W302"
)

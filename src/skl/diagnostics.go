package skl

import (
	"errors"
	"fmt"
	"strings"
)

// Diagnostic codes mirror upstream src/diagnostics.ts. Codes are
// SKILLS_<WHAT_WENT_WRONG> and permanent: never rename or reuse one.
const (
	// Command line.
	CodeInvalidAgents         = "SKILLS_INVALID_AGENTS"
	CodeOptionRequiresValue   = "SKILLS_OPTION_REQUIRES_VALUE"
	CodeUnknownOption         = "SKILLS_UNKNOWN_OPTION"
	CodeInvalidOptionValue    = "SKILLS_INVALID_OPTION_VALUE"
	CodeDuplicateOption       = "SKILLS_DUPLICATE_OPTION"
	CodeRemoveAllWithNames    = "SKILLS_REMOVE_ALL_WITH_NAMES"
	CodePromptRequiresTTY     = "SKILLS_PROMPT_REQUIRES_TTY"
	CodeMissingArgument       = "SKILLS_MISSING_ARGUMENT"
	CodeMultipleSources       = "SKILLS_MULTIPLE_SOURCES"
	CodeUnknownCommand        = "SKILLS_UNKNOWN_COMMAND"
	CodeRemoveGloballyTracked = "SKILLS_REMOVE_GLOBALLY_TRACKED"

	// Sources and skill selection.
	CodeUnsafeSubpath             = "SKILLS_UNSAFE_SUBPATH"
	CodeSubpathOutsideRepo        = "SKILLS_SUBPATH_OUTSIDE_REPO"
	CodeNoMatchingSkills          = "SKILLS_NO_MATCHING_SKILLS"
	CodeNoValidSkills             = "SKILLS_NO_VALID_SKILLS"
	CodeMultipleSkillsInSource    = "SKILLS_MULTIPLE_SKILLS_IN_SOURCE"
	CodeAmbiguousSkillSelector    = "SKILLS_AMBIGUOUS_SKILL_SELECTOR"
	CodeConflictingSkillSelectors = "SKILLS_CONFLICTING_SKILL_SELECTORS"
	CodeLocalPathNotFound         = "SKILLS_LOCAL_PATH_NOT_FOUND"
	CodeWellknownScopeNotFound    = "SKILLS_WELLKNOWN_SCOPE_NOT_FOUND"
	CodeWellknownDigestMismatch   = "SKILLS_WELLKNOWN_DIGEST_MISMATCH"
	CodeWellknownIndexNotFound    = "SKILLS_WELLKNOWN_INDEX_NOT_FOUND"
	CodeSkillSkipped              = "SKILLS_SKILL_SKIPPED"

	// Git.
	CodeUnsupportedGitTransport = "SKILLS_UNSUPPORTED_GIT_TRANSPORT"
	CodeGitCloneTimeout         = "SKILLS_GIT_CLONE_TIMEOUT"
	CodeGitAuthFailed           = "SKILLS_GIT_AUTH_FAILED"
	CodeGitSAMLSSOBlocked       = "SKILLS_GIT_SAML_SSO_BLOCKED"
	CodeGitCloneFailed          = "SKILLS_GIT_CLONE_FAILED"

	// Download.
	CodeDownloadHTTPError = "SKILLS_DOWNLOAD_HTTP_ERROR"
	CodeDownloadTooLarge  = "SKILLS_DOWNLOAD_TOO_LARGE"
	CodeDownloadNoBody    = "SKILLS_DOWNLOAD_NO_BODY"
	CodeDownloadEmpty     = "SKILLS_DOWNLOAD_EMPTY"
	CodeDownloadNotASkill = "SKILLS_DOWNLOAD_NOT_A_SKILL"
	CodeDownloadNotLocked = "SKILLS_DOWNLOAD_NOT_LOCKED"

	// Archive.
	CodeArchiveInvalid                = "SKILLS_ARCHIVE_INVALID"
	CodeArchiveMultiDiskZip           = "SKILLS_ARCHIVE_MULTI_DISK_ZIP"
	CodeArchiveUnsafePath             = "SKILLS_ARCHIVE_UNSAFE_PATH"
	CodeArchiveEncryptedEntry         = "SKILLS_ARCHIVE_ENCRYPTED_ENTRY"
	CodeArchiveLinkEntry              = "SKILLS_ARCHIVE_LINK_ENTRY"
	CodeArchiveTooManyFiles           = "SKILLS_ARCHIVE_TOO_MANY_FILES"
	CodeArchiveTooLarge               = "SKILLS_ARCHIVE_TOO_LARGE"
	CodeArchiveUnsupportedCompression = "SKILLS_ARCHIVE_UNSUPPORTED_COMPRESSION"
	CodeArchiveUnsupportedFormat      = "SKILLS_ARCHIVE_UNSUPPORTED_FORMAT"
	CodeArchiveMissingSkillMD         = "SKILLS_ARCHIVE_MISSING_SKILL_MD"
	CodeWellknownArchiveUnsafePath    = "SKILLS_WELLKNOWN_ARCHIVE_UNSAFE_PATH"
	CodeWellknownArchiveTooLarge      = "SKILLS_WELLKNOWN_ARCHIVE_TOO_LARGE"
	CodeWellknownArchiveTooManyFiles  = "SKILLS_WELLKNOWN_ARCHIVE_TOO_MANY_FILES"

	// Install.
	CodeUnsafeSkillName       = "SKILLS_UNSAFE_SKILL_NAME"
	CodeCleanupOutsideTempDir = "SKILLS_CLEANUP_OUTSIDE_TEMP_DIR"
	CodeBrokenSymlink         = "SKILLS_BROKEN_SYMLINK"
	CodeLockMissingSourceURL  = "SKILLS_LOCK_MISSING_SOURCE_URL"
	CodeInvalidLocalLock      = "SKILLS_INVALID_LOCAL_LOCK"
	CodeSymlinkOutsideSkill   = "SKILLS_SYMLINK_OUTSIDE_SKILL"
	CodeSymlinkCycle          = "SKILLS_SYMLINK_CYCLE"

	// package.json "skills" field.
	CodePackageSkillsNotArray      = "SKILLS_PACKAGE_SKILLS_NOT_ARRAY"
	CodePackageSkillsInvalidEntry  = "SKILLS_PACKAGE_SKILLS_INVALID_ENTRY"
	CodePackageSkillsNotGitSource  = "SKILLS_PACKAGE_SKILLS_NOT_GIT_SOURCE"
	CodePackageSkillsDuplicateRef  = "SKILLS_PACKAGE_SKILLS_DUPLICATE_REF"
	CodePackageSkillsRefNotAllowed = "SKILLS_PACKAGE_SKILLS_REF_NOT_ALLOWED"
	CodePackageSkillsUnresolvedNPM = "SKILLS_PACKAGE_SKILLS_UNRESOLVED_NPM"

	// Notion.
	CodeNotionCLIMissing               = "SKILLS_NOTION_CLI_MISSING"
	CodeNotionAPIFailed                = "SKILLS_NOTION_API_FAILED"
	CodeNotionAPITimeout               = "SKILLS_NOTION_API_TIMEOUT"
	CodeNotionAPIOutputTooLarge        = "SKILLS_NOTION_API_OUTPUT_TOO_LARGE"
	CodeNotionCLIStartFailed           = "SKILLS_NOTION_CLI_START_FAILED"
	CodeNotionResponseMissingField     = "SKILLS_NOTION_RESPONSE_MISSING_FIELD"
	CodeNotionResponseInvalidField     = "SKILLS_NOTION_RESPONSE_INVALID_FIELD"
	CodeNotionInvalidPack              = "SKILLS_NOTION_INVALID_PACK"
	CodeNotionInvalidListResponse      = "SKILLS_NOTION_INVALID_LIST_RESPONSE"
	CodeNotionInvalidDirectoryResponse = "SKILLS_NOTION_INVALID_DIRECTORY_RESPONSE"
	CodeNotionInvalidJSON              = "SKILLS_NOTION_INVALID_JSON"
	CodeNotionInvalidCursor            = "SKILLS_NOTION_INVALID_CURSOR"
	CodeNotionNoPacks                  = "SKILLS_NOTION_NO_PACKS"
	CodeNotionNoMatchingPacks          = "SKILLS_NOTION_NO_MATCHING_PACKS"
	CodeNotionPackChanged              = "SKILLS_NOTION_PACK_CHANGED"
	CodeNotionPackPrepareFailed        = "SKILLS_NOTION_PACK_PREPARE_FAILED"
	CodeNotionPackNoSkills             = "SKILLS_NOTION_PACK_NO_SKILLS"

	// skills use.
	CodeUseAgentUnsupported     = "SKILLS_USE_AGENT_UNSUPPORTED"
	CodeUseAgentCommandNotFound = "SKILLS_USE_AGENT_COMMAND_NOT_FOUND"
	CodeUseAgentWildcard        = "SKILLS_USE_AGENT_WILDCARD"
	CodeUseMultipleAgents       = "SKILLS_USE_MULTIPLE_AGENTS"
	CodeUseNoMatchingSkill      = "SKILLS_USE_NO_MATCHING_SKILL"

	// Programmatic API / internal.
	CodeSourceFetchFailed         = "SKILLS_SOURCE_FETCH_FAILED"
	CodeSourceDisposed            = "SKILLS_SOURCE_DISPOSED"
	CodeDuplicateSkillName        = "SKILLS_DUPLICATE_SKILL_NAME"
	CodeSkillContainsSymlinks     = "SKILLS_SKILL_CONTAINS_SYMLINKS"
	CodeAgentNotAllowed           = "SKILLS_AGENT_NOT_ALLOWED"
	CodeNoTargetAgents            = "SKILLS_NO_TARGET_AGENTS"
	CodeGlobalInstallUnsupported  = "SKILLS_GLOBAL_INSTALL_UNSUPPORTED"
	CodeInstallFailed             = "SKILLS_INSTALL_FAILED"
	CodeRemoveFailed              = "SKILLS_REMOVE_FAILED"
	CodeLockWriteFailed           = "SKILLS_LOCK_WRITE_FAILED"
	CodeProviderAlreadyRegistered = "SKILLS_PROVIDER_ALREADY_REGISTERED"
)

// DiagnosticError is a user-facing error with a permanent code (upstream
// nostics Diagnostic). Why is the full user-facing message; Fix carries
// optional guidance printed on its own line. Message is Why plus Fix joined
// by a newline, i.e. exactly what Error() returns.
type DiagnosticError struct {
	Code    string
	Message string
	Why     string
	Fix     string
	// Cause is an optional wrapped error (exposed via Unwrap).
	Cause error
}

// NewDiagnostic builds a DiagnosticError. An empty fix is omitted.
func NewDiagnostic(code, why, fix string) *DiagnosticError {
	msg := why
	if fix != "" {
		msg = why + "\n" + fix
	}
	return &DiagnosticError{Code: code, Message: msg, Why: why, Fix: fix}
}

// Diagnosticf builds a DiagnosticError without a fix line from a format string.
func Diagnosticf(code, format string, args ...interface{}) *DiagnosticError {
	return NewDiagnostic(code, fmt.Sprintf(format, args...), "")
}

// WithCause attaches an underlying error and returns d for chaining.
func (d *DiagnosticError) WithCause(err error) *DiagnosticError {
	d.Cause = err
	return d
}

func (d *DiagnosticError) Error() string {
	if d == nil {
		return ""
	}
	if d.Message != "" {
		return d.Message
	}
	if d.Fix != "" {
		return d.Why + "\n" + d.Fix
	}
	return d.Why
}

// Unwrap exposes the optional cause.
func (d *DiagnosticError) Unwrap() error { return d.Cause }

// Is matches another *DiagnosticError with the same code, so sentinel values
// like ErrWellKnownDigestMismatch work with errors.Is.
func (d *DiagnosticError) Is(target error) bool {
	t, ok := target.(*DiagnosticError)
	if !ok || t == nil || d == nil {
		return false
	}
	return t.Code != "" && t.Code == d.Code && t.Why == ""
}

// DiagnosticCode returns the code of err if it is (or wraps) a DiagnosticError.
func DiagnosticCode(err error) string {
	var d *DiagnosticError
	if errors.As(err, &d) && d != nil {
		return d.Code
	}
	return ""
}

// IsDiagnostic reports whether err is (or wraps) a DiagnosticError. When codes
// are given, the diagnostic's code must match one of them.
// The legacy lock.go sentinel ErrInvalidLocalLock also counts as a
// SKILLS_INVALID_LOCAL_LOCK diagnostic.
func IsDiagnostic(err error, codes ...string) bool {
	if err == nil {
		return false
	}
	var d *DiagnosticError
	isDiag := errors.As(err, &d) && d != nil
	legacyLock := errors.Is(err, ErrInvalidLocalLock)
	if len(codes) == 0 {
		return isDiag || legacyLock
	}
	for _, c := range codes {
		if isDiag && c == d.Code {
			return true
		}
		if legacyLock && c == CodeInvalidLocalLock {
			return true
		}
	}
	return false
}

// FormatError renders an error for the terminal like upstream's nostics
// ansiFormatter (without colors): "[CODE] why" followed, when a fix exists,
// by "\n╰▶ fix: <fix>" with continuation lines indented under the fix text.
// Non-diagnostic errors render as err.Error().
func FormatError(err error) string {
	if err == nil {
		return ""
	}
	var d *DiagnosticError
	if errors.As(err, &d) && d != nil {
		why := d.Why
		if why == "" && d.Fix == "" {
			why = d.Message
		}
		out := why
		if d.Code != "" {
			out = "[" + d.Code + "] " + why
		}
		if d.Fix != "" {
			out += "\n╰▶ fix: " + strings.ReplaceAll(d.Fix, "\n", "\n        ")
		}
		return out
	}
	return err.Error()
}

// availableSkillsFix formats the "Available skills:" fix block (empty when
// there are no names).
func availableSkillsFix(names []string) string {
	if len(names) == 0 {
		return ""
	}
	lines := []string{"Available skills:"}
	for _, n := range names {
		lines = append(lines, "  - "+n)
	}
	return strings.Join(lines, "\n")
}

// Sentinel diagnostics usable with errors.Is (matched by code only).
var (
	ErrWellKnownDigestMismatch = &DiagnosticError{Code: CodeWellknownDigestMismatch}
	ErrWellKnownScopeNotFound  = &DiagnosticError{Code: CodeWellknownScopeNotFound}
	ErrWellKnownIndexNotFound  = &DiagnosticError{Code: CodeWellknownIndexNotFound}
)

// Constructors for diagnostics shared across commands. Messages match
// upstream src/diagnostics.ts verbatim.

// ErrInvalidAgents reports unknown agent names.
func ErrInvalidAgents(invalid, valid []string) *DiagnosticError {
	return NewDiagnostic(CodeInvalidAgents,
		"Invalid agents: "+strings.Join(invalid, ", "),
		"Valid agents: "+strings.Join(valid, ", "))
}

// ErrOptionRequiresValue reports a flag missing its value ("--skill requires a skill name").
func ErrOptionRequiresValue(option, value string) *DiagnosticError {
	return Diagnosticf(CodeOptionRequiresValue, "%s requires %s", option, value)
}

// ErrUnknownOption reports an unrecognized flag.
func ErrUnknownOption(option string) *DiagnosticError {
	return Diagnosticf(CodeUnknownOption, "Unknown option: %s", option)
}

// ErrInvalidOptionValue reports a flag with an invalid value ("--metadata must be valid JSON").
func ErrInvalidOptionValue(option, expected string) *DiagnosticError {
	return Diagnosticf(CodeInvalidOptionValue, "%s must be %s", option, expected)
}

// ErrDuplicateOption reports a flag given more than once.
func ErrDuplicateOption(option string) *DiagnosticError {
	return Diagnosticf(CodeDuplicateOption, "Only one %s value can be provided", option)
}

// ErrRemoveAllWithNames reports `remove --all` combined with names.
func ErrRemoveAllWithNames(example string) *DiagnosticError {
	return NewDiagnostic(CodeRemoveAllWithNames,
		"Cannot combine --all with specific skill names.",
		"Use `skills remove --all` to remove every skill, or omit --all to remove only the named skills."+
			"\n   Example: skills remove "+example+" -y")
}

// ErrPromptRequiresTTY reports an interactive prompt without a TTY.
func ErrPromptRequiresTTY() *DiagnosticError {
	return NewDiagnostic(CodePromptRequiresTTY,
		"Interactive prompt required but stdin is not a TTY. Nothing was installed. "+
			"Use --agent <name> (or --agent '*') and -y to run non-interactively.", "")
}

// ErrMissingArgument reports a missing positional argument.
func ErrMissingArgument(name string) *DiagnosticError {
	return Diagnosticf(CodeMissingArgument, "Missing required argument: %s", name)
}

// ErrMultipleSources reports more than one source argument.
func ErrMultipleSources(received []string) *DiagnosticError {
	return Diagnosticf(CodeMultipleSources, "Expected one source, received %d: %s",
		len(received), strings.Join(received, ", "))
}

// ErrUnknownCommand reports an unknown CLI command.
func ErrUnknownCommand(command string) *DiagnosticError {
	return NewDiagnostic(CodeUnknownCommand, "Unknown command: "+command, "Run skills --help for usage.")
}

// ErrRemoveGloballyTracked reports skills tracked in the global lock.
func ErrRemoveGloballyTracked(skills []string) *DiagnosticError {
	return NewDiagnostic(CodeRemoveGloballyTracked,
		"Tracked in the global lock: "+strings.Join(skills, ", "),
		"Remove globally with: skills remove "+strings.Join(skills, " ")+" -g")
}

// ErrUnsafeSubpath reports a subpath containing ".." segments.
func ErrUnsafeSubpath(subpath string) *DiagnosticError {
	return NewDiagnostic(CodeUnsafeSubpath, fmt.Sprintf(
		"Unsafe subpath: \"%s\" contains path traversal segments. Subpaths must not contain \"..\" components.",
		subpath), "")
}

// ErrSubpathOutsideRepo reports a subpath resolving outside the base.
func ErrSubpathOutsideRepo(subpath string) *DiagnosticError {
	return NewDiagnostic(CodeSubpathOutsideRepo, fmt.Sprintf(
		"Invalid subpath: \"%s\" resolves outside the repository directory. Subpath must not contain \"..\" segments that escape the base path.",
		subpath), "")
}

// ErrNoMatchingSkills reports requested skills that matched nothing.
func ErrNoMatchingSkills(requested, available []string) *DiagnosticError {
	return NewDiagnostic(CodeNoMatchingSkills,
		"No matching skills found for: "+strings.Join(requested, ", "),
		availableSkillsFix(available))
}

// ErrNoValidSkills reports a source without any valid SKILL.md.
func ErrNoValidSkills() *DiagnosticError {
	return NewDiagnostic(CodeNoValidSkills,
		"No valid skills found. Skills require a SKILL.md with name and description.", "")
}

// ErrMultipleSkillsInSource reports `skills use` on a multi-skill source.
// cliName is the binary name used in the examples (e.g. "skl").
func ErrMultipleSkillsInSource(cliName, source string, names []string) *DiagnosticError {
	lines := []string{"This source contains multiple skills. Specify exactly one skill:"}
	for _, n := range names {
		lines = append(lines, "  - "+n)
	}
	example := "<skill>"
	if len(names) > 0 {
		example = names[0]
	}
	return NewDiagnostic(CodeMultipleSkillsInSource, strings.Join(lines, "\n"),
		fmt.Sprintf("Examples:\n  %s use %s@%s\n  %s use %s --skill %s", cliName, source, example, cliName, source, example))
}

// ErrAmbiguousSkillSelector reports a selector matching several skills.
func ErrAmbiguousSkillSelector(selector string) *DiagnosticError {
	return Diagnosticf(CodeAmbiguousSkillSelector, "Skill selector \"%s\" matched multiple skills.", selector)
}

// ErrConflictingSkillSelectors reports source@skill disagreeing with --skill.
func ErrConflictingSkillSelectors(fromSource, fromOption string) *DiagnosticError {
	return Diagnosticf(CodeConflictingSkillSelectors,
		"Conflicting skill selectors: source selects \"%s\" but --skill selects \"%s\". Provide one selector.",
		fromSource, fromOption)
}

// ErrLocalPathNotFound reports a missing local source path.
func ErrLocalPathNotFound(p string) *DiagnosticError {
	return Diagnosticf(CodeLocalPathNotFound, "Local path does not exist: %s", p)
}

// ErrWellKnownScope reports a scoped well-known URL with no scoped skills.
func ErrWellKnownScope(scopePath, rootURL string) *DiagnosticError {
	return Diagnosticf(CodeWellknownScopeNotFound,
		"No skills found for the scoped path '%s' on %s. "+
			"Not falling back to the root skills index because that would install every skill the host publishes. "+
			"Check the URL, or run 'skills add %s' to install from the root index.", scopePath, rootURL, rootURL)
}

// ErrWellKnownDigest reports a v0.2.0 artifact digest mismatch.
func ErrWellKnownDigest(skill, expected, actual string) *DiagnosticError {
	return Diagnosticf(CodeWellknownDigestMismatch,
		"Digest mismatch for skill '%s': expected %s, received %s.", skill, expected, actual)
}

// ErrWellKnownIndex reports a URL without a well-known index.
func ErrWellKnownIndex() *DiagnosticError {
	return NewDiagnostic(CodeWellknownIndexNotFound,
		"No skills found at this URL. Make sure the server has a /.well-known/agent-skills/index.json or /.well-known/skills/index.json file.", "")
}

// ErrLockMissingSourceURL reports a generic git lock entry without sourceUrl.
func ErrLockMissingSourceURL(skill string) *DiagnosticError {
	return Diagnosticf(CodeLockMissingSourceURL,
		"Cannot restore %s: skills-lock.json is missing sourceUrl for this generic Git source", skill)
}

// ErrInvalidLocalLockFile reports a malformed skills-lock.json.
func ErrInvalidLocalLockFile(path, reason string) *DiagnosticError {
	return Diagnosticf(CodeInvalidLocalLock, "Invalid %s: %s. Fix or delete the file, then retry.", path, reason).
		WithCause(ErrInvalidLocalLock)
}

// ErrPackageSkillsNotArray reports package.json "skills" that is not an array.
func ErrPackageSkillsNotArray() *DiagnosticError {
	return NewDiagnostic(CodePackageSkillsNotArray, `package.json: "skills" must be an array`, "")
}

// ErrPackageSkillsInvalidEntry reports an invalid package.json "skills" entry.
func ErrPackageSkillsInvalidEntry(declarer, entry string) *DiagnosticError {
	return Diagnosticf(CodePackageSkillsInvalidEntry, `%s: invalid "skills" entry %s`, declarer, entry)
}

// ErrPackageSkillsNotGitSource reports a package.json "skills" source that is not git.
func ErrPackageSkillsNotGitSource(declarer, source string) *DiagnosticError {
	return Diagnosticf(CodePackageSkillsNotGitSource, `%s: "%s" is not a git source`, declarer, source)
}

// ErrPackageSkillsDuplicateRef reports a source with a ref plus a "ref" field.
func ErrPackageSkillsDuplicateRef(declarer, source string) *DiagnosticError {
	return Diagnosticf(CodePackageSkillsDuplicateRef, `%s: "%s" already has a ref; remove "ref"`, declarer, source)
}

// ErrPackageSkillsRefNotAllowed reports a "ref" on a source that cannot take one.
func ErrPackageSkillsRefNotAllowed(declarer, source string) *DiagnosticError {
	return Diagnosticf(CodePackageSkillsRefNotAllowed, `%s: "ref" cannot be used with "%s"`, declarer, source)
}

// ErrPackageSkillsUnresolvedNPM reports an unresolvable npm: skills entry.
func ErrPackageSkillsUnresolvedNPM(declarer, pkg string) *DiagnosticError {
	return Diagnosticf(CodePackageSkillsUnresolvedNPM, `%s: cannot resolve "npm:%s"; is it a dependency?`, declarer, pkg)
}

// ErrUnsupportedGitTransport reports a blocked git transport (ext::).
func ErrUnsupportedGitTransport(transport string) *DiagnosticError {
	return Diagnosticf(CodeUnsupportedGitTransport, "Unsupported Git transport: %s", transport)
}

// ErrCleanupOutsideTempDir reports a refused cleanup outside os.TempDir().
func ErrCleanupOutsideTempDir() *DiagnosticError {
	return NewDiagnostic(CodeCleanupOutsideTempDir, "Attempted to clean up directory outside of temp directory", "")
}

// ErrSourceFetchFailed wraps a fetch failure for a source.
func ErrSourceFetchFailed(source string, cause error) *DiagnosticError {
	detail := ""
	if cause != nil {
		detail = cause.Error()
	}
	return Diagnosticf(CodeSourceFetchFailed, "Could not fetch %s: %s", source, detail).WithCause(cause)
}

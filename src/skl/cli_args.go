package skl

import (
	"regexp"
	"strings"
)

// CLIOptionType is the value arity of a CLI option in ParseCLIArgs.
type CLIOptionType int

const (
	CLIOptionBoolean CLIOptionType = iota
	CLIOptionString
	CLIOptionList
)

// CLIOptionSpec describes one option accepted by ParseCLIArgs.
type CLIOptionSpec struct {
	Type  CLIOptionType
	Short string
	Value string
}

// ParsedCLIArgs is the result of ParseCLIArgs.
type ParsedCLIArgs struct {
	Bools      map[string]bool
	Strings    map[string][]string
	FlagOrder  []string
	Positionals []string
	Unknown    []string
	Errors     []*DiagnosticError

	// TrailingListOption is the canonical option name of the final option when it
	// was a space-separated CLIOptionList that consumed at least two values and no
	// `--` terminator appeared after it (used by ParseAddCLIOptions to reclaim a
	// trailing source positional in `skl add -s alpha ./skills`).
	TrailingListOption string
	SawTerminator      bool
}

var shortGroupRe = regexp.MustCompile(`^-[A-Za-z]{2,}$`)

// ParseCLIArgs parses command-line tokens against spec, mirroring upstream
// src/cli-args.ts (supporting --flag=value, -f=value, -fvalue, grouped short
// boolean flags like -gy, multi-token list flags, and the -- terminator).
func ParseCLIArgs(args []string, spec map[string]CLIOptionSpec) ParsedCLIArgs {
	res := ParsedCLIArgs{
		Bools:   make(map[string]bool),
		Strings: make(map[string][]string),
	}

	shortBooleans := make(map[byte]bool)
	shortToName := make(map[string]string)
	for name, def := range spec {
		if len(def.Short) == 1 {
			shortToName[def.Short] = name
			if def.Type == CLIOptionBoolean {
				shortBooleans[def.Short[0]] = true
			}
		}
	}

	expanded := expandShortGroups(args, shortBooleans)

	for i := 0; i < len(expanded); i++ {
		tok := expanded[i]
		if tok == "--" {
			res.SawTerminator = true
			res.TrailingListOption = ""
			res.Positionals = append(res.Positionals, expanded[i+1:]...)
			break
		}

		if strings.HasPrefix(tok, "--") {
			res.TrailingListOption = ""
			body := tok[2:]
			name, inlineVal, hasInline := strings.Cut(body, "=")
			rawName := "--" + name
			def, ok := spec[name]
			if !ok {
				res.Unknown = append(res.Unknown, rawName)
				continue
			}
			if def.Type == CLIOptionBoolean {
				if hasInline {
					res.Errors = append(res.Errors, ErrOptionTakesNoValue(rawName))
					continue
				}
				res.Bools[name] = true
				res.FlagOrder = append(res.FlagOrder, name)
				continue
			}
			if hasInline {
				if inlineVal == "" {
					res.Errors = append(res.Errors, ErrOptionRequiresValue(rawName, cliValueLabel(def)))
					continue
				}
				res.Strings[name] = append(res.Strings[name], inlineVal)
				res.FlagOrder = append(res.FlagOrder, name)
				continue
			}
			if i+1 >= len(expanded) {
				res.Errors = append(res.Errors, ErrOptionRequiresValue(rawName, cliValueLabel(def)))
				continue
			}
			i++
			val := expanded[i]
			if val == "" || strings.HasPrefix(val, "-") {
				res.Errors = append(res.Errors, ErrOptionRequiresValue(rawName, cliValueLabel(def)))
				continue
			}
			res.Strings[name] = append(res.Strings[name], val)
			res.FlagOrder = append(res.FlagOrder, name)
			if def.Type == CLIOptionList {
				consumedExtra := false
				for i+1 < len(expanded) && expanded[i+1] != "--" && !isOptionToken(expanded[i+1]) {
					i++
					res.Strings[name] = append(res.Strings[name], expanded[i])
					consumedExtra = true
				}
				if consumedExtra {
					res.TrailingListOption = name
				}
			}
			continue
		}

		if strings.HasPrefix(tok, "-") && len(tok) > 1 {
			res.TrailingListOption = ""
			short := tok[1:2]
			rest := tok[2:]
			rawName := "-" + short
			name, ok := shortToName[short]
			if !ok {
				res.Unknown = append(res.Unknown, rawName)
				continue
			}
			def := spec[name]
			if def.Type == CLIOptionBoolean {
				if strings.HasPrefix(rest, "=") {
					res.Errors = append(res.Errors, ErrOptionTakesNoValue(rawName))
					continue
				}
				res.Bools[name] = true
				res.FlagOrder = append(res.FlagOrder, name)
				if rest != "" {
					for j := 0; j < len(rest); j++ {
						s := rest[j : j+1]
						if n, exists := shortToName[s]; exists && spec[n].Type == CLIOptionBoolean {
							res.Bools[n] = true
							res.FlagOrder = append(res.FlagOrder, n)
						} else {
							res.Unknown = append(res.Unknown, "-"+s)
						}
					}
				}
				continue
			}
			if rest != "" {
				val := strings.TrimPrefix(rest, "=")
				if val == "" {
					res.Errors = append(res.Errors, ErrOptionRequiresValue(rawName, cliValueLabel(def)))
					continue
				}
				res.Strings[name] = append(res.Strings[name], val)
				res.FlagOrder = append(res.FlagOrder, name)
				continue
			}
			if i+1 >= len(expanded) {
				res.Errors = append(res.Errors, ErrOptionRequiresValue(rawName, cliValueLabel(def)))
				continue
			}
			i++
			val := expanded[i]
			if val == "" || strings.HasPrefix(val, "-") {
				res.Errors = append(res.Errors, ErrOptionRequiresValue(rawName, cliValueLabel(def)))
				continue
			}
			res.Strings[name] = append(res.Strings[name], val)
			res.FlagOrder = append(res.FlagOrder, name)
			if def.Type == CLIOptionList {
				consumedExtra := false
				for i+1 < len(expanded) && expanded[i+1] != "--" && !isOptionToken(expanded[i+1]) {
					i++
					res.Strings[name] = append(res.Strings[name], expanded[i])
					consumedExtra = true
				}
				if consumedExtra {
					res.TrailingListOption = name
				}
			}
			continue
		}

		res.TrailingListOption = ""
		res.Positionals = append(res.Positionals, tok)
	}

	return res
}

func expandShortGroups(args []string, shortBooleans map[byte]bool) []string {
	out := make([]string, 0, len(args))
	pastTerminator := false
	for _, arg := range args {
		if pastTerminator {
			out = append(out, arg)
			continue
		}
		if arg == "--" {
			pastTerminator = true
			out = append(out, arg)
			continue
		}
		if shortGroupRe.MatchString(arg) && shortBooleans[arg[1]] {
			for i := 1; i < len(arg); i++ {
				out = append(out, "-"+arg[i:i+1])
			}
			continue
		}
		out = append(out, arg)
	}
	return out
}

func isOptionToken(tok string) bool {
	return len(tok) > 1 && strings.HasPrefix(tok, "-")
}

func cliValueLabel(def CLIOptionSpec) string {
	if def.Value != "" {
		return def.Value
	}
	return "a value"
}

package skl

import (
	"reflect"
	"testing"
)

func TestParseCLIArgs(t *testing.T) {
	spec := map[string]CLIOptionSpec{
		"global":   {Type: CLIOptionBoolean, Short: "g"},
		"yes":      {Type: CLIOptionBoolean, Short: "y"},
		"copy":     {Type: CLIOptionBoolean},
		"agent":    {Type: CLIOptionList, Short: "a", Value: "an agent name"},
		"skill":    {Type: CLIOptionList, Short: "s", Value: "a skill name"},
		"metadata": {Type: CLIOptionString, Value: "a JSON value"},
	}

	t.Run("space-separated flags and multi-token lists", func(t *testing.T) {
		res := ParseCLIArgs([]string{"owner/repo", "--agent", "claude-code", "cursor", "-g", "-y"}, spec)
		if len(res.Errors) != 0 {
			t.Fatalf("errors = %v", res.Errors)
		}
		if !reflect.DeepEqual(res.Positionals, []string{"owner/repo"}) {
			t.Errorf("positionals = %v", res.Positionals)
		}
		if !reflect.DeepEqual(res.Strings["agent"], []string{"claude-code", "cursor"}) {
			t.Errorf("agent = %v", res.Strings["agent"])
		}
		if !res.Bools["global"] || !res.Bools["yes"] {
			t.Errorf("bools = %v", res.Bools)
		}
	})

	t.Run("long --flag=value inline syntax does not consume following positionals", func(t *testing.T) {
		res := ParseCLIArgs([]string{"owner/repo", "--agent=claude-code", "--skill=my-skill"}, spec)
		if len(res.Errors) != 0 || !reflect.DeepEqual(res.Positionals, []string{"owner/repo"}) ||
			!reflect.DeepEqual(res.Strings["agent"], []string{"claude-code"}) ||
			!reflect.DeepEqual(res.Strings["skill"], []string{"my-skill"}) {
			t.Errorf("res = %+v", res)
		}

		res = ParseCLIArgs([]string{"--agent=claude-code", "owner/repo"}, spec)
		if len(res.Errors) != 0 || !reflect.DeepEqual(res.Positionals, []string{"owner/repo"}) ||
			!reflect.DeepEqual(res.Strings["agent"], []string{"claude-code"}) {
			t.Errorf("res = %+v", res)
		}
	})

	t.Run("short -f=value and attached -fvalue inline syntax", func(t *testing.T) {
		res := ParseCLIArgs([]string{"owner/repo", "-a=cursor", "-aclaude-code", "-s=one"}, spec)
		if len(res.Errors) != 0 || !reflect.DeepEqual(res.Positionals, []string{"owner/repo"}) ||
			!reflect.DeepEqual(res.Strings["agent"], []string{"cursor", "claude-code"}) ||
			!reflect.DeepEqual(res.Strings["skill"], []string{"one"}) {
			t.Errorf("res = %+v", res)
		}
	})

	t.Run("grouped short boolean flags like -gy", func(t *testing.T) {
		res := ParseCLIArgs([]string{"owner/repo", "-gy"}, spec)
		if len(res.Errors) != 0 || !res.Bools["global"] || !res.Bools["yes"] {
			t.Errorf("res = %+v", res)
		}
	})

	t.Run("-- option terminator stops flag parsing and multi-token list consumption", func(t *testing.T) {
		res := ParseCLIArgs([]string{"--skill", "one", "--", "owner/repo", "--not-a-flag"}, spec)
		if len(res.Errors) != 0 || !reflect.DeepEqual(res.Strings["skill"], []string{"one"}) ||
			!reflect.DeepEqual(res.Positionals, []string{"owner/repo", "--not-a-flag"}) {
			t.Errorf("res = %+v", res)
		}
	})

	t.Run("missing option values and boolean inline values", func(t *testing.T) {
		for _, tc := range []struct {
			args []string
			code string
			want string
		}{
			{[]string{"--skill"}, CodeOptionRequiresValue, "--skill requires a skill name"},
			{[]string{"-s"}, CodeOptionRequiresValue, "-s requires a skill name"},
			{[]string{"--agent", "-y"}, CodeOptionRequiresValue, "--agent requires an agent name"},
			{[]string{"--skill="}, CodeOptionRequiresValue, "--skill requires a skill name"},
			{[]string{"-a="}, CodeOptionRequiresValue, "-a requires an agent name"},
			{[]string{"--yes=true"}, CodeOptionTakesNoValue, "--yes does not take a value"},
		} {
			res := ParseCLIArgs(tc.args, spec)
			if len(res.Errors) != 1 || res.Errors[0].Code != tc.code || res.Errors[0].Error() != tc.want {
				t.Errorf("%v: errors = %v, want [%s] %q", tc.args, res.Errors, tc.code, tc.want)
			}
		}
	})

	t.Run("unknown options are recorded without failing", func(t *testing.T) {
		res := ParseCLIArgs([]string{"owner/repo", "--unknown", "--also=1", "-x"}, spec)
		if len(res.Errors) != 0 || !reflect.DeepEqual(res.Positionals, []string{"owner/repo"}) ||
			!reflect.DeepEqual(res.Unknown, []string{"--unknown", "--also", "-x"}) {
			t.Errorf("res = %+v", res)
		}
	})
}

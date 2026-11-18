package as3

import "strings"

// tclEscaper escapes what TCL substitutes inside a double quoted word, so flock values (domains, paths, bodies) put
// in a generated iRule stay literal strings and can't run commands. Braces are literal in a quoted word but escaped
// all the same, since the quoted word sits in the braced body of an event or a proc, which TCL ends at the first
// unbalanced closing brace, quotes or not.
var tclEscaper = strings.NewReplacer(
	`\`, `\\`,
	`"`, `\"`,
	`{`, `\{`,
	`}`, `\}`,
	`$`, `\$`,
	`[`, `\[`,
	`]`, `\]`,
	"\n", `\n`,
	"\r", `\r`,
	"\t", `\t`,
)

func tclEscape(s string) string {
	return tclEscaper.Replace(s)
}

func tclQuote(s string) string {
	return `"` + tclEscape(s) + `"`
}

// globEscaper escapes the glob characters of `string match` other than the wildcard itself.
var globEscaper = strings.NewReplacer(`\`, `\\`, `[`, `\[`, `]`, `\]`, `?`, `\?`)

func indent(block, prefix string) string {
	lines := strings.Split(block, "\n")
	for i, line := range lines {
		if line != "" {
			lines[i] = prefix + line
		}
	}
	return strings.Join(lines, "\n")
}

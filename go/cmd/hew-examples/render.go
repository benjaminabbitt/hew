package main

import (
	"fmt"
	"path/filepath"
	"strings"
)

// fenceFor picks the code-fence language for a file. `.hew` is deliberately
// rendered as `diff`: a hew patch mirrors the document's shape with `+`/`-`
// margins and `@@` hunk headers, so the diff grammar highlights it almost
// exactly right — and Shiki has no hew grammar to lose.
func fenceFor(path string) string {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".hew", ".patch", ".diff":
		return "diff"
	case ".hewt":
		return "yaml"
	case ".json":
		return "json"
	case ".yaml", ".yml":
		return "yaml"
	case ".toml":
		return "toml"
	default:
		return "text"
	}
}

// fence wraps body in a code fence long enough to survive any backticks inside
// it.
func fence(lang, body string) string {
	ticks := "```"
	for strings.Contains(body, ticks) {
		ticks += "`"
	}
	if !strings.HasSuffix(body, "\n") {
		body += "\n"
	}
	return ticks + lang + "\n" + body + ticks + "\n"
}

// renderScenario turns an execution into the Starlight page for it.
func renderScenario(ex *execution) string {
	sc := ex.Scenario
	var b strings.Builder

	b.WriteString("---\n")
	fmt.Fprintf(&b, "title: %s\n", yamlString(sc.Title))
	fmt.Fprintf(&b, "description: %s\n", yamlString(sc.Description))
	if sc.Order != 0 {
		b.WriteString("sidebar:\n")
		fmt.Fprintf(&b, "  order: %d\n", sc.Order)
	}
	b.WriteString("---\n\n")

	b.WriteString(":::note[Generated transcript]\n")
	b.WriteString("Every command and every byte of output below was produced by running the `hew` CLI ")
	b.WriteString("against the fixtures in [`examples/" + sc.Name + "/`](https://github.com/benjaminabbitt/hew/tree/main/examples/" + sc.Name + ") ")
	b.WriteString("when this page was built. Nothing here is transcribed by hand.\n")
	b.WriteString(":::\n\n")

	if sc.Intro != "" {
		b.WriteString(strings.TrimRight(sc.Intro, "\n") + "\n\n")
	}

	if len(sc.Show) > 0 {
		b.WriteString("## The starting files\n\n")
		for _, name := range sc.Show {
			fmt.Fprintf(&b, "**`%s`**\n\n", name)
			b.WriteString(fence(fenceFor(name), ex.Start[name]) + "\n")
		}
	}

	n := 0
	for _, res := range ex.Steps {
		n++
		title := res.Step.Title
		if title == "" {
			if c, ok := res.subject(); ok {
				title = "`" + shellJoin(c.Argv) + "`"
			}
		}
		fmt.Fprintf(&b, "## %d. %s\n\n", n, title)
		if res.Step.Note != "" {
			b.WriteString(strings.TrimRight(res.Step.Note, "\n") + "\n\n")
		}

		for _, w := range res.Writes {
			if w.Hide {
				continue
			}
			label := w.Label
			if label == "" {
				label = fmt.Sprintf("**`%s`**", w.Path)
			}
			b.WriteString(label + "\n\n")
			b.WriteString(fence(fenceFor(w.Path), w.Content) + "\n")
		}

		if len(res.Cmds) > 0 {
			b.WriteString(renderCommands(res))
		}

		if res.Step.Caption != "" {
			b.WriteString(strings.TrimRight(res.Step.Caption, "\n") + "\n\n")
		}

		for _, pair := range res.Step.Identical {
			parts := strings.SplitN(pair, " == ", 2)
			fmt.Fprintf(&b, "`%s` and `%s` are now **byte for byte identical** — asserted by the generator, not by this sentence.\n\n", parts[0], parts[1])
		}

		for _, name := range res.Step.Watch {
			before, after := res.Before[name], res.After[name]
			fmt.Fprintf(&b, "**`%s`** — after\n\n", name)
			b.WriteString(fence(fenceFor(name), after) + "\n")
			if ud := unifiedDiff(name+" (before)", name+" (after)", before, after, 3); ud != "" {
				fmt.Fprintf(&b, "As an ordinary unified diff — what the apply amounted to, line by line:\n\n")
				b.WriteString(fence("diff", ud) + "\n")
			}
		}
		for _, name := range res.Step.Show {
			fmt.Fprintf(&b, "**`%s`**\n\n", name)
			b.WriteString(fence(fenceFor(name), res.After[name]) + "\n")
		}
		for _, name := range res.Step.Unchanged {
			fmt.Fprintf(&b, "**`%s`** — byte for byte what it was before the command:\n\n", name)
			b.WriteString(fence(fenceFor(name), res.After[name]) + "\n")
		}
	}

	if sc.Outro != "" {
		b.WriteString(strings.TrimRight(sc.Outro, "\n") + "\n")
	}
	return b.String()
}

// renderCommands writes the console block — each command, then its real stdout
// and stderr, as a terminal would show them — followed by the exit code of the
// step's subject, which is half of what hew promises.
func renderCommands(res stepResult) string {
	var body strings.Builder
	for _, c := range res.Cmds {
		body.WriteString("$ " + shellJoin(c.Argv) + "\n")
		body.WriteString(c.Stdout)
		if c.Stdout != "" && !strings.HasSuffix(c.Stdout, "\n") {
			body.WriteString("\n")
		}
		body.WriteString(c.Stderr)
		if c.Stderr != "" && !strings.HasSuffix(c.Stderr, "\n") {
			body.WriteString("\n")
		}
	}
	out := fence("console", body.String())
	// The exit code line is about hew's three-state contract, so a step that
	// only ran git scaffolding does not get one — there is nothing to promise
	// about it.
	if len(res.Step.Run) == 0 {
		return out + "\n"
	}
	subject, _ := res.subject()
	if subject.Stdout == "" && subject.Stderr == "" {
		return out + fmt.Sprintf("\nExit code `%d` — no output.\n\n", subject.ExitCode)
	}
	return out + fmt.Sprintf("\nExit code `%d`.\n\n", subject.ExitCode)
}

// shellJoin renders argv the way a person would have typed it, quoting only
// the arguments that need it. The transcript is a terminal session and should
// be copy-pasteable as one.
func shellJoin(argv []string) string {
	parts := make([]string, len(argv))
	for i, a := range argv {
		if a != "" && !strings.ContainsAny(a, " \t\n'\"\\$`&|;<>()*?#") {
			parts[i] = a
			continue
		}
		parts[i] = "'" + strings.ReplaceAll(a, "'", `'\''`) + "'"
	}
	return strings.Join(parts, " ")
}

// renderIndex writes the Examples landing page: one entry per scenario, in
// sidebar order.
func renderIndex(exs []*execution) string {
	var b strings.Builder
	b.WriteString("---\n")
	b.WriteString("title: Examples\n")
	b.WriteString("description: Executable hew scenarios — every transcript on these pages is generated by running the CLI.\n")
	b.WriteString("sidebar:\n  order: 0\n")
	b.WriteString("---\n\n")
	b.WriteString("These pages are not written. They are *run*.\n\n")
	b.WriteString("Each scenario is a directory of fixture files and an ordered list of commands in\n")
	b.WriteString("[`examples/`](https://github.com/benjaminabbitt/hew/tree/main/examples). Building this site executes them against a real\n")
	b.WriteString("`hew` binary in a scratch copy and renders what actually happened — commands, stdout,\n")
	b.WriteString("stderr, exit codes, and the resulting files. A transcript that claims hew does\n")
	b.WriteString("something it no longer does cannot be published: the generator fails when a step's\n")
	b.WriteString("exit code disagrees with the scenario.\n\n")
	for _, ex := range exs {
		sc := ex.Scenario
		fmt.Fprintf(&b, "### [%s](./%s/)\n\n%s\n\n", sc.Title, sc.Name, sc.Description)
	}
	return b.String()
}

// yamlString quotes a frontmatter scalar. Titles and descriptions are prose
// and prose contains colons.
func yamlString(s string) string {
	return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(s) + `"`
}

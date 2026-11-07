package repos

import (
	"fmt"
	"io"
	"path/filepath"
	"strings"
)

// Usage is the `repo` command's help, for a tool's usage text.
func Usage(tool string) string {
	return strings.ReplaceAll(`  TOOL repo add <name> <remote> [--path <p>] [--branch <b>]
                                          register a repo
  TOOL repo list                          list repos and their local paths
  TOOL repo path <name> <path>            set a repo's local path
  TOOL repo doctor [--clone] [--into <dir>]
                                          check repos; --clone fetches missing ones
`, "TOOL", tool)
}

// Run handles a tool's `repo` command and returns a process exit code.
func Run(root, tool string, args []string, stdout, stderr io.Writer) int {
	fail := func(err error) int {
		fmt.Fprintln(stderr, err)
		return 1
	}
	usage := func() int {
		fmt.Fprintf(stderr, "usage:\n%s", Usage(tool))
		return 1
	}
	if len(args) == 0 {
		return usage()
	}
	r, err := Load(root, tool)
	if err != nil {
		return fail(err)
	}
	verb, args := args[0], args[1:]
	switch verb {
	case "add":
		var pos []string
		var path, branch string
		for i := 0; i < len(args); i++ {
			switch a := args[i]; {
			case a == "--path" && i+1 < len(args):
				i++
				path = args[i]
			case a == "--branch" && i+1 < len(args):
				i++
				branch = args[i]
			case strings.HasPrefix(a, "--"):
				return usage()
			default:
				pos = append(pos, a)
			}
		}
		if len(pos) != 2 {
			return usage()
		}
		if err := r.Add(pos[0], pos[1], branch); err != nil {
			return fail(err)
		}
		if path != "" {
			if err := r.SetPath(pos[0], path); err != nil {
				return fail(err)
			}
		}
		if err := r.Save(); err != nil {
			return fail(err)
		}
		fmt.Fprintf(stdout, "added %s — commit REPOS.json to share it\n", pos[0])
		return 0
	case "list":
		for _, name := range r.Names() {
			path, ok := r.Paths[name]
			if !ok {
				path = "(no local path)"
			}
			repo := r.Repos[name]
			fmt.Fprintf(stdout, "%s  %s  %s  %s\n", name, repo.Remote, repo.DefaultBranch, path)
		}
		return 0
	case "path":
		if len(args) != 2 {
			return usage()
		}
		if err := r.SetPath(args[0], args[1]); err != nil {
			return fail(err)
		}
		if err := r.Save(); err != nil {
			return fail(err)
		}
		fmt.Fprintf(stdout, "%s: %s\n", args[0], r.Paths[args[0]])
		return 0
	case "doctor":
		return runDoctor(&r, tool, args, stdout, stderr)
	}
	return usage()
}

func runDoctor(r *Registry, tool string, args []string, stdout, stderr io.Writer) int {
	clone := false
	into := ""
	for i := 0; i < len(args); i++ {
		switch a := args[i]; {
		case a == "--clone":
			clone = true
		case a == "--into" && i+1 < len(args):
			i++
			into = args[i]
		default:
			fmt.Fprintf(stderr, "usage:\n%s", Usage(tool))
			return 1
		}
	}
	if clone {
		for _, name := range r.Names() {
			dest, ok := r.Paths[name]
			if path, err := r.Resolve(name); err == nil && isDir(path) {
				continue
			}
			if !ok {
				if into == "" {
					fmt.Fprintf(stderr, "%s: no local path — pass --into <dir> or run `%s repo path %s <path>`\n", name, tool, name)
					continue
				}
				dest = filepath.Join(into, name)
			}
			if err := r.Clone(name, dest); err != nil {
				fmt.Fprintln(stderr, err)
				continue
			}
			if err := r.Save(); err != nil {
				fmt.Fprintln(stderr, err)
				return 1
			}
			fmt.Fprintf(stdout, "cloned %s to %s\n", name, r.Paths[name])
		}
	}
	problems := r.Doctor()
	for _, p := range problems {
		fmt.Fprintf(stdout, "✗ %s: %s\n", p.Repo, p.Detail)
	}
	if len(problems) > 0 {
		return 1
	}
	fmt.Fprintf(stdout, "%d repos ok\n", len(r.Repos))
	return 0
}

// RunEnv handles a tool's `env` command for the named repos, printing harness
// flags on stdout, and returns a process exit code.
func RunEnv(root, tool string, names []string, stdout, stderr io.Writer) int {
	r, err := Load(root, tool)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	flags, err := r.EnvFlags(names)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	if flags != "" {
		fmt.Fprintln(stdout, flags)
	}
	return 0
}

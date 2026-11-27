package cicli

import (
	"context"
	"flag"
	"fmt"
	"io"
	"sort"
	"strings"
	"time"

	"api-audit-explorer/internal/localci"
)

// cmdClaims lists what the PCs that watch a repository have claimed, and the PCs themselves. It changes nothing
// unless --clean is given, which removes the finished claims that are older than a week.
func cmdClaims(ctx context.Context, args []string, stdout, stderr io.Writer, environ []string) int {
	fs := flag.NewFlagSet("claims", flag.ContinueOnError)
	fs.SetOutput(stderr)
	repo := fs.String("repo", defaultRepo, "owner/name of the repository")
	clean := fs.Bool("clean", false, "also remove the finished claims older than a week")
	if pos, err := parseInterspersed(fs, args); err != nil || len(pos) != 0 {
		if err == nil {
			_, _ = fmt.Fprintln(stderr, "claims: takes no arguments")
		}
		return exitUsage
	}
	if !strings.Contains(*repo, "/") {
		_, _ = fmt.Fprintf(stderr, "claims: --repo wants owner/name (got %q)\n", *repo)
		return exitUsage
	}
	redact := newRedactor(secrets(environ))
	host := newClaimHost(environ)
	claims, err := host.ListRefs(ctx, *repo, localci.RefNSClaim, "")
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "claims:", redact(err.Error()))
		return exitUsage
	}
	agents, err := host.ListRefs(ctx, *repo, localci.RefNSAgent, "")
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "claims:", redact(err.Error()))
		return exitUsage
	}
	sort.Slice(claims, func(i, j int) bool { return claims[i].Name < claims[j].Name })
	sort.Slice(agents, func(i, j int) bool { return agents[i].Name < agents[j].Name })
	_, _ = fmt.Fprintf(stdout, "%d PC(s) have written a record to %s:\n", len(agents), *repo)
	for _, a := range agents {
		_, _ = fmt.Fprintf(stdout, "  %-12s %-9s heard %s ago\n", a.Name, a.Rec.State, a.Age.Round(time.Second))
	}
	_, _ = fmt.Fprintf(stdout, "%d claim(s):\n", len(claims))
	for _, c := range claims {
		_, _ = fmt.Fprintf(stdout, "  %-12s %-6s %-9s %s (written %s ago)\n", c.Rec.Agent, c.Rec.State, c.Rec.Conclusion, c.Name, c.Age.Round(time.Second))
	}
	if *clean {
		f := &localci.Fleet{Host: host, Repo: *repo, Log: func(format string, a ...any) {
			_, _ = fmt.Fprintf(stderr, format+"\n", a...)
		}}
		f.CleanClaims(ctx)
		_, _ = fmt.Fprintln(stdout, "finished claims older than a week were removed")
	}
	return exitOK
}

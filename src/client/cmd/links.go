package cmd

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"strings"

	clientconfig "github.com/apimgr/shortner/src/client/config"
	"github.com/apimgr/shortner/src/common/display"
)

// runShorten creates a short link. The destination comes from the first
// argument, from --url, or from stdin when the client is used in a pipe,
// following AI.md PART 32's smart-input detection order.
func (r *runner) runShorten(ctx context.Context, args []string) int {
	if err := r.requireServer(); err != nil {
		return r.fail(err)
	}

	destination := r.flags.URL
	for _, arg := range args {
		if arg == "shorten" || arg == "create" || arg == "new" || arg == "add" {
			continue
		}
		if destination == "" {
			destination = arg
		}
	}
	if destination == "" {
		read, err := readStdin(r.io.In)
		if err != nil {
			return r.fail(err)
		}
		destination = read
	}
	if destination == "" {
		r.printer.Error("no URL given: pass a URL, --url URL, or pipe one on stdin")
		return ExitUsage
	}
	if !looksLikeURL(destination) {
		r.printer.Error("%q is not an http(s) URL", destination)
		return ExitUsage
	}

	created, err := r.client.CreateLink(ctx, destination, r.flags.Slug, r.flags.Expire)
	if err != nil {
		return r.fail(err)
	}
	if err := r.printer.PrintCreatedLink(created); err != nil {
		return r.fail(err)
	}
	return ExitOK
}

// runGet fetches one link.
func (r *runner) runGet(ctx context.Context, args []string) int {
	if err := r.requireServer(); err != nil {
		return r.fail(err)
	}
	slug := r.selectSlug(args)
	if slug == "" {
		r.printer.Error("no short code given: %s get SLUG", r.binaryName)
		return ExitUsage
	}

	link, err := r.client.GetLink(ctx, slug)
	if err != nil {
		return r.fail(err)
	}
	if err := r.printer.PrintLink(link); err != nil {
		return r.fail(err)
	}
	return ExitOK
}

// runList fetches a page of the public listing.
func (r *runner) runList(ctx context.Context) int {
	if err := r.requireServer(); err != nil {
		return r.fail(err)
	}
	page := clientconfig.ParseInt(r.flags.Page, 1)
	limit := clientconfig.ParseInt(r.flags.Limit, r.cfg.Defaults.Limit)

	list, err := r.client.ListLinks(ctx, page, limit)
	if err != nil {
		return r.fail(err)
	}
	if err := r.printer.PrintLinks(list); err != nil {
		return r.fail(err)
	}
	return ExitOK
}

// runUpdateLink changes a link's destination or expiration. It requires the
// link's owner token or the operator token.
func (r *runner) runUpdateLink(ctx context.Context, args []string) int {
	if err := r.requireServer(); err != nil {
		return r.fail(err)
	}
	slug := r.selectSlug(args)
	if slug == "" {
		r.printer.Error("no short code given: %s update SLUG --url URL", r.binaryName)
		return ExitUsage
	}
	if r.flags.URL == "" && r.flags.Expire == "" {
		r.printer.Error("nothing to update: pass --url URL or --expire WHEN")
		return ExitUsage
	}

	link, err := r.client.UpdateLink(ctx, slug, r.flags.URL, r.flags.Expire)
	if err != nil {
		return r.fail(err)
	}
	if err := r.printer.PrintLink(link); err != nil {
		return r.fail(err)
	}
	return ExitOK
}

// runDelete removes a link, confirming first unless --force was given.
func (r *runner) runDelete(ctx context.Context, args []string) int {
	if err := r.requireServer(); err != nil {
		return r.fail(err)
	}
	slug := r.selectSlug(args)
	if slug == "" {
		r.printer.Error("no short code given: %s delete SLUG", r.binaryName)
		return ExitUsage
	}

	if !r.flags.Force {
		env := display.DetectDisplayEnv()
		if !env.IsTerminal {
			r.printer.Error("refusing to delete %s without a terminal: pass --force", slug)
			return ExitUsage
		}
		fmt.Fprintf(r.io.Out, "Delete %s permanently? [y/N] ", slug)
		if !confirmed(r.io.In) {
			r.printer.Message("Aborted.")
			return ExitOK
		}
	}

	if err := r.client.DeleteLink(ctx, slug); err != nil {
		return r.fail(err)
	}
	r.printer.Message("Deleted %s", slug)
	return ExitOK
}

// runStats fetches a link's click analytics.
func (r *runner) runStats(ctx context.Context, args []string) int {
	if err := r.requireServer(); err != nil {
		return r.fail(err)
	}
	slug := r.selectSlug(args)
	if slug == "" {
		r.printer.Error("no short code given: %s stats SLUG", r.binaryName)
		return ExitUsage
	}

	stats, err := r.client.GetStats(ctx, slug)
	if err != nil {
		return r.fail(err)
	}
	if err := r.printer.PrintStats(stats); err != nil {
		return r.fail(err)
	}
	return ExitOK
}

// runHealth fetches the server's health document.
func (r *runner) runHealth(ctx context.Context) int {
	if err := r.requireServer(); err != nil {
		return r.fail(err)
	}
	health, err := r.client.Health(ctx)
	if err != nil {
		return r.fail(err)
	}
	if err := r.printer.PrintHealth(health); err != nil {
		return r.fail(err)
	}
	return ExitOK
}

// selectSlug picks the short code from --slug or the first positional
// argument.
func (r *runner) selectSlug(args []string) string {
	if r.flags.Slug != "" {
		return r.flags.Slug
	}
	for _, arg := range args {
		if arg != "" {
			return arg
		}
	}
	return ""
}

// readStdin reads a piped destination URL. A terminal stdin yields an empty
// string rather than blocking on input that will never arrive.
func readStdin(in io.Reader) (string, error) {
	file, ok := in.(*os.File)
	if ok {
		info, err := file.Stat()
		if err != nil {
			return "", err
		}
		if info.Mode()&os.ModeCharDevice != 0 {
			return "", nil
		}
	}

	data, err := io.ReadAll(io.LimitReader(in, 64<<10))
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(data)), nil
}

// confirmed reads a yes/no answer, defaulting to no.
func confirmed(in io.Reader) bool {
	reader := bufio.NewReader(in)
	line, err := reader.ReadString('\n')
	if err != nil && line == "" {
		return false
	}
	return clientconfig.IsTruthy(strings.TrimSpace(line))
}

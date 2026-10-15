package skill

import (
	"strings"
	"testing"
)

// A Windows checkout with core.autocrlf embeds SKILL.md with \r\n. The CI
// Windows job found that Body then kept the frontmatter.
func TestCRLFTemplateRendersLikeLF(t *testing.T) {
	t.Parallel()

	crlf := strings.ReplaceAll(template, "\n", "\r\n")

	if got := render(crlf, "1.2.3"); got != Render("1.2.3") {
		t.Error("a CRLF template renders differently from the LF one")
	}

	got := body(render(crlf, "1.2.3"))
	if !strings.HasPrefix(got, "# aigw\n") || strings.Contains(got, "\r") || strings.Contains(got, marker) {
		t.Errorf("body of a CRLF template starts %q", got[:min(len(got), 40)])
	}
}

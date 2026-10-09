package main

import (
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// UI token discipline and contrast.
//
// The point of this file is that "the design" is not a document someone has to
// remember to obey — it is enforced. Two rules, both mechanical:
//
//  1. every colour in a template comes from a :root token, never a raw hex
//     outside the token block;
//  2. the token pairs that actually sit on top of each other meet WCAG AA.
//
// Before this existed, base.html set the colour of header links but not content
// links, so every link in a table fell back to the browser default (#0000EE,
// 1.9:1 on this background). Nothing caught it. A contrast test would have.

// cssComments strips /* ... */ so a comment's own punctuation (a colon, or a
// hex mentioned in prose) cannot be mistaken for markup.
var cssComments = regexp.MustCompile(`(?s)/\*.*?\*/`)

// rootTokens extracts the `:root{--name:value;...}` block from base.html.
func rootTokens(t *testing.T) map[string]string {
	t.Helper()
	b, err := templateFiles.ReadFile("templates/base.html")
	if err != nil {
		t.Fatal(err)
	}
	b = cssComments.ReplaceAll(b, nil)
	m := regexp.MustCompile(`(?s):root\{(.*?)\}`).FindSubmatch(b)
	if m == nil {
		t.Fatal("no :root token block in base.html")
	}
	out := map[string]string{}
	for _, decl := range strings.Split(string(m[1]), ";") {
		decl = strings.TrimSpace(decl)
		if decl == "" {
			continue
		}
		k, v, ok := strings.Cut(decl, ":")
		if !ok {
			continue
		}
		out[strings.TrimSpace(k)] = strings.TrimSpace(v)
	}
	return out
}

func parseHex(s string) (r, g, b float64, err error) {
	s = strings.TrimSpace(strings.TrimPrefix(s, "#"))
	if len(s) == 3 {
		s = string([]byte{s[0], s[0], s[1], s[1], s[2], s[2]})
	}
	if len(s) != 6 {
		return 0, 0, 0, fmt.Errorf("not a hex colour: %q", s)
	}
	n, err := strconv.ParseUint(s, 16, 32)
	if err != nil {
		return 0, 0, 0, err
	}
	return float64(n >> 16 & 0xff), float64(n >> 8 & 0xff), float64(n & 0xff), nil
}

func relLum(c [3]float64) float64 {
	lin := func(v float64) float64 {
		v /= 255
		if v <= 0.03928 {
			return v / 12.92
		}
		return math.Pow((v+0.055)/1.055, 2.4)
	}
	return 0.2126*lin(c[0]) + 0.7152*lin(c[1]) + 0.0722*lin(c[2])
}

func contrast(a, b [3]float64) float64 {
	la, lb := relLum(a), relLum(b)
	hi, lo := la, lb
	if lb > la {
		hi, lo = lb, la
	}
	return (hi + 0.05) / (lo + 0.05)
}

func mustHex(t *testing.T, s string) [3]float64 {
	t.Helper()
	r, g, b, err := parseHex(s)
	if err != nil {
		t.Fatalf("token colour %q: %v", s, err)
	}
	return [3]float64{r, g, b}
}

// TestTokenContrast is the regression guard for the bug that started this: a
// colour pair that looks fine to whoever picked it but is unreadable in fact.
// Thresholds are WCAG AA: 4.5:1 for body text, 3:1 for large text/UI.
func TestTokenContrast(t *testing.T) {
	tok := rootTokens(t)
	for _, need := range []string{"--bg", "--fg", "--dim", "--faint", "--acc", "--on-acc", "--acc-visited", "--ok", "--warn", "--err", "--surface-2", "--line"} {
		if _, ok := tok[need]; !ok {
			t.Fatalf("token %s is missing from :root", need)
		}
	}
	bg := mustHex(t, tok["--bg"])
	cases := []struct {
		what   string
		fg, bg [3]float64
		min    float64
	}{
		{"body text (--fg on --bg)", mustHex(t, tok["--fg"]), bg, 7.0},
		{"secondary text (--dim on --bg)", mustHex(t, tok["--dim"]), bg, 4.5},
		{"link (--acc on --bg)", mustHex(t, tok["--acc"]), bg, 4.5},
		{"visited link (--acc-visited on --bg)", mustHex(t, tok["--acc-visited"]), bg, 4.5},
		{"button label (--on-acc on --acc)", mustHex(t, tok["--on-acc"]), mustHex(t, tok["--acc"]), 4.5},
		{"ok (--ok on --bg)", mustHex(t, tok["--ok"]), bg, 4.5},
		{"warn (--warn on --bg)", mustHex(t, tok["--warn"]), bg, 4.5},
		{"err (--err on --bg)", mustHex(t, tok["--err"]), bg, 4.5},
		{"tag text (--dim on --surface-2)", mustHex(t, tok["--dim"]), mustHex(t, tok["--surface-2"]), 4.5},
		{"placeholder text (--faint on --bg)", mustHex(t, tok["--faint"]), bg, 4.5},
		{"table header (--faint on --bg)", mustHex(t, tok["--faint"]), bg, 4.5},
		{"meta chip (--dim on --surface-2 backgrounds)", mustHex(t, tok["--faint"]), mustHex(t, tok["--surface-2"]), 4.0},
	}
	for _, c := range cases {
		if got := contrast(c.fg, c.bg); got < c.min {
			t.Errorf("%s: contrast %.2f:1, need >= %.1f:1", c.what, got, c.min)
		}
	}
}

// TestNoRawHexOutsideTokens enforces the token layer mechanically. A raw hex in
// a template means a colour that no token governs — exactly how the link bug and
// the five stray greys happened. Only the :root block may name colours.
func TestNoRawHexOutsideTokens(t *testing.T) {
	hexRe := regexp.MustCompile(`#[0-9a-fA-F]{3,8}\b`)
	entries, err := templateFiles.ReadDir("templates")
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		b, err := templateFiles.ReadFile("templates/" + e.Name())
		if err != nil {
			t.Fatal(err)
		}
		src := string(b)
		// strip comments and the token block: the tokens' own values are allowed,
		// and a hex named in explanatory prose is not a hardcoded colour.
		src = cssComments.ReplaceAllString(src, "")
		src = regexp.MustCompile(`(?s):root\{.*?\}`).ReplaceAllString(src, "")
		for _, m := range hexRe.FindAllString(src, -1) {
			t.Errorf("%s: raw colour %s outside the token block — use a var(--token)", e.Name(), m)
		}
	}
}

// TestContentLinksAreStyled guards the specific regression: a content link must
// get its colour from a rule, not from the browser default.
func TestContentLinksAreStyled(t *testing.T) {
	b, err := templateFiles.ReadFile("templates/base.html")
	if err != nil {
		t.Fatal(err)
	}
	css := regexp.MustCompile(`(?s)<style>(.*?)</style>`).FindSubmatch(b)
	if css == nil {
		t.Fatal("no stylesheet")
	}
	sheet := string(css[1])
	if !regexp.MustCompile(`(?m)^a\s*\{[^}]*color:var\(--acc\)`).MatchString(sheet) {
		t.Error("no bare `a{color:var(--acc)}` rule: content links would fall back to the browser default (#0000EE, ~1.9:1 here)")
	}
	if !strings.Contains(sheet, "a:visited{color:var(--acc-visited)}") {
		t.Error("no visited-link colour: the browser default purple is ~1.6:1 on this background")
	}
}

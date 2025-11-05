package dom

import (
	"context"
	"fmt"
	"strings"

	"github.com/playwright-community/playwright-go"
)

type Candidate struct {
	Selector string
	Tag      string
	Role     string
	Text     string
	Desc     string
	BBox     string
	Href     string
	Selected bool
	Region   string
}

// Собираем кликабельные/вводимые элементы.
func CollectCandidates(ctx context.Context, page playwright.Page, limit int) ([]Candidate, error) {
	sel := strings.Join([]string{
		"button, a, input, textarea, select, li",
		"[role=button],[role=link],[role=menuitem],[role=option],[role=radio],[role=checkbox],[role=combobox],[role=textbox],[role=listitem],[role=treeitem],[role=tab]",
		"[data-qa],[data-testid],[data-test]",
	}, ", ")

	elems, err := page.QuerySelectorAll(sel)
	if err != nil {
		return nil, err
	}

	out := make([]Candidate, 0, limit)
	seen := make(map[string]struct{})

	for _, e := range elems {
		vis, _ := e.IsVisible()
		if !vis {
			continue
		}

		tag, _ := e.Evaluate("(el)=>el.tagName.toLowerCase()")
		tagStr := strings.ToLower(strings.TrimSpace(fmt.Sprint(tag)))

		id, _ := e.GetAttribute("id")
		typ, _ := e.GetAttribute("type")
		role, _ := e.GetAttribute("role")
		aria, _ := e.GetAttribute("aria-label")
		ph, _ := e.GetAttribute("placeholder")
		href, _ := e.GetAttribute("href")
		dq, _ := e.GetAttribute("data-qa")
		dtid, _ := e.GetAttribute("data-testid")
		dtest, _ := e.GetAttribute("data-test")

		cls, _ := e.GetAttribute("class")
		ariaSel, _ := e.GetAttribute("aria-selected")
		ariaCur, _ := e.GetAttribute("aria-current")

		selected := strings.EqualFold(ariaSel, "true") ||
			strings.EqualFold(ariaCur, "page") ||
			strings.Contains(strings.ToLower(cls), "selected") ||
			strings.Contains(strings.ToLower(cls), "active") ||
			strings.Contains(strings.ToLower(cls), "current")

		if selected && (tagStr == "a" || role == "link" || role == "menuitem" || role == "tab") {
			continue
		}

		txt, _ := e.InnerText()
		if strings.TrimSpace(txt) == "" {
			txt, _ = e.TextContent()
		}
		txtNorm := strings.ReplaceAll(txt, "\u00a0", " ")

		if role == "" {
			role = guessRole(tagStr, typ, href)
		}

		box, _ := e.BoundingBox()
		bbox := ""
		if box != nil {
			bbox = fmt.Sprintf("%.0f,%.0f,%.0f,%.0f", box.X, box.Y, box.Width, box.Height)
		}

		cls, _ = e.GetAttribute("class")
		ariaSel, _ = e.GetAttribute("aria-selected")
		ariaCur, _ = e.GetAttribute("aria-current")

		selected = strings.EqualFold(ariaSel, "true") ||
			strings.EqualFold(ariaCur, "page") ||
			strings.Contains(strings.ToLower(cls), "selected") ||
			strings.Contains(strings.ToLower(cls), "active") ||
			strings.Contains(strings.ToLower(cls), "current")

		if selected && (tagStr == "a" || role == "link" || role == "menuitem" || role == "tab") {
			continue
		}

		var s string
		base := tagOrInput(tagStr)

		switch {
		case id != "":
			s = fmt.Sprintf(`[id=%q]`, id)

		case dq != "":
			s = fmt.Sprintf(`[data-qa=%q]`, dq)

		case dtid != "":
			s = fmt.Sprintf(`[data-testid=%q]`, dtid)

		case dtest != "":
			s = fmt.Sprintf(`[data-test=%q]`, dtest)

		case href != "":
			trimmed := href
			if i := strings.IndexByte(trimmed, '#'); i >= 0 {
				trimmed = trimmed[:i]
			}
			s = fmt.Sprintf(`a[href*=%q]`, crop(trimmed, 40))

		case ph != "":
			s = fmt.Sprintf(`%s[placeholder*=%q]`, base, crop(ph, 40))

		case aria != "":
			s = fmt.Sprintf(`%s[aria-label*=%q]`, base, crop(aria, 40))

		default:
			short := strings.TrimSpace(crop(txtNorm, 60))
			if short != "" {
				s = fmt.Sprintf(`%s:has-text(%q)`, base, short)
			} else {
				s = awaitNth(base)
			}
		}

		s = strings.TrimSpace(s)
		if s == "" {
			continue
		}
		if _, ok := seen[s]; ok {
			continue
		}
		seen[s] = struct{}{}

		state := ""
		if selected {
			state = "state=selected"
		}

		desc := strings.TrimSpace(strings.Join([]string{
			"tag=" + tagStr,
			"role=" + role,
			ifNonEmpty("type", typ),
			ifNonEmpty("text", crop(txtNorm, 80)),
			ifNonEmpty("placeholder", ph),
			ifNonEmpty("href", crop(href, 80)),
			ifNonEmpty("data-qa", dq),
			ifNonEmpty("data-testid", dtid),
			ifNonEmpty("data-test", dtest),
			ifNonEmpty("state", state),
		}, "; "))

		out = append(out, Candidate{
			Selector: s,
			Tag:      tagStr,
			Role:     role,
			Text:     crop(txtNorm, 80),
			Desc:     desc,
			BBox:     bbox,
			Href:     href,
			Selected: selected,
		})

		if limit > 0 && len(out) >= limit {
			break
		}
	}

	return out, nil
}

func crop(s string, n int) string {
	s = strings.ReplaceAll(s, "\n", " ")
	if len([]rune(s)) <= n {
		return s
	}
	return string([]rune(s)[:n]) + "…"
}
func ifNonEmpty(k, v string) string {
	if strings.TrimSpace(v) == "" {
		return ""
	}
	return fmt.Sprintf("%s=%s", k, v)
}
func tagOrInput(tag string) string {
	if tag == "" {
		return "input,button,a,textarea,select"
	}
	return tag
}
func awaitNth(base string) string {
	return base
}
func guessRole(tag, typ, href string) string {
	if tag == "a" || href != "" {
		return "link"
	}
	if tag == "input" || tag == "textarea" {
		if typ == "submit" {
			return "button"
		}
		return "textbox"
	}
	if tag == "button" {
		return "button"
	}
	return ""
}

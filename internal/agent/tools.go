package agent

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/playwright-community/playwright-go"
)

// Tools — обёртка над страницей браузера для вызова действий агентом.
type Tools struct {
	Page playwright.Page
}

// normalizeSelector приводит селектор к валидному CSS:
// - вытаскивает последний селектор из конструкций вида `... sel="..."`
// - убирает модификатор чувствительности ` i` внутри атрибутных селекторов
// - заменяет #<digits> на [id="<digits>"]
// - снимает лишние экранирования кавычек
var (
	reSelQuoted  = regexp.MustCompile(`sel\s*=\s*"(.*?)"`)
	reAttrCaseI  = regexp.MustCompile(`(\[[^\]]+)\s+i\]`)
	reNumID      = regexp.MustCompile(`^#\d+$`)
	reBackslashQ = regexp.MustCompile(`\\+"`)
)

func normalizeSelector(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return s
	}
	// Если LLM прислал что-то вроде: '#16 sel="a[href*=\"#inbox\" i]"' — берём последний quoted-селектор
	if m := reSelQuoted.FindAllStringSubmatch(s, -1); len(m) > 0 {
		s = m[len(m)-1][1] // содержимое последних кавычек
	}
	// Снять лишние слэши перед кавычками (после JSON-логов они часто видны)
	s = strings.ReplaceAll(s, `\"`, `"`)
	s = reBackslashQ.ReplaceAllString(s, `"`)

	// Удалить CSS4-модификатор чувствительности
	s = reAttrCaseI.ReplaceAllString(s, `$1]`)

	// Невалидный #<digits> → [id="<digits>"]
	if reNumID.MatchString(s) {
		id := strings.TrimPrefix(s, "#")
		s = `[id="` + id + `"]`
	}
	return strings.TrimSpace(s)
}

// Call исполняет действие по имени и аргументам.
// Используется агентом, который решает какой шаг сделать.
func (t *Tools) Call(ctx context.Context, name string, args map[string]any) (string, error) {
	switch name {
	case "goto_url":
		url, _ := args["url"].(string)
		if url == "" {
			return "", errors.New("goto_url: empty url")
		}
		_, err := t.Page.Goto(url)
		return "navigated", err

	case "click":

		selector, _ := args["selector"].(string)
		selector = normalizeSelector(selector)
		if selector == "" {
			return "", errors.New("click: empty selector")
		}

		el, err := t.Page.QuerySelector(selector)
		if err != nil {
			return "", err
		}
		if el == nil {
			return "", fmt.Errorf("click: element not found: %s", selector)
		}
		if err := el.Click(); err != nil {
			return "", err
		}
		return "clicked selector=" + selector, nil
	case "open_first_main_item":
		elems, err := t.Page.QuerySelectorAll(`a, div, [role=listitem], [role=row], [role=treeitem], [role=option], [role=tab]`)
		if err != nil {
			return "", err
		}
		if len(elems) == 0 {
			return "", fmt.Errorf("open_first_main_item: no candidates")
		}

		vwAny, _ := t.Page.Evaluate("() => window.innerWidth || 1280")
		vw := 1280.0
		if f, ok := vwAny.(float64); ok {
			vw = f
		}

		leftLim := 0.25 * vw
		if leftLim > 280 {
			leftLim = 280
		}

		type item struct {
			el      playwright.ElementHandle
			y, w, h float64
		}
		best := item{y: 1e12}

		for _, e := range elems {
			vis, _ := e.IsVisible()
			if !vis {
				continue
			}

			box, _ := e.BoundingBox()
			if box == nil {
				continue
			}

			if box.X < leftLim || box.Width < 50 || box.Height < 20 || box.Y < 60 {
				continue
			}

			txt, _ := e.InnerText()
			low := strings.ToLower(txt)
			if strings.Contains(low, "уведомлен") || strings.Contains(low, "notification") {
				continue
			}

			if box.Y < best.y {
				best = item{el: e, y: box.Y, w: box.Width, h: box.Height}
			}
		}

		if best.el == nil {
			return "", fmt.Errorf("open_first_main_item: no element in main region")
		}

		if err := best.el.Click(); err != nil {
			return "", err
		}
		return "opened_first_main_item", nil

	case "type":
		selector, _ := args["selector"].(string)
		text, _ := args["text"].(string)
		pressEnter, _ := args["pressEnter"].(bool)

		selector = normalizeSelector(selector)
		if selector == "" {
			return "", errors.New("type: empty selector")
		}

		el, err := t.Page.QuerySelector(selector)
		if err != nil {
			return "", err
		}
		if el == nil {
			return "", fmt.Errorf("type: element not found: %s", selector)
		}
		if err := el.Fill(text); err != nil {
			return "", err
		}
		if pressEnter {
			if err := el.Press("Enter"); err != nil {
				return "", err
			}
		}
		return "typed", nil

	case "press":
		key, _ := args["key"].(string)
		if key == "" {
			key = "Escape"
		}
		return "pressed", t.Page.Keyboard().Press(key)

	case "scroll":
		if sel, ok := args["selector"].(string); ok && sel != "" {
			_, err := t.Page.Evaluate(`(sel)=>{document.querySelector(sel)?.scrollIntoView({behavior:'instant',block:'center'})}`, sel)
			if err != nil {
				return "", err
			}
			return "scrolled-to", nil
		}
		y := 600.0
		if v, ok := args["y"].(float64); ok && v != 0 {
			y = v
		}
		_, err := t.Page.Evaluate(`(dy)=>{window.scrollBy(0,dy)}`, y)
		return "scrolled", err

	case "extract":
		title, _ := t.Page.Title()
		body, _ := t.Page.TextContent("body")
		if len(body) > 6000 {
			body = body[:6000] + "…"
		}
		return fmt.Sprintf("TITLE: %s\nSNAPSHOT:\n%s", title, body), nil

	case "answer_or_ask_user":
		return "done", nil

	default:
		return "", errors.New("unknown tool: " + name)
	}
}

// WaitIdle — короткое ожидание тишины сети, чтобы дать SPA догрузиться после действия.
func WaitIdle(page playwright.Page) {
	_ = page.WaitForLoadState()
	time.Sleep(200 * time.Millisecond)
}

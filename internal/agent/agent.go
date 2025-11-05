// internal/agent/agent.go
package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"hash/fnv"
	"net/http"
	"os"
	"strings"
	"time"

	"AIAgent/internal/dom"
	"AIAgent/internal/memory"

	"github.com/playwright-community/playwright-go"
)

func Run(ctx context.Context, _ playwright.Browser, page playwright.Page, userTask string) error {
	mem := memory.New()
	tools := &Tools{Page: page}

	fmt.Println("\n[agent] Задача:", userTask)

	obs, _ := observe(ctx, page, 24)
	fmt.Printf("[agent] Текущая страница: %s | %s\n", obs.URL, obs.Title)

	lastURL := obs.URL
	lastHash := hashSnap(obs.Snapshot)
	noProgress := 0

	const maxSteps = 40
	for step := 1; step <= maxSteps; step++ {
		act, err := decide(ctx, userTask, obs, mem)
		if err != nil {
			return fmt.Errorf("ошибка планирования: %w", err)
		}

		fmt.Printf("\n[agent] Шаг %d\n", step)
		fmt.Printf("[agent] → инструмент: %s\n", act.Tool)
		if len(act.Args) != 0 {
			js, _ := json.Marshal(act.Args)
			fmt.Printf("[agent] → аргументы: %s\n", js)
		}
		if s := strings.TrimSpace(act.Comment); s != "" {
			fmt.Printf("[agent] → комментарий: %s\n", s)
		}

		res, err := tools.Call(ctx, act.Tool, act.Args)
		if err != nil {
			fmt.Printf("[agent] ⚠ ошибка инструмента: %v\n", err)
			mem.SetLastAction("error: " + err.Error())
		} else {
			mem.SetLastAction(act.Tool + ": " + res)
		}

		WaitIdle(page)

		newObs, _ := observe(ctx, page, 36)
		newURL := newObs.URL
		newHash := hashSnap(newObs.Snapshot)

		if newURL == lastURL {
			fmt.Println("[agent] URL не изменился")
		} else {
			fmt.Printf("[agent] URL изменился: %s → %s\n", lastURL, newURL)
		}
		if newHash == lastHash {
			fmt.Println("[agent] Контент почти не изменился (hash)")
			noProgress++
		} else {
			noProgress = 0
		}

		if act.Tool == "answer_or_ask_user" {
			if s := strings.TrimSpace(act.Comment); s != "" {
				fmt.Println("\n[agent] Ответ/уточнение:")
				fmt.Println(s)
			}
			return nil
		}

		if noProgress >= 2 {
			fmt.Println("[agent] Нет прогресса два шага подряд → принудительно open_first_main_item")
			if _, err := tools.Call(ctx, "open_first_main_item", map[string]any{}); err == nil {
				WaitIdle(page)
				forcedObs, _ := observe(ctx, page, 36)
				lastURL = forcedObs.URL
				lastHash = hashSnap(forcedObs.Snapshot)
				noProgress = 0
				obs = forcedObs
				continue
			}
			_, _ = tools.Call(ctx, "scroll", map[string]any{"y": 800})
			WaitIdle(page)
		}

		lastURL = newURL
		lastHash = newHash
		obs = newObs

		time.Sleep(150 * time.Millisecond)
	}

	return errors.New("достигнут лимит шагов")
}

type Observation struct {
	Title      string
	URL        string
	Snapshot   string
	Candidates []dom.Candidate
}

func observe(ctx context.Context, page playwright.Page, maxCandidates int) (Observation, error) {
	title, _ := page.Title()
	url := page.URL()
	body, _ := page.TextContent("body")
	if len(body) > 3000 {
		body = body[:3000] + "…"
	}
	cands, _ := dom.CollectCandidates(ctx, page, maxCandidates)
	return Observation{
		Title:      title,
		URL:        url,
		Snapshot:   safeTrim(body),
		Candidates: cands,
	}, nil
}

func fnv32(s string) uint32 {
	h := fnv.New32a()
	_, _ = h.Write([]byte(s))
	return h.Sum32()
}

func hashSnap(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 512 {
		s = s[:512]
	}
	return fmt.Sprintf("%d|%08x", len(s), fnv32(s))
}

func safeTrim(s string) string {
	s = strings.ReplaceAll(s, "\u00a0", " ")
	s = strings.TrimSpace(s)
	return s
}

type llmAction struct {
	Tool    string         `json:"tool"`
	Args    map[string]any `json:"args"`
	Comment string         `json:"comment,omitempty"`
}

func decide(ctx context.Context, task string, obs Observation, mem *memory.Memory) (llmAction, error) {
	if apiKey := os.Getenv("OPENAI_API_KEY"); strings.TrimSpace(apiKey) != "" {
		return decideWithOpenAI(ctx, apiKey, task, obs, mem.LastAction())
	}
	return simpleHeuristicDecision(task, obs, mem), nil
}

const systemPrompt = `
You are a web-automation AI agent that controls a real browser page.
You must choose EXACTLY ONE next tool call in JSON, no extra text.
Pick selectors ONLY from the provided candidates.
Prefer stable selectors: #id, [data-qa], a[href*=... ], placeholders/aria-labels. Avoid raw text unless necessary.
If user task requires reading emails and classifying spam, you must navigate the mailbox UI, open Inbox, read latest messages (subject/sender/preview), decide spam vs important, move spam to Trash/Spam, and then summarize to the user.
If you need user input (e.g., missing info or login), return tool=answer_or_ask_user with a short question.
If a navigation item like Inbox is already selected, do NOT click it again. Instead call open_first_main_item to open the newest message from the main content area.

Available tools:
- goto_url {url}
- click {selector}
- type {selector, text, pressEnter?}
- press {key}
- scroll {y? or selector?}
- extract {}
- open_first_main_item {}


Return strictly:
{"tool":"...", "args":{...}, "comment":"...optional human-readable note..."}
`

func decideWithOpenAI(ctx context.Context, apiKey string, task string, obs Observation, lastAction string) (llmAction, error) {
	// Собираем компактное представление кандидатов
	var b strings.Builder
	for i, c := range obs.Candidates {
		fmt.Fprintf(&b, "- #%d sel=%q | %s\n", i+1, c.Selector, c.Desc)
		if i >= 80 {
			break
		}
	}

	userPrompt := map[string]any{
		"task":          task,
		"page":          map[string]string{"url": obs.URL, "title": obs.Title},
		"last_action":   lastAction,
		"candidates":    b.String(),
		"page_snapshot": obs_snapshot(obs),
	}
	uj, _ := json.Marshal(userPrompt)

	body := map[string]any{
		"model": "gpt-4o",
		"messages": []map[string]string{
			{"role": "system", "content": systemPrompt},
			{"role": "user", "content": string(uj)},
		},
		"response_format": map[string]string{"type": "json_object"},
		"temperature":     0.2,
	}
	reqBytes, _ := json.Marshal(body)

	httpReq, _ := http.NewRequestWithContext(ctx, "POST", "https://api.openai.com/v1/chat/completions", bytes.NewReader(reqBytes))
	httpReq.Header.Set("Authorization", "Bearer "+apiKey)
	httpReq.Header.Set("Content-Type", "application/json")

	httpCli := &http.Client{Timeout: 45 * time.Second}
	resp, err := httpCli.Do(httpReq)
	if err != nil {
		return llmAction{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return llmAction{}, fmt.Errorf("openai http %d", resp.StatusCode)
	}

	var out struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return llmAction{}, err
	}
	if len(out.Choices) == 0 {
		return llmAction{}, errors.New("empty LLM response")
	}

	var act llmAction
	if err := json.Unmarshal([]byte(out.Choices[0].Message.Content), &act); err != nil {
		act = tryExtractJSON(out.Choices[0].Message.Content)
	}
	if act.Tool == "" {
		return llmAction{}, errors.New("LLM returned empty tool")
	}
	return act, nil
}

func obs_snapshot(obs Observation) string {
	// Короткий отрывок контента страницы для контекста
	if obs.Snapshot == "" {
		return ""
	}
	if len(obs.Snapshot) > 1000 {
		return obs.Snapshot[:1000] + "…"
	}
	return obs.Snapshot
}

func tryExtractJSON(s string) llmAction {
	var act llmAction
	// Наивно ищем первую { и последнюю }
	l := strings.Index(s, "{")
	r := strings.LastIndex(s, "}")
	if l >= 0 && r > l {
		_ = json.Unmarshal([]byte(s[l:r+1]), &act)
	}
	return act
}

func alreadyInInbox(obs Observation) bool {
	s := strings.ToLower(obs.Title + " " + obs.URL + " " + obs.Snapshot)
	return strings.Contains(s, "входящие") || strings.Contains(s, "inbox")
}

func simpleHeuristicDecision(task string, obs Observation, mem *memory.Memory) llmAction {
	lowTask := strings.ToLower(task)
	last := strings.ToLower(mem.LastAction())

	if alreadyInInbox(obs) {
		return llmAction{
			Tool:    "open_first_main_item",
			Args:    map[string]any{},
			Comment: "Открываю первое письмо из центрального списка",
		}
	}

	// Логин/вход
	if hasAny(lowTask, []string{"войти", "логин", "login", "sign in"}) ||
		strings.Contains(strings.ToLower(obs.Title), "логин") {
		if sel := findByContains(obs.Candidates, []string{"email", "login", "username", "почта", "телефон"}, "placeholder", "aria"); sel != "" {
			return llmAction{Tool: "type", Args: map[string]any{"selector": sel, "text": ""}, Comment: "Ожидаю логин от пользователя"}
		}
	}

	if hasAny(lowTask, []string{"почт", "mail", "email", "входящие", "яндекс"}) && !strings.Contains(strings.ToLower(obs.URL), "mail") {
		return llmAction{Tool: "goto_url", Args: map[string]any{"url": guessMailURL(lowTask)}, Comment: "Переход к почтовому сервису"}
	}

	if sel := findByText(obs.Candidates, []string{"Входящие", "Inbox"}); sel != "" && !strings.Contains(last, "inbox") {
		return llmAction{Tool: "click", Args: map[string]any{"selector": sel}, Comment: "Открываю Inbox"}
	}

	if strings.Contains(strings.ToLower(obs.Snapshot), "inbox") || strings.Contains(strings.ToLower(obs.Snapshot), "письм") {
		return llmAction{Tool: "scroll", Args: map[string]any{"y": 800.0}, Comment: "Прокрутка списка писем"}
	}

	return llmAction{
		Tool:    "answer_or_ask_user",
		Args:    map[string]any{},
		Comment: "Нужна доп. информация (вы уже авторизованы и открыт Inbox?).",
	}
}

func hasAny(s string, subs []string) bool {
	for _, sub := range subs {
		if strings.Contains(s, sub) {
			return true
		}
	}
	return false
}

func findByText(cands []dom.Candidate, texts []string) string {
	for _, c := range cands {
		t := strings.ToLower(c.Text + " " + c.Desc)
		for _, q := range texts {
			if strings.Contains(t, strings.ToLower(q)) {
				return c.Selector
			}
		}
	}
	return ""
}

func findByContains(cands []dom.Candidate, qs []string, fields ...string) string {
	for _, c := range cands {
		t := strings.ToLower(c.Desc + " " + c.Text)
		for _, q := range qs {
			if strings.Contains(t, strings.ToLower(q)) {
				return c.Selector
			}
		}
		_ = fields // (оставлено на будущее — разбирать placeholder/aria отдельно)
	}
	return ""
}

func guessMailURL(task string) string {
	if strings.Contains(task, "яндекс") || strings.Contains(task, "yandex") {
		return "https://mail.yandex.ru/"
	}
	if strings.Contains(task, "gmail") || strings.Contains(task, "google") {
		return "https://mail.google.com/mail/u/0/#inbox"
	}
	return "https://mail.yandex.ru/"
}

package main

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"strings"

	"AIAgent/internal/agent"
	"AIAgent/internal/browser"

	"github.com/playwright-community/playwright-go"
)

func main() {
	ctx := context.Background()

	pdir, err := browser.DefaultProfileDir("aiagent")
	if err != nil {
		panic(err)
	}
	pw, bctx, page, err := browser.LaunchPersistent(ctx, pdir, true)
	if err != nil {
		panic(err)
	}
	defer func() {
		_ = bctx.Close()
		_ = pw.Stop()
	}()

	fmt.Println("AI-браузер запущен. Опишите задачу (одной строкой).")
	reader := bufio.NewReader(os.Stdin)

	for {
		fmt.Print("\n> ")
		task, _ := reader.ReadString('\n')
		task = strings.TrimSpace(task)
		if task == "" {
			continue
		}
		if strings.EqualFold(task, "exit") || strings.EqualFold(task, "quit") {
			fmt.Println("Пока!")
			return
		}

		var br playwright.Browser = nil
		if err := agent.Run(ctx, br, page, task); err != nil {
			fmt.Println("Ошибка задачи:", err)
		}
	}
}

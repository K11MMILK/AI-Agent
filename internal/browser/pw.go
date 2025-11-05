package browser

import (
	"context"
	"os"
	"path/filepath"

	"github.com/playwright-community/playwright-go"
)

func LaunchPersistent(ctx context.Context, userDataDir string, headed bool) (*playwright.Playwright, playwright.BrowserContext, playwright.Page, error) {

	if err := os.MkdirAll(userDataDir, 0o755); err != nil {
		return nil, nil, nil, err
	}

	if err := playwright.Install(); err != nil {
	}
	pw, err := playwright.Run()
	if err != nil {
		return nil, nil, nil, err
	}

	ctxOpts := playwright.BrowserTypeLaunchPersistentContextOptions{
		Headless: playwright.Bool(!headed),
	}
	bctx, err := pw.Chromium.LaunchPersistentContext(userDataDir, ctxOpts)
	if err != nil {
		_ = pw.Stop()
		return nil, nil, nil, err
	}

	page, err := bctx.NewPage()
	if err != nil {
		_ = bctx.Close()
		_ = pw.Stop()
		return nil, nil, nil, err
	}

	return pw, bctx, page, nil
}

func DefaultProfileDir(appName string) (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, "."+appName, "profile"), nil
}

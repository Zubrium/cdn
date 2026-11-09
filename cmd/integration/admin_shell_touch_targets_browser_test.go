package integration

import (
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strings"
	"testing"
)

func TestAdminShellTouchTargetsBrowserAcceptance(t *testing.T) {
	node := firstNonEmpty(os.Getenv("ADMIN_UI_TEST_NODE"), os.Getenv("PRERENDER_TEST_NODE"))
	module := firstNonEmpty(os.Getenv("ADMIN_UI_TEST_MODULE"), os.Getenv("PRERENDER_TEST_MODULE"))
	browser := firstNonEmpty(os.Getenv("ADMIN_UI_TEST_BROWSER"), os.Getenv("PRERENDER_TEST_BROWSER"))
	distDir := strings.TrimSpace(os.Getenv("ADMIN_UI_TEST_DIST_DIR"))
	if node == "" || module == "" || browser == "" || distDir == "" {
		t.Skip("set ADMIN_UI_TEST_NODE/MODULE/BROWSER and ADMIN_UI_TEST_DIST_DIR for admin shell browser acceptance")
	}
	for label, path := range map[string]string{"Node.js": node, "Playwright": module, "Chromium": browser} {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("%s is unavailable at %q: %v", label, path, err)
		}
	}
	if _, err := os.Stat(filepath.Join(distDir, "admin.html")); err != nil {
		t.Fatalf("temporary admin build is unavailable at %q: %v", distDir, err)
	}

	mux := http.NewServeMux()
	mux.Handle("/admin/", http.StripPrefix("/admin/", adminTouchTargetStaticHandler(distDir)))
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)

	script := filepath.Join(t.TempDir(), "admin-shell-touch-targets.mjs")
	if err := os.WriteFile(script, []byte(adminShellTouchTargetsScript), 0600); err != nil {
		t.Fatalf("write browser acceptance script: %v", err)
	}
	command := exec.Command(node, script)
	command.Env = append(os.Environ(),
		"ADMIN_SHELL_TEST_URL="+server.URL+"/admin/",
		"ADMIN_SHELL_TEST_DIST_DIR="+distDir,
		"ADMIN_SHELL_PLAYWRIGHT_MODULE="+module,
		"ADMIN_SHELL_BROWSER="+browser,
		"ADMIN_SHELL_SCREENSHOT_DIR="+strings.TrimSpace(os.Getenv("ADMIN_UI_QA_OUTPUT_DIR")),
	)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("admin shell browser acceptance failed: %v\n%s", err, output)
	}
	for _, line := range strings.Split(strings.TrimSpace(string(output)), "\n") {
		if line != "" {
			t.Log(line)
		}
	}
}

func adminTouchTargetStaticHandler(distDir string) http.Handler {
	root := http.Dir(distDir)
	files := http.FileServer(root)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			w.Header().Set("Allow", "GET, HEAD")
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		clean := path.Clean("/" + r.URL.Path)
		if clean != "/" && !strings.HasPrefix(clean, "/../") {
			filePath := filepath.Join(distDir, filepath.FromSlash(strings.TrimPrefix(clean, "/")))
			if info, err := os.Stat(filePath); err == nil && !info.IsDir() {
				if strings.HasPrefix(clean, "/assets/") {
					w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
				}
				files.ServeHTTP(w, r)
				return
			}
			if strings.HasPrefix(clean, "/assets/") {
				http.NotFound(w, r)
				return
			}
		}
		data, err := os.ReadFile(filepath.Join(distDir, "admin.html"))
		if err != nil {
			http.Error(w, "admin build is unavailable", http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		w.WriteHeader(http.StatusOK)
		if r.Method != http.MethodHead {
			_, _ = w.Write(data)
		}
	})
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

const adminShellTouchTargetsScript = `import { pathToFileURL } from "node:url";
const { chromium } = await import(pathToFileURL(process.env.ADMIN_SHELL_PLAYWRIGHT_MODULE).href);
const browser = await chromium.launch({
  executablePath: process.env.ADMIN_SHELL_BROWSER,
  headless: true,
  args: ["--no-sandbox"],
});
const failures = [];
const assert = (condition, message) => { if (!condition) failures.push(message); };
const intersects = (a, b) => a.left < b.right && a.right > b.left && a.top < b.bottom && a.bottom > b.top;
const checkArea = (label, rect) => {
  assert(Boolean(rect), label + " is missing");
  if (rect) assert(rect.width >= 44 && rect.height >= 44, label + " is " + rect.width + "x" + rect.height + "px; expected at least 44x44");
};

try {
  const context = await browser.newContext({ viewport: { width: 1280, height: 900 }, colorScheme: "light" });
  const page = await context.newPage();
  const pageErrors = [];
  page.on("pageerror", error => pageErrors.push(error.message));
  page.on("console", message => { if (message.type() === "error") pageErrors.push(message.text()); });
  await page.route("**/api/**", async route => {
    const pathname = new URL(route.request().url()).pathname;
    if (pathname === "/api/v1/admin/me") {
      await route.fulfill({ status: 200, contentType: "application/json", body: JSON.stringify({ id: "admin-shell-qa", name: "Browser QA", role: "admin", created_at: "2026-01-01T00:00:00Z", expires_at: null }) });
      return;
    }
    if (pathname.endsWith("/overview")) {
      await route.fulfill({ status: 200, contentType: "application/json", body: JSON.stringify({ counts: { features_active: 0, features_archived: 0, draft_model_snapshots: 0, draft_model_records: 0, new_submissions: 0, media_ready: 0, failed_media: 0 }, attention: { draft_model_snapshots: [], new_submissions: [], failed_media: [] }, recent_activity: [] }) });
      return;
    }
    await route.fulfill({ status: 200, contentType: "application/json", body: JSON.stringify({ items: [] }) });
  });
  await page.addInitScript(() => {
    localStorage.setItem("we_admin_token", "disposable-admin-shell-browser-token");
    localStorage.setItem("we_admin_sidebar_collapsed", "true");
    localStorage.setItem("we_admin_theme", "light");
  });
  await page.goto(process.env.ADMIN_SHELL_TEST_URL, { waitUntil: "networkidle" });
  await page.locator('nav[aria-label="Основная навигация"]').waitFor();

  const measureDesktop = async (label, expectedWidth) => {
    const result = await page.evaluate(() => {
      const sidebar = document.querySelector("aside");
      const nav = document.querySelector('nav[aria-label="Основная навигация"]');
      const main = document.querySelector("main#main-content");
      const links = [...(nav?.querySelectorAll("a.sidebar-link") ?? [])].map(element => {
        const rect = element.getBoundingClientRect();
        return { label: element.textContent?.trim() ?? "", left: rect.left, right: rect.right, top: rect.top, bottom: rect.bottom, width: rect.width, height: rect.height };
      });
      const sidebarRect = sidebar?.getBoundingClientRect();
      const mainRect = main?.getBoundingClientRect();
      const box = selector => {
        const element = document.querySelector(selector);
        if (!element) return null;
        const rect = element.getBoundingClientRect();
        return { left: rect.left, right: rect.right, top: rect.top, bottom: rect.bottom, width: rect.width, height: rect.height };
      };
      return {
        sidebar: sidebarRect ? { left: sidebarRect.left, right: sidebarRect.right, width: sidebarRect.width } : null,
        main: mainRect ? { left: mainRect.left, right: mainRect.right } : null,
        navClientWidth: nav?.clientWidth ?? 0,
        navScrollWidth: nav?.scrollWidth ?? 0,
        headerClientWidth: sidebar?.querySelector(":scope > div")?.clientWidth ?? 0,
        headerScrollWidth: sidebar?.querySelector(":scope > div")?.scrollWidth ?? 0,
        links,
        brand: box('aside a[aria-label="ZUBRIUM — на главную"]'),
        collapse: box('button[aria-label="Развернуть навигацию"]'),
        theme: box('button[aria-label^="Переключить тему"]'),
        logout: box('button[aria-label="Выйти"]'),
        viewportWidth: window.innerWidth,
        documentWidth: document.documentElement.scrollWidth,
        bodyWidth: document.body.scrollWidth,
      };
    });
    assert(result.viewportWidth === expectedWidth, label + ": unexpected viewport " + result.viewportWidth);
    assert(Boolean(result.sidebar && result.main), label + ": sidebar or main landmark is missing");
    assert(result.links.length === 10, label + ": expected 10 admin navigation links, got " + result.links.length);
    for (const link of result.links) {
      checkArea(label + " navigation link " + link.label, link);
      assert(Boolean(result.sidebar && link.left >= result.sidebar.left && link.right <= result.sidebar.right), label + " navigation link " + link.label + " escapes sidebar");
    }
    checkArea(label + " collapsed brand link", result.brand);
    checkArea(label + " collapse button", result.collapse);
    checkArea(label + " theme button", result.theme);
    checkArea(label + " logout button", result.logout);
    for (const [name, control] of [["brand", result.brand], ["collapse", result.collapse], ["theme", result.theme], ["logout", result.logout]]) {
      assert(Boolean(result.sidebar && control && control.left >= result.sidebar.left && control.right <= result.sidebar.right), label + " " + name + " button escapes sidebar");
      assert(Boolean(result.main && control && !intersects(control, result.main)), label + " " + name + " button overlaps page content");
    }
    assert(result.navScrollWidth <= result.navClientWidth + 1, label + " sidebar navigation has horizontal overflow " + result.navScrollWidth + "/" + result.navClientWidth + "px");
    assert(result.headerScrollWidth <= result.headerClientWidth + 1, label + " sidebar header has horizontal overflow " + result.headerScrollWidth + "/" + result.headerClientWidth + "px");
    assert(result.documentWidth <= result.viewportWidth && result.bodyWidth <= result.viewportWidth, label + " page has horizontal overflow " + result.documentWidth + "/" + result.bodyWidth + "px");
    assert(result.links.every(link => !intersects(link, result.collapse)), label + " collapse button overlaps navigation");
    console.log("[" + label + "] " + JSON.stringify(result));
    return result;
  };

  await measureDesktop("desktop-light-1280", 1280);
  assert(await page.locator("html").evaluate(element => element.classList.contains("light")), "light theme is not active");
  if (process.env.ADMIN_SHELL_SCREENSHOT_DIR) await page.screenshot({ path: process.env.ADMIN_SHELL_SCREENSHOT_DIR + "/desktop-light-1280.png", fullPage: true });

  await page.evaluate(() => document.activeElement?.blur());
  let navFocused = false;
  for (let i = 0; i < 40; i++) {
    await page.keyboard.press("Tab");
    navFocused = await page.evaluate(() => document.activeElement?.matches("a.sidebar-link:focus-visible") ?? false);
    if (navFocused) break;
  }
  assert(navFocused, "collapsed navigation link did not receive visible keyboard focus");
  const navFocusShadow = await page.evaluate(() => getComputedStyle(document.activeElement).boxShadow);
  assert(navFocusShadow !== "none" && Boolean(navFocusShadow), "collapsed navigation focus ring is not rendered");

  await page.evaluate(() => document.activeElement?.blur());
  let collapseFocused = false;
  for (let i = 0; i < 40; i++) {
    await page.keyboard.press("Tab");
    collapseFocused = await page.evaluate(() => document.activeElement?.getAttribute("aria-label") === "Развернуть навигацию" && document.activeElement.matches(":focus-visible"));
    if (collapseFocused) break;
  }
  assert(collapseFocused, "collapse action did not receive visible keyboard focus");
  const collapseFocusShadow = await page.evaluate(() => getComputedStyle(document.activeElement).boxShadow);
  assert(collapseFocusShadow !== "none" && Boolean(collapseFocusShadow), "collapse action focus ring is not rendered");
  assert(await page.getByRole("button", { name: "Развернуть навигацию" }).getAttribute("aria-expanded") === "false", "collapsed navigation aria-expanded must be false");
  await page.keyboard.press("Enter");
  assert(await page.getByRole("button", { name: "Свернуть навигацию" }).getAttribute("aria-expanded") === "true", "Enter did not expand navigation and update aria-expanded");
  await page.keyboard.press("Space");
  assert(await page.getByRole("button", { name: "Развернуть навигацию" }).getAttribute("aria-expanded") === "false", "Space did not collapse navigation and update aria-expanded");

  await page.getByRole("button", { name: /^Переключить тему/ }).click();
  await page.waitForFunction(() => document.documentElement.classList.contains("dark"));
  assert(await page.locator("html").evaluate(element => element.classList.contains("dark")), "dark theme is not active");
  await page.mouse.move(400, 760);
  await page.evaluate(() => document.activeElement?.blur());
  await page.setViewportSize({ width: 1440, height: 900 });
  await measureDesktop("desktop-dark-1440", 1440);
  if (process.env.ADMIN_SHELL_SCREENSHOT_DIR) await page.screenshot({ path: process.env.ADMIN_SHELL_SCREENSHOT_DIR + "/desktop-dark-1440.png", fullPage: true });

  await page.evaluate(() => document.activeElement?.blur());
  for (const width of [360, 390]) {
    await page.setViewportSize({ width, height: 800 });
    await page.mouse.move(200, 100);
    const mobile = await page.evaluate(() => {
      const menu = document.querySelector('button[aria-label="Открыть меню"]');
      const rect = menu?.getBoundingClientRect();
      return {
        menu: rect ? { left: rect.left, right: rect.right, top: rect.top, bottom: rect.bottom, width: rect.width, height: rect.height } : null,
        viewportWidth: window.innerWidth,
        documentWidth: document.documentElement.scrollWidth,
        bodyWidth: document.body.scrollWidth,
      };
    });
    checkArea("mobile menu " + width + "px", mobile.menu);
    assert(mobile.viewportWidth === width, "mobile viewport should be " + width + "px, got " + mobile.viewportWidth);
    assert(mobile.documentWidth <= width && mobile.bodyWidth <= width, "mobile page has horizontal overflow at " + width + "px: " + mobile.documentWidth + "/" + mobile.bodyWidth + "px");
    console.log("[mobile-" + width + "] " + JSON.stringify(mobile));
    if (process.env.ADMIN_SHELL_SCREENSHOT_DIR) await page.screenshot({ path: process.env.ADMIN_SHELL_SCREENSHOT_DIR + "/mobile-" + width + "-dark.png", fullPage: false });
  }

  await page.evaluate(() => document.activeElement?.blur());
  let mobileMenuFocused = false;
  for (let i = 0; i < 12; i++) {
    await page.keyboard.press("Tab");
    mobileMenuFocused = await page.evaluate(() => document.activeElement?.getAttribute("aria-label") === "Открыть меню" && document.activeElement.matches(":focus-visible"));
    if (mobileMenuFocused) break;
  }
  assert(mobileMenuFocused, "mobile menu action did not receive visible keyboard focus");
  const mobileMenuFocusShadow = await page.evaluate(() => getComputedStyle(document.activeElement).boxShadow);
  assert(mobileMenuFocusShadow !== "none" && Boolean(mobileMenuFocusShadow), "mobile menu focus ring is not rendered");
  await page.keyboard.press("Enter");
  await page.getByRole("dialog").waitFor({ state: "visible" });

  await context.close();
  assert(pageErrors.length === 0, "browser reported page/console errors: " + pageErrors.join(" | "));
  if (failures.length > 0) throw new Error(failures.join("\n"));
  console.log("PASS: collapsed desktop targets, keyboard toggling, focus-visible, light/dark and mobile menu target sizes");
} finally {
  await browser.close();
}
`

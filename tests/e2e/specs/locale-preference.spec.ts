import { expect, test } from "@playwright/test";
import { loadRunState, publicURL } from "../helpers/target";

test("E2E-LOC-001 sets a valid locale preference from a configured route", async ({ browser }) => {
  const state = await loadRunState();
  const locale = state.locales.find((value) => value !== "en-US") ?? state.locales[0];
  const route = state.routes[0];
  const context = await browser.newContext();

  const invalid = await context.request.post(`${publicURL}/api/locale-preference`, {
    maxRedirects: 0,
    data: { locale, return_path: "https://evil.example/" + route.path },
  });
  expect(invalid.status(), "external return path is rejected").toBe(400);
  expect(invalid.headers()["set-cookie"], "no cookie on rejected path").toBeUndefined();

  const unsupported = await context.request.post(`${publicURL}/api/locale-preference`, {
    maxRedirects: 0,
    data: { locale: "zz-ZZ", return_path: route.path },
  });
  expect(unsupported.status(), "unsupported locale is rejected").toBe(400);
  expect(unsupported.headers()["set-cookie"], "no cookie on rejected locale").toBeUndefined();

  const response = await context.request.post(`${publicURL}/api/locale-preference`, {
    maxRedirects: 0,
    data: { locale, return_path: route.path },
  });
  expect(response.status(), "locale preference status").toBe(303);
  expect(response.headers()["location"], "locale preference redirect").toBe(route.path);
  const cookie = response.headers()["set-cookie"] ?? "";
  expect(cookie, "host-only HttpOnly cookie").toContain(`zubrium_locale=${locale}`);
  expect(cookie).toContain("Path=/");
  expect(cookie).toContain("HttpOnly");
  expect(cookie).toContain("SameSite=Lax");
  expect(cookie).not.toContain("Domain=");

  const page = await context.request.get(`${publicURL}${route.path}`);
  expect(page.status(), "public delivery with preference cookie").toBe(200);
  expect(await page.text(), `rendered ${locale} from preference cookie`).toContain(`lang="${locale}"`);
});

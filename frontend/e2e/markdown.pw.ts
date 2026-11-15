import { expect, test } from "@playwright/test";

const answer = [
  "**В чём проблема**",
  "Метод `Service.Create` теряет исходную причину ошибки.",
  "",
  "**Как исправить**",
  "",
  "Замените возврат ошибки на оборачивание:",
  "",
  "```go",
  'return Order{}, fmt.Errorf("create order failed: %w", err)',
  "```",
  "",
  "1. Сохраните причину через `%w`.",
  "2. Проверьте её через `errors.Is`.",
  "",
  "### Связь с CI",
  "Тест **TestCreateRepositoryError** проверяет сохранение причины.",
  "",
  "| Проверка | Результат |",
  "| --- | --- |",
  "| TestCreateSuccess | PASSED |",
  "",
  "[Правило команды](/api/v1/context/documents/review-rules#GO-ERR-01)",
  "",
  '[Небезопасная ссылка](javascript:alert("unsafe"))',
  '<img src="https://tracking.invalid/pixel" onerror="alert(1)">',
  "![Внешняя картинка](https://tracking.invalid/image.png)",
  "<script>window.markdownExecuted = true</script>",
].join("\n");

for (const viewport of [{ width: 1440, height: 1000 }, { width: 390, height: 844 }]) {
  test(`форматирует Markdown и ограничивает опасный контент (${viewport.width}px)`, async ({ page }) => {
    await page.setViewportSize(viewport);
    const imageRequests: string[] = [];
    page.on("request", (request) => {
      if (request.url().includes("tracking.invalid")) imageRequests.push(request.url());
    });
    await page.route("**/api/v1/workspace", (route) => route.fulfill({ json: {
      assistant: { mode: "demo" },
      messages: [
        { id: "user-markdown", role: "user", text: "**Проверь мой PR**" },
        { id: "assistant-markdown", role: "assistant", text: answer },
      ],
      reviews: [], review_requests: {}, pending_operations: [],
    } }));
    await page.goto("/");
    const log = page.getByRole("log");
    await expect(log.locator("strong", { hasText: "В чём проблема" })).toBeVisible();
    await expect(log.getByText("**Проверь мой PR**", { exact: true })).toBeVisible();
    const block = log.locator('pre[aria-label="Пример кода"]');
    await expect(log.locator("pre code.language-go")).toHaveText('return Order{}, fmt.Errorf("create order failed: %w", err)\n');
    await expect(log.locator("ol > li")).toHaveCount(2);
    await expect(log.getByRole("table")).toBeVisible();
    await expect(log.getByRole("heading", { name: "Связь с CI", level: 3 })).toBeVisible();
    await expect(log.getByRole("link", { name: "Правило команды" })).toHaveAttribute("rel", "noopener noreferrer");
    await expect(log.getByRole("link", { name: "Небезопасная ссылка" })).toHaveCount(0);
    await expect(log.locator("script, img")).toHaveCount(0);
    expect(imageRequests).toEqual([]);
    expect(await page.evaluate(() => (window as Window & { markdownExecuted?: boolean }).markdownExecuted)).toBeUndefined();
    expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true);
    await expect(block).toHaveCSS("overflow-x", "auto");
    await log.evaluate((element) => { element.scrollTop = 0; });
    await page.screenshot({ path: `/tmp/ai-buddy-markdown-${viewport.width}.png` });
  });
}

import { expect, test, type Page } from "@playwright/test";
import { readFile, writeFile } from "node:fs/promises";
import { join } from "node:path";

const root = process.env.BUDDY_E2E_ROOT!;

async function send(page: Page, text: string) {
  const input = page.getByRole("textbox", { name: "Сообщение AI-Buddy" });
  await input.fill(text);
  await expect(page.getByRole("button", { name: "Отправить сообщение" })).toBeEnabled();
  await input.press("Enter");
}

async function saveCoverage(page: Page, name: string) {
  if (!process.env.BUDDY_TEST_COVERAGE) return;
  const counters = await page.evaluate(() =>
    (globalThis as typeof globalThis & { __coverage__?: unknown }).__coverage__,
  );
  expect(counters).toBeTruthy();
  await writeFile(join("coverage", "raw", `browser-${name}.json`), JSON.stringify(counters));
}

test("от первого сообщения до квитанции и восстановления истории", async ({ page, context }) => {
  const log = page.getByRole("log");
  const confirmButton = page.getByRole("button", { name: "Отправить наставнику" });
  const dialog = page.getByRole("dialog");
  const errors: Error[] = [];
  page.on("pageerror", (error) => errors.push(error));

  await page.goto("/");
  await expect(log.getByText(/Пришлите ссылку или номер PR/)).toBeVisible();
  await send(page, "Проверь PR #17");
  await expect(page.getByRole("heading", { name: "Теряется причина ошибки" })).toBeVisible();

  const finding = page.getByRole("link", { name: "internal/orders/service.go:38" });
  const diffHref = await finding.getAttribute("href");
  const popup = page.waitForEvent("popup");
  await finding.click();
  const diff = await popup;
  await expect(diff.getByRole("heading", { name: "Diff PR #17" })).toBeVisible();
  await expect(diff.locator(".row:has(:target)")).toContainText('return Order{}, errors.New("create order failed")');
  const anotherSession = await context.browser()!.newContext();
  try {
    const response = await anotherSession.request.get(new URL(diffHref!, page.url()).href);
    expect(response.status()).toBe(404);
  } finally {
    await anotherSession.close();
  }
  await diff.close();

  await send(page, "Объясни замечание 1");
  await expect(log.getByText(/потеряет возможность проверить её/)).toBeVisible();

  await confirmButton.click();
  await expect(dialog.getByText(/Наставник —/)).toBeVisible();
  await page.keyboard.press("Escape");
  await expect(dialog).toBeHidden();
  await expect(confirmButton).toBeFocused();

  const fixture = join(root, "mocks", "pr-17.json");
  const original = await readFile(fixture, "utf8");
  await writeFile(fixture, JSON.stringify({ ...JSON.parse(original), current_revision: "fixed" }));
  try {
    await confirmButton.click();
    await dialog.getByRole("button", { name: "Отправить", exact: true }).click();
    await expect(dialog.getByRole("alert")).toContainText("Запустите проверку повторно");
    await dialog.getByRole("button", { name: "Отмена" }).click();

    await page.getByRole("button", { name: "Проверить повторно" }).click();
    await expect(page.getByRole("heading", { name: "В проверенном diff замечаний нет" })).toBeVisible();
    await page.getByLabel("Версия отчёта").selectOption({ index: 1 });
    await expect(finding).toHaveAttribute("href", diffHref!);
    const oldDiff = await context.request.get(new URL(diffHref!, page.url()).href);
    expect(await oldDiff.text()).toContain('a13d9c2');
    await page.getByRole("button", { name: "К текущему отчёту" }).click();

    await confirmButton.click();
    await dialog.getByRole("button", { name: "Отправить", exact: true }).click();
    await expect(log.getByText("Запрос на ревью отправлен", { exact: false })).toBeVisible();
    const receipt = log.getByText(/ID запроса/);
    await expect(receipt).toHaveText(/ID запроса · \S+/);
    const receiptText = await receipt.innerText();
    await saveCoverage(page, "review");

    let release!: () => void;
    const gate = new Promise<void>((done) => { release = done; });
    await page.route("**/api/v1/workspace", async (route) => {
      await gate;
      await route.continue();
    });
    try {
      await page.reload();
      await expect(log.getByRole("status")).toHaveText("Загружаем историю…");
      await expect(log.getByText(/Пришлите ссылку или номер PR/)).toHaveCount(0);
      await page.getByRole("textbox", { name: "Сообщение AI-Buddy" }).fill("Черновик");
      await expect(page.getByRole("button", { name: "Отправить сообщение" })).toBeDisabled();
    } finally {
      release();
    }
    await expect(log.getByText(receiptText, { exact: true })).toBeVisible();
    await expect(log.getByText("Объясни замечание 1", { exact: true })).toBeVisible();
    await expect(page.getByText("Дождитесь обратной связи наставника")).toBeVisible();
    await saveCoverage(page, "restored");
    expect(errors).toEqual([]);
  } finally {
    await writeFile(fixture, original);
  }
});

test("действия в чате и повтор загрузки истории", async ({ page, context }) => {
  await context.grantPermissions(["clipboard-read", "clipboard-write"]);
  await page.route("**/api/v1/workspace", (route) => route.abort());
  await page.goto("/");
  const log = page.getByRole("log");
  await expect(log.getByRole("alert")).toContainText("Не удалось загрузить историю");
  await page.unroute("**/api/v1/workspace");
  await log.getByRole("button", { name: "Повторить", exact: true }).click();
  await expect(log.getByText(/Пришлите ссылку или номер PR/)).toBeVisible();

  await send(page, "Как получить доступ к репозиторию?");
  const access = log.getByRole("link", { name: "Доступ к рабочим инструментам" });
  await expect(access).toHaveAttribute("href", "/api/v1/context/documents/access-guide");
  const accessDoc = await context.request.get(new URL((await access.getAttribute("href"))!, page.url()).href);
  expect(accessDoc.status()).toBe(200);
  expect(await accessDoc.text()).toContain("Доступ к инструменту");

  await send(page, "Как оформить больничный?");
  await expect(log.getByRole("link", { name: "Открыть People-агента" })).toHaveAttribute("href", "https://people.example.test/");

  await send(page, "Как обратиться к наставнику?");
  const draft = log.getByRole("textbox", { name: "Черновик для наставника" });
  await expect(draft).toHaveValue(/Уже проверил/);
  await log.getByRole("button", { name: "Скопировать черновик" }).click();
  await expect(log.getByText("Черновик скопирован")).toBeVisible();
  expect(await page.evaluate(() => navigator.clipboard.readText())).toBe(await draft.inputValue());

  await page.evaluate(() => {
    navigator.clipboard.writeText = () => Promise.reject(new Error("Clipboard unavailable"));
  });
  await log.getByRole("button", { name: "Скопировать черновик" }).click();
  await expect(log.getByText(/Скопируйте его вручную/)).toBeVisible();
  await expect(draft).toBeFocused();

  await page.route("**/api/v1/messages", (route) => route.abort(), { times: 1 });
  await send(page, "Привет");
  await expect(log.getByRole("alert")).toBeVisible();
  await log.getByRole("button", { name: "Повторить", exact: true }).click();
  await expect(log.getByRole("alert")).toHaveCount(0);
  await expect(log.getByText(/PR №17/)).toBeVisible();
  await saveCoverage(page, "chat");
});

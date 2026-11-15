import { expect, test, type Page } from "@playwright/test";
import { readFile, writeFile } from "node:fs/promises";
import { execFile } from "node:child_process";
import { promisify } from "node:util";
import { join } from "node:path";

const root = process.env.BUDDY_E2E_ROOT!;
const execute = promisify(execFile);

async function configureDemo(...args: string[]) {
  await execute(process.env.BUDDY_E2E_DEMO!, [
    "-root", root, "-data", join(root, "data"), ...args,
  ]);
}

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

test("первая линия: документы, доступы и разные профильные помощники", async ({ page, context }) => {
  const log = page.getByRole("log");
  await page.goto("/");
  await expect(log.getByText(/Пришлите ссылку или номер PR/)).toBeVisible();
  await send(page, "Что писать в описании задачи?");
  const source = log.getByRole("link", { name: "Процессы демо-команды · Работа с задачей" });
  await expect(source).toHaveAttribute("href", "/api/v1/context/documents/process-guide#section-1");
  const popup = page.waitForEvent("popup");
  await source.click();
  const document = await popup;
  await expect(document.locator(":target")).toHaveText("Работа с задачей");
  await document.close();
  await send(page, "Как получить доступ к Grafana?");
  await expect(log.getByText(/Для чтения командных дашбордов/)).toContainText("Viewer");
  const request = log.getByRole("link", { name: "Заявка: Grafana (демо)" });
  const response = await context.request.get(new URL((await request.getAttribute("href"))!, page.url()).href);
  expect(response.status()).toBe(200);
  expect(await response.text()).toContain("Заявка Grafana");
  for (const [question, recipient, section] of [
    ["Как оформить больничный?", "HR-помощник", "section-1"],
    ["Как оформить отпуск?", "Помощник по отпускам", "section-2"],
    ["Кому задать вопрос по PostgreSQL?", "Помощник юнита БД", "section-3"],
  ]) {
    await send(page, question);
    await expect(log.getByRole("link", { name: `Профиль: ${recipient} (демо)` })).toHaveAttribute("href", `/api/v1/context/documents/assistant-directory#${section}`);
  }
  await expect(log.getByRole("textbox", { name: "Черновик для наставника" })).toHaveCount(0);
  await send(page, "Как получить доступ к Kubernetes?");
  await expect(log.getByText(/Поиск по материалам демо-команды не нашёл/)).toBeVisible();
  await expect(log.getByRole("textbox", { name: "Черновик для наставника" })).toHaveCount(0);
  await send(page, "Подготовь вопрос наставнику");
  await expect(log.getByRole("textbox", { name: "Черновик для наставника" })).toHaveValue(/Kubernetes/);
  await page.reload();
  await expect(log.getByRole("link", { name: "Профиль: Помощник юнита БД (демо)" })).toBeVisible();
  await expect(log.getByRole("textbox", { name: "Черновик для наставника" })).toHaveValue(/Kubernetes/);
  const workspace = await (await context.request.get("/api/v1/workspace")).json();
  expect(workspace.review_requests).toEqual({});
  expect(workspace.messages.filter((message: { help_execution?: { kind: string } }) => message.help_execution?.kind === "handoff")).toHaveLength(3);
  await saveCoverage(page, "team-help");
});

test("PR и mock CI: падение, объяснение, повторный запуск и исправленная версия", async ({ page, context }) => {
  await configureDemo("-reset");
  const ci = page.getByRole("region", { name: "Результаты CI" });
  try {
    await page.goto("/");
    await expect(page.getByRole("log").getByText(/Пришлите ссылку или номер PR/)).toBeVisible();
    await send(page, "Проверь PR #17 и запусти тесты");
    await expect(ci.getByText("TestCreateRepositoryError", { exact: true })).toBeVisible();
    await expect(ci.getByText(/Код PR не выполняется/)).toBeVisible();
    await expect(ci.locator("li").filter({ hasText: "TestCreateRepositoryError" })).toContainText("FAILED");
    await ci.getByText("Логи mock CI").click();
    await expect(ci.locator("details pre")).toContainText("expected wrapped storage error");
    const firstRun = await ci.getByText(/ID запуска/).innerText();
    expect(firstRun).toContain("CI-");
    await expect(page.getByText(/Разберите падение CI/)).toBeVisible();
    await send(page, "Почему упал тест?");
    await expect(page.getByRole("log").getByText(/Проверка ожидает сохранённую причину ошибки/)).toBeVisible();
    await page.getByRole("button", { name: "Запустить mock CI" }).click();
    await expect(ci.getByText(/ID запуска/)).not.toHaveText(firstRun);
    await configureDemo("-revision", "fixed");
    await page.getByRole("button", { name: "Запустить mock CI" }).click();
    await expect(page.getByRole("heading", { name: "В проверенном diff замечаний нет" })).toBeVisible();
    await expect(ci.locator("li").filter({ hasText: "TestCreateRepositoryError" })).toContainText("PASSED");
    const fixedRun = await ci.getByText(/ID запуска/).innerText();
    const workspace = await (await context.request.get("/api/v1/workspace")).json();
    expect(workspace.reviews[0].checks.head_sha).toBe(workspace.reviews[0].head_sha);
    expect(workspace.reviews[0].checks.run_id).not.toBe(workspace.reviews[2].checks.run_id);
    await page.getByLabel("Версия отчёта").selectOption({ index: 2 });
    await expect(ci.getByText(/ID запуска/)).toHaveText(firstRun);
    await expect(ci.locator("li").filter({ hasText: "TestCreateRepositoryError" })).toContainText("FAILED");
    await page.reload();
    await expect(ci.getByText(/ID запуска/)).toHaveText(fixedRun);
    await expect(ci.locator("li").filter({ hasText: "TestCreateRepositoryError" })).toContainText("PASSED");
    await saveCoverage(page, "ci-runs");
  } finally {
    await configureDemo("-reset");
  }
});

test("ошибка внутреннего скилла видна в чате и не создаёт отчёт", async ({ page, context }) => {
  const path = join(root, "mocks", "skills.json");
  const original = await readFile(path, "utf8");
  try {
    const unavailable = JSON.parse(original);
    unavailable.skills[0].version = "99.0.0";
    await writeFile(path, JSON.stringify(unavailable));
    await page.goto("/");
    await expect(page.getByRole("log").getByText(/Пришлите ссылку или номер PR/)).toBeVisible();
    await send(page, "Проверь PR #17");
    await expect(page.getByRole("log").getByRole("alert")).toContainText("Внутренний скилл недоступен");
    const workspace = await (await context.request.get("/api/v1/workspace")).json();
    expect(workspace.reviews).toEqual([]);
    expect(workspace.review_requests).toEqual({});
    await writeFile(path, original);
    await send(page, "Проверь PR #17");
    await expect(page.getByRole("heading", { name: "Теряется причина ошибки" })).toBeVisible();
    await expect(page.getByRole("log").getByText(/Источник — mock внутреннего скилла/)).toBeVisible();
    await saveCoverage(page, "skill-recovery");
  } finally {
    await writeFile(path, original);
  }
});

test("повтор после временного сбоя использует исходную операцию", async ({ page, context }) => {
  const path = join(root, "mocks", "skills.json");
  const original = await readFile(path, "utf8");
  const posts: string[] = [];
  page.on("request", (request) => {
    if (request.method() === "POST") posts.push(new URL(request.url()).pathname);
  });
  try {
    await page.goto("/");
    await expect(page.getByRole("log").getByText(/Пришлите ссылку или номер PR/)).toBeVisible();
    await writeFile(path, "{");
    await send(page, "Проверь PR #17");
    const alert = page.getByRole("log").getByRole("alert");
    await expect(alert.getByRole("button", { name: "Повторить", exact: true })).toBeVisible();
    await writeFile(path, original);
    await alert.getByRole("button", { name: "Повторить", exact: true }).click();
    await expect(page.getByRole("heading", { name: "Теряется причина ошибки" })).toBeVisible();
    expect(posts.filter((path) => path.endsWith("/messages"))).toHaveLength(1);
    expect(posts.filter((path) => path.endsWith("/retry"))).toHaveLength(1);
    const workspace = await (await context.request.get("/api/v1/workspace")).json();
    expect(workspace.messages.filter((message: { role: string }) => message.role === "user")).toHaveLength(1);
    expect(workspace.reviews).toHaveLength(1);
    await saveCoverage(page, "temporary-retry");
  } finally {
    await writeFile(path, original);
  }
});

test("новая версия PR требует отдельной отправки, повтор той же версии сохраняет статус", async ({ page, context }) => {
  await configureDemo("-reset");
  const log = page.getByRole("log");
  const sendReview = page.getByRole("button", { name: "Отправить наставнику" });
  async function confirm() {
    await sendReview.click();
    await page.getByRole("dialog").getByRole("button", { name: "Отправить", exact: true }).click();
    await expect(page.getByText("Дождитесь обратной связи наставника")).toBeVisible();
  }
  try {
    await page.goto("/");
    await expect(log.getByText(/Пришлите ссылку или номер PR/)).toBeVisible();
    await send(page, "Проверь PR #17");
    await expect(page.getByRole("heading", { name: "Теряется причина ошибки" })).toBeVisible();
    await confirm();
    const initial = await (await context.request.get("/api/v1/workspace")).json();
    const initialReceipt = initial.review_requests["e2-engineers#17"];
    await configureDemo("-revision", "fixed");
    await page.getByRole("button", { name: "Проверить повторно" }).click();
    await expect(page.getByRole("heading", { name: "В проверенном diff замечаний нет" })).toBeVisible();
    await expect(sendReview).toBeVisible();
    await expect(page.getByText("Дождитесь обратной связи наставника")).toHaveCount(0);
    await page.reload();
    await expect(sendReview).toBeVisible();
    await confirm();
    const fixed = await (await context.request.get("/api/v1/workspace")).json();
    const fixedReceipt = fixed.review_requests["e2-engineers#17"];
    expect(fixedReceipt.head_sha).toBe(fixed.reviews[0].head_sha);
    expect(fixedReceipt.head_sha).not.toBe(initialReceipt.head_sha);
    expect(fixedReceipt.id).not.toBe(initialReceipt.id);
    await page.getByRole("button", { name: "Проверить повторно" }).click();
    await expect(page.getByText("Дождитесь обратной связи наставника")).toBeVisible();
    await expect(sendReview).toHaveCount(0);
    await page.reload();
    await expect(page.getByText("Дождитесь обратной связи наставника")).toBeVisible();
    const restored = await (await context.request.get("/api/v1/workspace")).json();
    expect(restored.review_requests["e2-engineers#17"]).toEqual(fixedReceipt);
    expect(restored.messages.filter((message: { receipt?: unknown }) => message.receipt)).toHaveLength(2);
    await saveCoverage(page, "revision-request");
  } finally {
    await configureDemo("-reset");
  }
});

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
  await expect(page.getByText(/заменяет исходную ошибку/)).toBeVisible();
  await expect(page.getByText(/Сборку и тесты PR агент не запускал/)).toBeVisible();
  await expect(log.getByText(/Источник — mock внутреннего скилла/)).toBeVisible();
  const workspace = await (await context.request.get("/api/v1/workspace")).json();
  expect(workspace.reviews[0].skill_execution).toMatchObject({
    skill: { id: "pr-self-review", version: "0.4.0" },
    target: { id: "internal-pr-review", version: "1.0.0" },
    contract: "pr-review.v1", source: "MOCK",
    run_id: expect.any(String), instructions_sha256: expect.stringMatching(/^[a-f0-9]{64}$/),
  });

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

  await configureDemo("-revision", "fixed");
  await configureDemo("-checks", "PASSED");
  try {
    await confirmButton.click();
    await dialog.getByRole("button", { name: "Отправить", exact: true }).click();
    await expect(dialog.getByRole("alert")).toContainText("Запустите проверку повторно");
    await dialog.getByRole("button", { name: "Отмена" }).click();

    await page.getByRole("button", { name: "Проверить повторно" }).click();
    await expect(page.getByRole("heading", { name: "В проверенном diff замечаний нет" })).toBeVisible();
    await expect(page.getByText(/видно оборачивание/)).toBeVisible();
    await expect(page.getByText("CI · Тестовая проверка CI пройдена")).toBeVisible();
    await page.getByLabel("Версия отчёта").selectOption({ index: 1 });
    await expect(finding).toHaveAttribute("href", diffHref!);
    await expect(page.getByText(/заменяет исходную ошибку/)).toBeVisible();
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
    await configureDemo("-reset");
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
  const access = log.getByRole("link", { name: "Доступ к рабочим инструментам · Репозиторий" });
  await expect(access).toHaveAttribute("href", "/api/v1/context/documents/access-guide#section-3");
  const accessDoc = await context.request.get(new URL((await access.getAttribute("href"))!, page.url()).href);
  expect(accessDoc.status()).toBe(200);
  expect(await accessDoc.text()).toContain("Доступ к инструменту");

  await send(page, "Как оформить больничный?");
  await expect(log.getByRole("link", { name: "Профиль: HR-помощник (демо)" })).toHaveAttribute("href", "/api/v1/context/documents/assistant-directory#section-1");

  await send(page, "Не могу запустить orders: получаю 502, уже пробовал перезапуск Docker");
  await expect(log.getByText(/Что проверил Buddy/)).toContainText("STOPPED");
  await expect(log.getByRole("textbox", { name: "Черновик для наставника" })).toHaveCount(0);
  await send(page, "Не помогло");
  await expect(log.getByText(/Предыдущая рекомендация не помогла/)).toBeVisible();
  await send(page, "Как обратиться к наставнику?");
  const draft = log.getByRole("textbox", { name: "Черновик для наставника" }).last();
  await expect(draft).toHaveValue(/перезапуск Docker/);
  await draft.fill((await draft.inputValue()) + "\nДополнение: проверил логи сервиса.");
  await log.getByRole("button", { name: "Скопировать черновик" }).last().click();
  await expect(log.getByText("Черновик скопирован").last()).toBeVisible();
  expect(await page.evaluate(() => navigator.clipboard.readText())).toBe(await draft.inputValue());

  await page.evaluate(() => {
    navigator.clipboard.writeText = () => Promise.reject(new Error("Clipboard unavailable"));
  });
  await log.getByRole("button", { name: "Скопировать черновик" }).last().click();
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

test("после перезагрузки уточняет исходную отправку без нового запроса ревью", async ({ page, context }) => {
  const posts: { path: string; key?: string }[] = [];
  page.on("request", (request) => {
    if (request.method() === "POST") {
      posts.push({
        path: new URL(request.url()).pathname,
        key: request.headers()["idempotency-key"],
      });
    }
  });
  await configureDemo("-reset");
  await page.goto("/");
  await expect(page.getByRole("log").getByText(/Пришлите ссылку или номер PR/)).toBeVisible();
  await send(page, "Проверь PR #17");
  await expect(page.getByRole("heading", { name: "Теряется причина ошибки" })).toBeVisible();
  const fixture = join(root, "mocks", "pr-17.json");
  const original = await readFile(fixture, "utf8");
  try {
    await writeFile(fixture, "{");
    await page.getByRole("button", { name: "Отправить наставнику" }).click();
    await page.getByRole("dialog").getByRole("button", { name: "Отправить", exact: true }).click();
    await expect(page.getByRole("dialog").getByRole("alert")).toContainText("Сервис ещё не подтвердил");
    const workspace = await (await context.request.get("/api/v1/workspace")).json();
    const pending = workspace.pending_operations[0];
    expect(pending.state).toBe("RESULT_UNKNOWN");
    expect(pending.key).toBe(posts.find((post) => post.path.endsWith("/review-requests"))!.key);
    await page.reload();
    await expect(page.getByRole("log").getByRole("alert")).toContainText("Сервис ещё не подтвердил");
    await expect(page.getByText("Уточните результат исходного запроса")).toBeVisible();
    expect(posts.filter((post) => post.path.endsWith("/review-requests"))).toHaveLength(1);
    expect(posts.filter((post) => post.path.endsWith("/retry"))).toHaveLength(0);

    await writeFile(fixture, original);
    await page.getByRole("button", { name: "Уточнить результат", exact: true }).click();
    await expect(page.getByRole("dialog")).toContainText("a13d9c2");
    await page.getByRole("dialog").getByRole("button", { name: "Уточнить результат", exact: true }).click();
    await expect(page.getByRole("log").getByText(/Запрос на ревью отправлен/)).toBeVisible();
    expect(posts.filter((post) => post.path.endsWith("/review-requests"))).toHaveLength(1);
    expect(posts.find((post) => post.path.endsWith("/retry"))).toEqual({
      path: `/api/v1/operations/${pending.operation_id}/retry`,
      key: pending.key,
    });
    const restored = await (await context.request.get("/api/v1/workspace")).json();
    expect(restored.pending_operations).toEqual([]);
    await saveCoverage(page, "unknown-restored");
  } finally {
    await writeFile(fixture, original);
    await configureDemo("-reset");
  }
});

test("продолжает опрос операции из workspace при открытии страницы", async ({ page, context }) => {
  let release!: () => void;
  let gate = new Promise<void>((resolve) => { release = resolve; });
  await page.route("**/api/v1/operations/*", async (route) => {
    await gate;
    await route.continue();
  });
  await page.goto("/");
  await expect(page.getByRole("log").getByText(/Пришлите ссылку или номер PR/)).toBeVisible();
  const accepted = page.waitForResponse((response) =>
    response.request().method() === "POST" &&
    new URL(response.url()).pathname === "/api/v1/messages");
  await send(page, "Привет");
  const response = await accepted;
  const submitted = {
    id: (await response.json()).operation_id,
    key: response.request().headers()["idempotency-key"],
  };
  const saved = await (await context.request.get("/api/v1/workspace")).json();
  saved.pending_operations = [{
    operation_id: submitted.id, key: submitted.key, state: "RUNNING",
    command: { kind: "message", text: "Привет" },
  }];
  await page.route("**/api/v1/workspace", (route) => route.fulfill({ json: saved }));
  let newMessages = 0;
  page.on("request", (request) => {
    if (request.method() === "POST" && new URL(request.url()).pathname === "/api/v1/messages") newMessages++;
  });
  try {
    await page.reload();
    await expect(page.getByRole("status").filter({ hasText: "AI-Buddy готовит ответ" })).toBeVisible();
  } finally {
    release();
    gate = Promise.resolve();
  }
  await expect(page.getByRole("log").getByText(/PR №17/)).toBeVisible();
  await expect(page.getByRole("log").getByText("Привет", { exact: true })).toHaveCount(1);
  expect(newMessages).toBe(0);
  await saveCoverage(page, "running-restored");
});

test("показывает учебное окружение, команды и CI нужной версии PR", async ({ page, context }) => {
  await configureDemo("-reset");
  const log = page.getByRole("log");
  try {
    await page.goto("/");
    await expect(log.getByText(/Пришлите ссылку или номер PR/)).toBeVisible();
    await send(page, "Где тестовое окружение?");
    const initial = log.getByText(/Учебный сервис — orders-training/);
    await expect(initial).toContainText("статус STOPPED");
    await expect(initial).toContainText("Источник состояния окружения и CI — mock-инструменты");
    await expect(initial).toContainText("make training-up");
    await expect(initial).toContainText("make training-test");
    await expect(initial).toContainText("a13d9c2");
    const guide = log.getByRole("link", { name: "Учебный сервис и тестовое окружение" });
    await expect(guide).toHaveAttribute("href", "/api/v1/context/documents/environment-guide");
    const doc = await context.request.get("/api/v1/context/documents/environment-guide");
    expect(doc.status()).toBe(200);
    expect(await doc.text()).toContain("TestCreateRepositoryError");

    await configureDemo("-environment", "READY");
    await configureDemo("-revision", "fixed");
    await configureDemo("-checks", "FAILED");
    await send(page, "Покажи результат тестов");
    const checked = log.getByText(/CI — FAILED/).filter({ hasText: "c84be19" });
    await expect(checked).toContainText("статус READY");
    await expect(checked).toContainText("c84be19");
    await expect(checked).toContainText("Разберите ошибку CI");
    await page.reload();
    await expect(checked).toBeVisible();
    await expect(log.getByText(/статус STOPPED/)).toBeVisible();
    await saveCoverage(page, "training");
  } finally {
    await configureDemo("-reset");
  }
});

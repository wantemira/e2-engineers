import { afterEach, describe, expect, it, vi } from "vitest";
import { executeCommand, loadWorkspace } from "./api";

const receipt = {
  id: "review-request-17",
  status: "REQUESTED",
  mentor: { id: "mentor-1", name: "Наставник" },
  pr_number: 17,
  head_sha: "commit-2",
};

function json(body: unknown, status = 200) {
  return new Response(JSON.stringify(body), {
    status,
    headers: { "Content-Type": "application/json" },
  });
}

afterEach(() => vi.unstubAllGlobals());

describe("операции AI-Buddy", () => {
  it("читает режим Live с сервера и отклоняет повреждённый режим", async () => {
    const fetch = vi.fn()
      .mockResolvedValueOnce(json({ messages: [], reviews: [], review_requests: {}, assistant: { mode: "live", model: "configured-model" } }))
      .mockResolvedValueOnce(json({ messages: [], reviews: [], review_requests: {}, assistant: { mode: "live" } }));
    vi.stubGlobal("fetch", fetch);
    const signal = new AbortController().signal;
    expect((await loadWorkspace(signal)).assistant).toEqual({ mode: "live", model: "configured-model" });
    await expect(loadWorkspace(signal)).rejects.toMatchObject({ code: "INVALID_RESPONSE" });
  });
  it("повторяет временно упавшую операцию с прежними ID и ключом", async () => {
    const fetch = vi.fn()
      .mockResolvedValueOnce(json({ operation_id: "temporary-op" }, 202))
      .mockResolvedValueOnce(json({ state: "FAILED", error: { code: "DEPENDENCY_UNAVAILABLE", retryable: true } }))
      .mockResolvedValueOnce(json({ operation_id: "temporary-op" }, 202))
      .mockResolvedValueOnce(json({ state: "SUCCEEDED", result: { messages: [] } }));
    vi.stubGlobal("fetch", fetch);
    const pending = { command: { kind: "review" as const, prRef: "17" }, key: "original-key" };
    const signal = new AbortController().signal;
    await expect(executeCommand(pending, signal)).rejects.toMatchObject({
      operationId: "temporary-op", retryable: true, needsReconciliation: true,
    });
    await executeCommand({ ...pending, operationId: "temporary-op", needsReconciliation: true }, signal);
    expect(fetch.mock.calls.map(([url]) => url)).toEqual([
      "/api/v1/self-reviews", "/api/v1/operations/temporary-op",
      "/api/v1/operations/temporary-op/retry", "/api/v1/operations/temporary-op",
    ]);
    expect(fetch.mock.calls[2][1].headers["Idempotency-Key"]).toBe("original-key");
  });
  it.each([
    ["SKILL_UNAVAILABLE", "Внутренний скилл недоступен"],
    ["SKILL_INVALID_RESULT", "Не удалось проверить ответ внутреннего скилла"],
  ])("показывает понятную ошибку %s", async (code, message) => {
    const fetch = vi.fn()
      .mockResolvedValueOnce(json({ operation_id: "skill-operation" }, 202))
      .mockResolvedValueOnce(json({ state: "FAILED", error: { code, retryable: false } }));
    vi.stubGlobal("fetch", fetch);
    await expect(executeCommand({
      command: { kind: "review", prRef: "17" }, key: "skill-key",
    }, new AbortController().signal)).rejects.toMatchObject({
      code, message: expect.stringContaining(message),
    });
  });
  it("загружает пустую историю и сохранённые квитанции через сессионный запрос", async () => {
    const fetch = vi.fn().mockResolvedValueOnce(
      json({
        messages: [],
        reviews: [],
        review_requests: { "backend#17": receipt },
      }),
    );
    vi.stubGlobal("fetch", fetch);

    const workspace = await loadWorkspace(new AbortController().signal);

    expect(workspace).toEqual({
      messages: [],
      reviews: [],
      pendingOperations: [],
      reviewRequests: {
        "backend#17": {
          id: receipt.id,
          status: receipt.status,
          mentor: receipt.mentor,
          prNumber: 17,
          headSha: receipt.head_sha,
        },
      },
    });
    expect(fetch.mock.calls[0][0]).toBe("/api/v1/workspace");
    expect(fetch.mock.calls[0][1].credentials).toBe("include");
  });

  it("продолжает сохранённую операцию чтением исходного ID", async () => {
    const fetch = vi.fn()
      .mockResolvedValueOnce(json({
        messages: [], reviews: [], review_requests: {},
        pending_operations: [{
          operation_id: "saved-operation", key: "original-key", state: "RUNNING",
          command: { kind: "message", text: "Проверь PR #17" },
        }],
      }))
      .mockResolvedValueOnce(json({ state: "SUCCEEDED", result: { messages: [] } }));
    vi.stubGlobal("fetch", fetch);
    const signal = new AbortController().signal;
    const workspace = await loadWorkspace(signal);
    await executeCommand(workspace.pendingOperations[0], signal);
    expect(fetch.mock.calls.map(([url]) => url)).toEqual([
      "/api/v1/workspace", "/api/v1/operations/saved-operation",
    ]);
    expect(fetch.mock.calls.every(([, options]) => options.method === "GET")).toBe(true);
  });

  it("восстанавливает неизвестную отправку и уточняет её только после действия пользователя", async () => {
    const review = {
      id: "saved-review", head_sha: receipt.head_sha, mentor: receipt.mentor,
      pr: { number: 17, repository: "backend", title: "PR", url: "https://example.test/pull/17" },
      findings: [], checks: { status: "UNKNOWN", label: "Нет данных" },
      created_at: "2026-11-12T10:00:00Z",
    };
    const fetch = vi.fn()
      .mockResolvedValueOnce(json({
        messages: [], reviews: [review], review_requests: {},
        pending_operations: [{
          operation_id: "saved-request", key: "original-confirmation", state: "RESULT_UNKNOWN",
          command: { kind: "request", review_run_id: review.id, expected_mentor_id: receipt.mentor.id },
        }],
      }))
      .mockResolvedValueOnce(json({ state: "RESULT_UNKNOWN" }))
      .mockResolvedValueOnce(json({ operation_id: "saved-request" }, 202))
      .mockResolvedValueOnce(json({ state: "SUCCEEDED", result: { messages: [], review_request: receipt } }));
    vi.stubGlobal("fetch", fetch);
    const signal = new AbortController().signal;
    const { pendingOperations } = await loadWorkspace(signal);
    const pending = pendingOperations[0];
    await expect(executeCommand(pending, signal)).rejects.toMatchObject({
      operationId: "saved-request", needsReconciliation: true,
    });
    expect(fetch.mock.calls).toHaveLength(2);
    await executeCommand({ ...pending, needsReconciliation: true }, signal);
    expect(fetch.mock.calls[2][0]).toBe("/api/v1/operations/saved-request/retry");
    expect(fetch.mock.calls[2][1].headers["Idempotency-Key"]).toBe("original-confirmation");
    expect(fetch.mock.calls.filter(([, options]) => options.method === "POST")).toHaveLength(1);
  });

  it("отклоняет незавершённую отправку без сохранённой проверки", async () => {
    vi.stubGlobal("fetch", vi.fn().mockResolvedValueOnce(json({
      messages: [], reviews: [], review_requests: {},
      pending_operations: [{
        operation_id: "request", key: "key", state: "RESULT_UNKNOWN",
        command: { kind: "request", review_run_id: "missing", expected_mentor_id: "mentor" },
      }],
    })));
    await expect(loadWorkspace(new AbortController().signal)).rejects.toMatchObject({
      code: "INVALID_RESPONSE",
    });
  });

  it("не передаёт повреждённую сохранённую историю в React", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn().mockResolvedValueOnce(
        json({
          messages: [{ id: "m1", role: "assistant", text: { invalid: true } }],
          reviews: [],
          review_requests: {},
        }),
      ),
    );

    await expect(
      loadWorkspace(new AbortController().signal),
    ).rejects.toMatchObject({ code: "INVALID_RESPONSE", retryable: true });
  });

  it("уточняет неизвестный результат с прежними operation ID и ключом действия", async () => {
    const fetch = vi
      .fn()
      .mockResolvedValueOnce(json({ operation_id: "operation-1" }, 202))
      .mockResolvedValueOnce(json({ state: "RESULT_UNKNOWN" }))
      .mockResolvedValueOnce(json({ operation_id: "operation-1" }, 202))
      .mockResolvedValueOnce(
        json({
          state: "SUCCEEDED",
          result: { messages: [], review_request: receipt },
        }),
      );
    vi.stubGlobal("fetch", fetch);

    const pending = {
      command: {
        kind: "request" as const,
        reviewRunId: "run-2",
        mentorId: "mentor-1",
      },
      key: "confirmation-1",
    };
    const signal = new AbortController().signal;
    await expect(executeCommand(pending, signal)).rejects.toMatchObject({
      code: "RESULT_UNKNOWN",
      operationId: "operation-1",
      needsReconciliation: true,
    });

    const result = await executeCommand(
      { ...pending, operationId: "operation-1", needsReconciliation: true },
      signal,
    );

    expect(result.reviewRequest?.id).toBe("review-request-17");
    expect(fetch.mock.calls.map(([url]) => url)).toEqual([
      "/api/v1/review-requests",
      "/api/v1/operations/operation-1",
      "/api/v1/operations/operation-1/retry",
      "/api/v1/operations/operation-1",
    ]);
    expect(fetch.mock.calls[0][1].headers["Idempotency-Key"]).toBe(
      "confirmation-1",
    );
    expect(fetch.mock.calls[2][1].headers["Idempotency-Key"]).toBe(
      "confirmation-1",
    );
  });

  it("после сетевого сбоя при опросе продолжает чтение принятой операции", async () => {
    const fetch = vi
      .fn()
      .mockResolvedValueOnce(json({ operation_id: "operation-2" }, 202))
      .mockRejectedValueOnce(new TypeError("connection reset"))
      .mockResolvedValueOnce(
        json({ state: "SUCCEEDED", result: { messages: [] } }),
      );
    vi.stubGlobal("fetch", fetch);
    const command = {
      command: { kind: "message" as const, text: "Проверь PR" },
      key: "message-1",
    };
    const signal = new AbortController().signal;

    await expect(executeCommand(command, signal)).rejects.toMatchObject({
      operationId: "operation-2",
      retryable: true,
    });
    await executeCommand({ ...command, operationId: "operation-2" }, signal);

    expect(
      fetch.mock.calls.filter(([, options]) => options.method === "POST"),
    ).toHaveLength(1);
  });

  it("отклоняет успешный ответ записи без квитанции сервиса", async () => {
    const fetch = vi
      .fn()
      .mockResolvedValueOnce(json({ operation_id: "operation-3" }, 202))
      .mockResolvedValueOnce(
        json({ state: "SUCCEEDED", result: { messages: [] } }),
      );
    vi.stubGlobal("fetch", fetch);

    await expect(
      executeCommand(
        {
          command: {
            kind: "request",
            reviewRunId: "run-2",
            mentorId: "mentor-1",
          },
          key: "confirmation-2",
        },
        new AbortController().signal,
      ),
    ).rejects.toMatchObject({
      code: "INVALID_RESPONSE",
      operationId: "operation-3",
    });
  });

  it("отклоняет отчёт с некорректным списком замечаний", async () => {
    const fetch = vi
      .fn()
      .mockResolvedValueOnce(json({ operation_id: "operation-4" }, 202))
      .mockResolvedValueOnce(
        json({
          state: "SUCCEEDED",
          result: {
            messages: [],
            review: {
              id: "run-1",
              head_sha: "commit-1",
              mentor: receipt.mentor,
              pr: {
                number: 17,
                title: "Добавить обработчик",
                repository: "backend",
                url: "https://github.com/team/backend/pull/17",
              },
              findings: null,
              checks: { status: "PASSED", label: "Проверки прошли" },
              created_at: "2026-11-12T12:00:00Z",
            },
          },
        }),
      );
    vi.stubGlobal("fetch", fetch);

    await expect(
      executeCommand(
        { command: { kind: "review", prRef: "17" }, key: "review-1" },
        new AbortController().signal,
      ),
    ).rejects.toMatchObject({
      code: "INVALID_RESPONSE",
      operationId: "operation-4",
    });
  });

  it("отклоняет объект вместо текста замечания до передачи отчёта в React", async () => {
    const fetch = vi
      .fn()
      .mockResolvedValueOnce(json({ operation_id: "operation-5" }, 202))
      .mockResolvedValueOnce(
        json({
          state: "SUCCEEDED",
          result: {
            messages: [],
            review: {
              id: "run-1",
              head_sha: "commit-1",
              mentor: receipt.mentor,
              pr: {
                number: 17,
                title: "Добавить обработчик",
                repository: "backend",
                url: "https://github.com/team/backend/pull/17",
              },
              findings: [
                {
                  id: "finding-1",
                  kind: "problem",
                  title: { text: "Некорректный формат" },
                  description: "Пояснение",
                },
              ],
              checks: { status: "PASSED", label: "Проверки прошли" },
              created_at: "2026-11-12T12:00:00Z",
            },
          },
        }),
      );
    vi.stubGlobal("fetch", fetch);

    await expect(
      executeCommand(
        { command: { kind: "review", prRef: "17" }, key: "review-2" },
        new AbortController().signal,
      ),
    ).rejects.toMatchObject({
      code: "INVALID_RESPONSE",
      operationId: "operation-5",
    });
  });
});

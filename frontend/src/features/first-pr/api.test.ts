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

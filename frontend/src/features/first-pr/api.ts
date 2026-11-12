import type {
  ChatMessage,
  Finding,
  Mentor,
  OperationResult,
  ReviewRequest,
  ReviewRun,
} from "./types";

type Command =
  | { kind: "message"; text: string }
  | { kind: "review"; prRef: string }
  | { kind: "request"; reviewRunId: string; mentorId: string };

export interface PendingCommand {
  command: Command;
  key: string;
  operationId?: string;
  needsReconciliation?: boolean;
}

export class BuddyApiError extends Error {
  constructor(
    message: string,
    public readonly code: string,
    public readonly retryable: boolean,
    public readonly operationId?: string,
    public readonly needsReconciliation = false,
  ) {
    super(message);
    this.name = "BuddyApiError";
  }
}

const apiBase = (import.meta.env.VITE_API_BASE_URL || "/api/v1").replace(
  /\/$/,
  "",
);
const operationTimeoutMs = 90_000;

interface ErrorDto {
  code?: string;
  message?: string;
  retryable?: boolean;
}

interface ReviewRequestDto {
  id: string;
  status: "REQUESTED";
  mentor: Mentor;
  pr_number: number;
  head_sha: string;
}

interface ReviewDto {
  id: string;
  head_sha: string;
  mentor: Mentor;
  pr: ReviewRun["pr"];
  findings: Finding[];
  checks: ReviewRun["checks"];
  created_at: string;
  is_stale?: boolean;
}

interface MessageDto extends Omit<ChatMessage, "receipt"> {
  receipt?: ReviewRequestDto;
}

interface OperationDto {
  state: "QUEUED" | "RUNNING" | "SUCCEEDED" | "FAILED" | "RESULT_UNKNOWN";
  result?: {
    messages: MessageDto[];
    review?: ReviewDto;
    review_request?: ReviewRequestDto;
  };
  error?: ErrorDto;
}

export interface Workspace {
  messages: ChatMessage[];
  reviews: ReviewRun[];
  reviewRequests: Record<string, ReviewRequest>;
}

function errorMessage(code: string): string {
  switch (code) {
    case "STALE_HEAD":
      return "В PR появились новые изменения. Запустите проверку повторно.";
    case "NOT_FOUND":
    case "FORBIDDEN":
      return "PR недоступен. Проверьте ссылку и доступ к репозиторию.";
    case "UNAUTHORIZED":
      return "Сессия завершилась. Войдите в приложение повторно.";
    case "DIFF_TOO_LARGE":
      return "Изменений слишком много для одной проверки. Разделите PR на меньшие части.";
    case "RESULT_UNKNOWN":
      return "Сервис ещё не подтвердил результат отправки. Уточните статус запроса.";
    case "TIMEOUT":
      return "Ответ занимает больше времени. Попробуйте уточнить результат.";
    case "INVALID_INPUT":
      return "Проверьте номер или ссылку на PR и повторите запрос.";
    default:
      return "Не удалось получить ответ. Попробуйте ещё раз.";
  }
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return value !== null && typeof value === "object" && !Array.isArray(value);
}

function isIdentifier(value: unknown): value is string {
  return typeof value === "string" && value.length > 0;
}

function isMentor(value: unknown): value is Mentor {
  return isRecord(value) && isIdentifier(value.id) && isIdentifier(value.name);
}

async function jsonRequest<T>(
  path: string,
  signal: AbortSignal,
  body?: unknown,
  key?: string,
): Promise<T> {
  let response: Response;

  try {
    response = await fetch(`${apiBase}${path}`, {
      method: body === undefined ? "GET" : "POST",
      credentials: "include",
      signal,
      headers: {
        Accept: "application/json",
        ...(body !== undefined ? { "Content-Type": "application/json" } : {}),
        ...(key ? { "Idempotency-Key": key } : {}),
      },
      ...(body !== undefined ? { body: JSON.stringify(body) } : {}),
    });
  } catch (error) {
    if (signal.aborted) throw error;
    throw new BuddyApiError(
      "Не удалось связаться с сервисом. Попробуйте ещё раз.",
      "NETWORK",
      true,
    );
  }

  const bodyText = await response.text();
  let payload: unknown;

  try {
    payload = bodyText ? JSON.parse(bodyText) : undefined;
  } catch {
    throw new BuddyApiError(
      "Сервис вернул некорректный ответ. Попробуйте ещё раз.",
      "INVALID_RESPONSE",
      true,
    );
  }

  if (!response.ok) {
    const data =
      payload && typeof payload === "object" ? (payload as ErrorDto) : {};
    const code =
      typeof data.code === "string"
        ? data.code
        : response.status === 401
          ? "UNAUTHORIZED"
          : "DEPENDENCY_UNAVAILABLE";
    const retryable =
      typeof data.retryable === "boolean"
        ? data.retryable
        : response.status >= 500;
    throw new BuddyApiError(errorMessage(code), code, retryable);
  }

  if (!isRecord(payload)) {
    throw new BuddyApiError(
      "Сервис вернул пустой ответ. Попробуйте ещё раз.",
      "INVALID_RESPONSE",
      true,
    );
  }

  return payload as T;
}

function mapReceipt(dto: ReviewRequestDto): ReviewRequest {
  if (
    !isRecord(dto) ||
    !isIdentifier(dto.id) ||
    dto.status !== "REQUESTED" ||
    !isMentor(dto.mentor) ||
    !isIdentifier(dto.head_sha) ||
    !Number.isInteger(dto.pr_number) ||
    dto.pr_number < 1
  ) {
    throw new BuddyApiError(
      errorMessage("INVALID_RESPONSE"),
      "INVALID_RESPONSE",
      true,
    );
  }
  return {
    id: dto.id,
    status: dto.status,
    mentor: dto.mentor,
    prNumber: dto.pr_number,
    headSha: dto.head_sha,
  };
}

function mapResult(
  result: NonNullable<OperationDto["result"]>,
): OperationResult {
  if (
    !isRecord(result) ||
    (result.messages !== undefined && !Array.isArray(result.messages))
  ) {
    throw new BuddyApiError(
      errorMessage("INVALID_RESPONSE"),
      "INVALID_RESPONSE",
      true,
    );
  }
  if (
    result.messages?.some(
      (message) =>
        !isRecord(message) ||
        !isIdentifier(message.id) ||
        typeof message.text !== "string" ||
        !["assistant", "user"].includes(message.role) ||
        (message.links !== undefined &&
          (!Array.isArray(message.links) ||
            message.links.some(
              (link) =>
                !isRecord(link) ||
                !isIdentifier(link.label) ||
                !isIdentifier(link.url),
            ))),
    )
  ) {
    throw new BuddyApiError(
      errorMessage("INVALID_RESPONSE"),
      "INVALID_RESPONSE",
      true,
    );
  }
  const messages = (result.messages || []).map(({ receipt, ...message }) => ({
    ...message,
    ...(receipt !== undefined ? { receipt: mapReceipt(receipt) } : {}),
  }));
  const review = result.review;

  if (
    review !== undefined &&
    (!isRecord(review) ||
      !isIdentifier(review.id) ||
      !isIdentifier(review.head_sha) ||
      !isMentor(review.mentor) ||
      !isRecord(review.pr) ||
      !isIdentifier(review.pr.url) ||
      !Number.isInteger(review.pr.number) ||
      review.pr.number < 1 ||
      !isIdentifier(review.pr.repository) ||
      !isIdentifier(review.pr.title) ||
      !Array.isArray(review.findings) ||
      !isRecord(review.checks) ||
      typeof review.checks.label !== "string" ||
      !["PASSED", "FAILED", "RUNNING", "UNKNOWN"].includes(
        review.checks.status,
      ) ||
      typeof review.created_at !== "string" ||
      !Number.isFinite(Date.parse(review.created_at)) ||
      (review.is_stale !== undefined && typeof review.is_stale !== "boolean"))
  ) {
    throw new BuddyApiError(
      errorMessage("INVALID_RESPONSE"),
      "INVALID_RESPONSE",
      true,
    );
  }
  if (
    review?.findings.some(
      (finding) =>
        !isRecord(finding) ||
        !isIdentifier(finding.id) ||
        !["problem", "assumption"].includes(finding.kind) ||
        !isIdentifier(finding.title) ||
        typeof finding.description !== "string" ||
        (finding.location !== undefined &&
          (!isRecord(finding.location) ||
            !isIdentifier(finding.location.path) ||
            !Number.isInteger(finding.location.line) ||
            finding.location.line < 1 ||
            (finding.location.url !== undefined &&
              typeof finding.location.url !== "string"))) ||
        (finding.rule !== undefined &&
          (!isRecord(finding.rule) ||
            !isIdentifier(finding.rule.id) ||
            !isIdentifier(finding.rule.label) ||
            !isIdentifier(finding.rule.url))),
    )
  ) {
    throw new BuddyApiError(
      errorMessage("INVALID_RESPONSE"),
      "INVALID_RESPONSE",
      true,
    );
  }

  const reviewRequest =
    result.review_request !== undefined
      ? mapReceipt(result.review_request)
      : messages.find((message) => message.receipt)?.receipt;

  return {
    messages,
    ...(review
      ? {
          review: {
            id: review.id,
            headSha: review.head_sha,
            mentor: review.mentor,
            pr: review.pr,
            findings: review.findings,
            checks: review.checks,
            createdAt: review.created_at,
            isStale: review.is_stale,
          },
        }
      : {}),
    ...(reviewRequest ? { reviewRequest } : {}),
  };
}

export async function loadWorkspace(signal: AbortSignal): Promise<Workspace> {
  const data = await jsonRequest<Record<string, unknown>>("/workspace", signal);

  if (
    !Array.isArray(data.messages) ||
    !Array.isArray(data.reviews) ||
    !isRecord(data.review_requests)
  ) {
    throw new BuddyApiError(
      "Не удалось загрузить историю. Попробуйте ещё раз.",
      "INVALID_RESPONSE",
      true,
    );
  }

  const messages = mapResult({
    messages: data.messages as MessageDto[],
  }).messages;
  const reviews = data.reviews.map((review) => {
    const result = mapResult({ messages: [], review: review as ReviewDto });
    if (!result.review) {
      throw new BuddyApiError(
        errorMessage("INVALID_RESPONSE"),
        "INVALID_RESPONSE",
        true,
      );
    }
    return result.review;
  });
  const reviewRequests = Object.fromEntries(
    Object.entries(data.review_requests).map(([key, value]) => {
      if (!key) {
        throw new BuddyApiError(
          errorMessage("INVALID_RESPONSE"),
          "INVALID_RESPONSE",
          true,
        );
      }
      return [key, mapReceipt(value as ReviewRequestDto)];
    }),
  );

  return { messages, reviews, reviewRequests };
}

function pause(signal: AbortSignal): Promise<void> {
  return new Promise((resolve, reject) => {
    const onAbort = () => {
      clearTimeout(timer);
      reject(signal.reason);
    };
    const timer = setTimeout(() => {
      signal.removeEventListener("abort", onAbort);
      resolve();
    }, 1_000);
    signal.addEventListener("abort", onAbort, { once: true });
    if (signal.aborted) onAbort();
  });
}

function requestFor(command: Command): { path: string; body: unknown } {
  switch (command.kind) {
    case "message":
      return { path: "/messages", body: { text: command.text } };
    case "review":
      return { path: "/self-reviews", body: { pr_ref: command.prRef } };
    case "request":
      return {
        path: "/review-requests",
        body: {
          review_run_id: command.reviewRunId,
          expected_mentor_id: command.mentorId,
        },
      };
  }
}

export async function executeCommand(
  pending: PendingCommand,
  callerSignal: AbortSignal,
): Promise<OperationResult> {
  const deadlineSignal = AbortSignal.timeout(operationTimeoutMs);
  const signal = AbortSignal.any([callerSignal, deadlineSignal]);
  let operationId = pending.operationId;

  try {
    if (operationId && pending.needsReconciliation) {
      await jsonRequest(
        `/operations/${encodeURIComponent(operationId)}/retry`,
        signal,
        {},
        pending.key,
      );
    }

    if (!operationId) {
      const request = requestFor(pending.command);
      const accepted = await jsonRequest<{ operation_id: string }>(
        request.path,
        signal,
        request.body,
        pending.key,
      );
      if (typeof accepted.operation_id !== "string" || !accepted.operation_id) {
        throw new BuddyApiError(
          errorMessage("INVALID_RESPONSE"),
          "INVALID_RESPONSE",
          true,
        );
      }
      operationId = accepted.operation_id;
    }

    for (;;) {
      const operation = await jsonRequest<OperationDto>(
        `/operations/${encodeURIComponent(operationId)}`,
        signal,
      );

      switch (operation.state) {
        case "SUCCEEDED": {
          if (!operation.result)
            throw new BuddyApiError(
              errorMessage("INVALID_RESPONSE"),
              "INVALID_RESPONSE",
              true,
            );
          const result = mapResult(operation.result);
          if (pending.command.kind === "request" && !result.reviewRequest) {
            throw new BuddyApiError(
              errorMessage("INVALID_RESPONSE"),
              "INVALID_RESPONSE",
              true,
            );
          }
          return result;
        }
        case "FAILED": {
          const code =
            typeof operation.error?.code === "string"
              ? operation.error.code
              : "OPERATION_FAILED";
          throw new BuddyApiError(errorMessage(code), code, false);
        }
        case "RESULT_UNKNOWN":
          throw new BuddyApiError(
            errorMessage("RESULT_UNKNOWN"),
            "RESULT_UNKNOWN",
            true,
            operationId,
            true,
          );
        case "QUEUED":
        case "RUNNING":
          await pause(signal);
          break;
        default:
          throw new BuddyApiError(
            errorMessage("INVALID_RESPONSE"),
            "INVALID_RESPONSE",
            true,
          );
      }
    }
  } catch (error) {
    if (callerSignal.aborted) throw error;
    if (deadlineSignal.aborted) {
      throw new BuddyApiError(
        errorMessage("TIMEOUT"),
        "TIMEOUT",
        true,
        operationId,
      );
    }
    if (error instanceof BuddyApiError) {
      throw new BuddyApiError(
        error.message,
        error.code,
        error.retryable,
        error.operationId || operationId,
        error.needsReconciliation,
      );
    }
    throw new BuddyApiError(
      errorMessage("NETWORK"),
      "NETWORK",
      true,
      operationId,
    );
  }
}

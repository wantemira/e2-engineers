import { useCallback, useEffect, useRef, useState } from "react";
import {
  BuddyApiError,
  executeCommand,
  loadWorkspace,
  type PendingCommand,
} from "./api";
import type {
  AssistantInfo,
  BuddyError,
  BusyState,
  ChatMessage,
  OperationResult,
  ReviewRequest,
  ReviewRun,
} from "./types";

interface ActiveCommand extends PendingCommand {
  reviewSnapshot?: ReviewRun;
}

interface PendingReviewRequest {
  command: ActiveCommand;
  review: ReviewRun;
}

function prKey(review: ReviewRun): string {
  return `${review.pr.repository}#${review.pr.number}`;
}

export function useBuddy() {
  const [assistant, setAssistant] = useState<AssistantInfo | null>(null);
  const [messages, setMessages] = useState<ChatMessage[]>([]);
  const [reviews, setReviews] = useState<ReviewRun[]>([]);
  const [selectedId, setSelectedId] = useState<string | null>(null);
  const [reviewRequests, setReviewRequests] = useState<
    Record<string, ReviewRequest>
  >({});
  const [busy, setBusy] = useState<BusyState | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<BuddyError | null>(null);
  const controller = useRef<AbortController | null>(null);
  const hydrationController = useRef<AbortController | null>(null);
  const ready = useRef<Promise<boolean> | null>(null);
  const retryCommand = useRef<ActiveCommand | null>(null);
  const unresolvedRequests = useRef(new Map<string, PendingReviewRequest>());
  const reviewedPr = useRef<string | null>(null);
  const mounted = useRef(true);

  const applyResult = useCallback(
    (result: OperationResult, pending: ActiveCommand) => {
      setMessages((previous) => {
        const hasServerUser = result.messages.some(
          (message) => message.role === "user",
        );
        const merged = new Map(
          previous
            .filter((message) => !hasServerUser || message.id !== pending.key)
            .map((message) => [message.id, message]),
        );

        for (const message of result.messages) merged.set(message.id, message);

        if (
          result.reviewRequest &&
          !result.messages.some((message) => message.receipt)
        ) {
          const id = `request-${result.reviewRequest.id}`;
          merged.set(id, {
            id,
            role: "assistant",
            text: "",
            receipt: result.reviewRequest,
          });
        }

        return [...merged.values()];
      });

      if (result.review) {
        const review = result.review;
        reviewedPr.current = prKey(review);
        setReviews((previous) => [
          review,
          ...previous.filter((item) => item.id !== review.id),
        ]);
        setSelectedId(review.id);
      }
      if (result.reviewRequest) {
        const snapshot = pending.reviewSnapshot ?? result.review;
        const identity = snapshot ? prKey(snapshot) : reviewedPr.current;
        if (identity) {
          const receipt = result.reviewRequest;
          setReviewRequests((previous) => ({
            ...previous,
            [identity]: receipt,
          }));
        }
      }
    },
    [],
  );

  const run = useCallback(
    async (pending: ActiveCommand): Promise<boolean> => {
      if (controller.current) return false;

      const active = new AbortController();
      controller.current = active;
      setBusy(pending.command.kind);
      setError(null);
      retryCommand.current = pending;

      try {
        const result = await executeCommand(pending, active.signal);
        if (active.signal.aborted || !mounted.current) return false;
        applyResult(result, pending);
        retryCommand.current = null;
        if (pending.command.kind === "request" && pending.reviewSnapshot) {
          unresolvedRequests.current.delete(prKey(pending.reviewSnapshot));
        }
        return true;
      } catch (failure) {
        if (active.signal.aborted || !mounted.current) return false;

        if (failure instanceof BuddyApiError) {
          retryCommand.current = {
            ...pending,
            operationId: failure.operationId,
            needsReconciliation: failure.needsReconciliation,
          };
          if (pending.command.kind === "request" && pending.reviewSnapshot) {
            const identity = prKey(pending.reviewSnapshot);
            if (failure.retryable) {
              unresolvedRequests.current.set(identity, {
                command: retryCommand.current,
                review: pending.reviewSnapshot,
              });
            } else {
              unresolvedRequests.current.delete(identity);
            }
          }
          if (failure.code === "STALE_HEAD") {
            setReviews((previous) =>
              previous.map((review, index) => {
                const affected = pending.reviewSnapshot
                  ? review.id === pending.reviewSnapshot.id
                  : index === 0;
                return affected ? { ...review, isStale: true } : review;
              }),
            );
          }
          setError({ message: failure.message, retryable: failure.retryable });
        } else {
          setError({
            message: "Не удалось получить ответ. Попробуйте ещё раз.",
            retryable: true,
          });
        }
        return false;
      } finally {
        if (controller.current === active) {
          controller.current = null;
          if (mounted.current) setBusy(null);
        }
      }
    },
    [applyResult],
  );

  const initialize = useCallback(() => {
    hydrationController.current?.abort();
    const active = new AbortController();
    hydrationController.current = active;
    setLoading(true);
    setError(null);

    const request = (async () => {
      try {
        const workspace = await loadWorkspace(active.signal);
        if (active.signal.aborted || !mounted.current) return false;

        setMessages(workspace.messages);
        setAssistant(workspace.assistant ?? { mode: "demo" });
        setReviews(workspace.reviews);
        setReviewRequests(workspace.reviewRequests);
        setSelectedId(workspace.reviews[0]?.id ?? null);
        reviewedPr.current = workspace.reviews[0]
          ? prKey(workspace.reviews[0])
          : null;
        // Show the restored history while resuming the original operation IDs.
        setLoading(false);
        for (const pending of workspace.pendingOperations) {
          if (active.signal.aborted || !mounted.current) return false;
          const command = pending.command;
          const reviewSnapshot =
            command.kind === "request"
              ? workspace.reviews.find(
                  (review) => review.id === command.reviewRunId,
                )
              : undefined;
          await run({ ...pending, reviewSnapshot });
        }
        return !active.signal.aborted && mounted.current;
      } catch (failure) {
        if (active.signal.aborted || !mounted.current) return false;
        setError({
          message:
            "Не удалось загрузить историю. Проверьте подключение и повторите запрос.",
          retryable: !(failure instanceof BuddyApiError) || failure.retryable,
        });
        return false;
      } finally {
        if (!active.signal.aborted && mounted.current) setLoading(false);
      }
    })();

    ready.current = request;
    return request;
  }, [run]);

  useEffect(() => {
    mounted.current = true;
    void initialize();
    return () => {
      mounted.current = false;
      hydrationController.current?.abort();
      controller.current?.abort();
    };
  }, [initialize]);

  const sendMessage = useCallback(
    async (text: string) => {
      const trimmed = text.trim();
      if (!trimmed || controller.current) return false;
      if (!ready.current) initialize();
      if (!(await ready.current) || !mounted.current || controller.current)
        return false;
      const key = crypto.randomUUID();
      setMessages((previous) => [
        ...previous,
        { id: key, role: "user", text: trimmed },
      ]);
      void run({ command: { kind: "message", text: trimmed }, key });
      return true;
    },
    [initialize, run],
  );

  const currentReview = reviews[0] ?? null;
  const selectedReview =
    reviews.find((review) => review.id === selectedId) ?? currentReview;
  const savedRequest = currentReview
    ? (reviewRequests[prKey(currentReview)] ?? null)
    : null;
  const latestRequest =
    savedRequest && currentReview &&
    savedRequest.headSha === currentReview.headSha &&
    savedRequest.mentor.id === currentReview.mentor.id &&
    savedRequest.prNumber === currentReview.pr.number
      ? savedRequest
      : null;
  const pendingRequest = currentReview
    ? unresolvedRequests.current.get(prKey(currentReview))
    : undefined;
  const requestTarget = pendingRequest?.review ?? currentReview;
  const isReconcilingRequest = pendingRequest !== undefined;

  const recheck = useCallback(async (runChecks = false) => {
    if (!currentReview || loading) return;
    await run({
      command: { kind: "review", prRef: currentReview.pr.url, runChecks },
      key: crypto.randomUUID(),
    });
  }, [currentReview, loading, run]);

  const requestReview = useCallback(async (): Promise<boolean> => {
    if (!currentReview || loading || latestRequest) return false;
    const unresolved = unresolvedRequests.current.get(prKey(currentReview));
    if (unresolved) {
      return run(unresolved.command);
    }
    if (currentReview.isStale) return false;
    return run({
      command: {
        kind: "request",
        reviewRunId: currentReview.id,
        mentorId: currentReview.mentor.id,
      },
      key: crypto.randomUUID(),
      reviewSnapshot: currentReview,
    });
  }, [currentReview, loading, latestRequest, run]);

  const retry = useCallback(async () => {
    if (retryCommand.current) await run(retryCommand.current);
    else await initialize();
  }, [initialize, run]);

  return {
    assistant,
    messages,
    reviews,
    currentReview,
    selectedReview,
    requestTarget,
    isReconcilingRequest,
    selectReview: setSelectedId,
    latestRequest,
    busy,
    loading,
    error,
    sendMessage,
    recheck,
    requestReview,
    retry,
    dismissError: () => setError(null),
  };
}

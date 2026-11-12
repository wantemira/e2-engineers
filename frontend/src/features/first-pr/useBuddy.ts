import { useCallback, useEffect, useRef, useState } from "react";
import {
  BuddyApiError,
  executeCommand,
  loadWorkspace,
  type PendingCommand,
} from "./api";
import type {
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
        setReviews(workspace.reviews);
        setReviewRequests(workspace.reviewRequests);
        setSelectedId(workspace.reviews[0]?.id ?? null);
        reviewedPr.current = workspace.reviews[0]
          ? prKey(workspace.reviews[0])
          : null;
        return true;
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
  }, []);

  useEffect(() => {
    mounted.current = true;
    void initialize();
    return () => {
      mounted.current = false;
      hydrationController.current?.abort();
      controller.current?.abort();
    };
  }, [initialize]);

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
        if (!mounted.current) return false;
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
        controller.current = null;
        if (mounted.current) setBusy(null);
      }
    },
    [applyResult],
  );

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
  const latestRequest = currentReview
    ? (reviewRequests[prKey(currentReview)] ?? null)
    : null;
  const pendingRequest = currentReview
    ? unresolvedRequests.current.get(prKey(currentReview))
    : undefined;
  const requestTarget = pendingRequest?.review ?? currentReview;
  const isReconcilingRequest = pendingRequest !== undefined;

  const recheck = useCallback(async () => {
    if (!currentReview) return;
    await run({
      command: { kind: "review", prRef: currentReview.pr.url },
      key: crypto.randomUUID(),
    });
  }, [currentReview, run]);

  const requestReview = useCallback(async (): Promise<boolean> => {
    if (!currentReview || latestRequest) return false;
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
  }, [currentReview, latestRequest, run]);

  const retry = useCallback(async () => {
    if (retryCommand.current) await run(retryCommand.current);
    else await initialize();
  }, [initialize, run]);

  return {
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

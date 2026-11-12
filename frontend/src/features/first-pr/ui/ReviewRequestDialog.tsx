import { Button } from "../../../components/Button";
import { Dialog } from "../../../components/Dialog";
import type { BuddyError, ReviewRun } from "../types";
import styles from "./ReviewRequestDialog.module.css";

type ReviewRequestDialogProps = {
  open: boolean;
  review: ReviewRun;
  busy: boolean;
  reconciling: boolean;
  error: BuddyError | null;
  onClose: () => void;
  onConfirm: () => Promise<void>;
};

export function ReviewRequestDialog({
  open,
  review,
  busy,
  reconciling,
  error,
  onClose,
  onConfirm,
}: ReviewRequestDialogProps) {
  return (
    <Dialog
      open={open}
      title={
        reconciling
          ? "Уточнить результат отправки?"
          : "Отправить PR наставнику?"
      }
      busy={busy}
      onClose={onClose}
    >
      <div className={styles.summary}>
        <p className={styles.commit}>
          PR #{review.pr.number} · {review.pr.repository} ·{" "}
          {review.headSha.slice(0, 7)}
        </p>
        <p>Наставник — {review.mentor.name}</p>
      </div>
      {error && (
        <p className={styles.error} role="alert">
          {error.message}
        </p>
      )}
      <div className={styles.actions}>
        <Button
          onClick={() => void onConfirm()}
          disabled={busy || (!reconciling && review.isStale)}
        >
          {busy
            ? "Отправляем…"
            : reconciling || error?.retryable
              ? "Уточнить результат"
              : "Отправить"}
        </Button>
        <Button variant="secondary" onClick={onClose} disabled={busy}>
          Отмена
        </Button>
      </div>
    </Dialog>
  );
}

import { Button } from "../../../components/Button";
import { SafeLink } from "../../../components/SafeLink";
import type { BusyState, ReviewRun } from "../types";
import { FindingCard } from "./FindingCard";
import styles from "./ReviewPanel.module.css";

type ReviewPanelProps = {
  review: ReviewRun | null;
  currentReview: ReviewRun | null;
  reviews: ReviewRun[];
  busy: BusyState | null;
  requested: boolean;
  onSelectReview: (id: string) => void;
  onRecheck: () => Promise<void>;
  onRequest: () => void;
};

export function ReviewPanel({
  review,
  currentReview,
  reviews,
  busy,
  requested,
  onSelectReview,
  onRecheck,
  onRequest,
}: ReviewPanelProps) {
  const isPrevious = Boolean(review && review.id !== currentReview?.id);
  const disabled = Boolean(busy);
  const history = reviews.filter(
    (item) =>
      item.pr.number === currentReview?.pr.number &&
      item.pr.repository === currentReview?.pr.repository,
  );
  const recheckButton = (
    <Button
      variant={
        requested || review?.isStale || review?.findings.length
          ? "primary"
          : "secondary"
      }
      onClick={() => void onRecheck()}
      disabled={disabled}
    >
      {busy === "review" ? "Проверяем…" : "Проверить повторно"}
    </Button>
  );
  const requestButton = !requested && !review?.isStale && (
    <Button
      variant={review?.findings.length ? "secondary" : "primary"}
      onClick={onRequest}
      disabled={disabled}
    >
      Отправить наставнику
    </Button>
  );

  return (
    <section
      className={styles.panel}
      aria-labelledby="review-heading"
      aria-busy={busy === "review"}
    >
      <h2 id="review-heading" className={styles.heading}>
        Самопроверка PR
      </h2>
      {!review ? (
        <div className={styles.empty}>
          <h3>Отчёт появится после проверки</h3>
        </div>
      ) : (
        <>
          <div
            className={styles.reportContent}
            tabIndex={0}
            aria-label="Результаты самопроверки"
          >
            <div className={styles.meta}>
              <SafeLink href={review.pr.url} className={styles.prLink}>
                PR #{review.pr.number}
              </SafeLink>
              <span aria-hidden="true">·</span>
              <span className={styles.repository} title={review.pr.repository}>
                {review.pr.repository}
              </span>
              <span aria-hidden="true">·</span>
              <code title={review.headSha}>{review.headSha.slice(0, 7)}</code>
            </div>
            <p className={styles.checks}>
              <span
                className={`${styles.checkDot} ${styles[review.checks.status.toLowerCase()]}`}
                aria-hidden="true"
              />
              CI · {review.checks.label}
            </p>

            {history.length > 1 && (
              <label className={styles.history}>
                <span className="visually-hidden">Версия отчёта</span>
                <select
                  value={review.id}
                  onChange={(event) => onSelectReview(event.target.value)}
                >
                  {history.map((item, index) => (
                    <option key={item.id} value={item.id}>
                      {index === 0 ? "Текущий отчёт" : "Предыдущий отчёт"} ·{" "}
                      {item.headSha.slice(0, 7)}
                    </option>
                  ))}
                </select>
              </label>
            )}

            {(isPrevious || review.isStale) && (
              <p className={styles.versionNote}>
                {review.isStale
                  ? "PR обновился. Этот отчёт относится к предыдущей версии."
                  : "Вы просматриваете предыдущую проверку PR."}
              </p>
            )}

            {review.findings.length ? (
              <div className={styles.findings}>
                {review.findings.map((finding) => (
                  <FindingCard key={finding.id} finding={finding} />
                ))}
              </div>
            ) : (
              <div className={styles.empty}>
                <h3>В проверенном diff замечаний нет</h3>
              </div>
            )}
          </div>

          <div className={styles.actions}>
            {isPrevious ? (
              <Button
                variant="secondary"
                onClick={() =>
                  currentReview && onSelectReview(currentReview.id)
                }
              >
                К текущему отчёту
              </Button>
            ) : review.findings.length ? (
              <>
                {recheckButton}
                {requestButton}
              </>
            ) : (
              <>
                {requestButton}
                {recheckButton}
              </>
            )}
          </div>
        </>
      )}
    </section>
  );
}

import { useState } from "react";
import { useBuddy } from "../features/first-pr/useBuddy";
import { ChatPanel } from "../features/first-pr/ui/ChatPanel";
import { ReviewPanel } from "../features/first-pr/ui/ReviewPanel";
import { ReviewRequestDialog } from "../features/first-pr/ui/ReviewRequestDialog";
import styles from "./App.module.css";

export function App() {
  const buddy = useBuddy();
  const [confirmationOpen, setConfirmationOpen] = useState(false);
  const { currentReview, latestRequest, busy } = buddy;
  let stage = "Готов помочь";
  let nextStep = "Спросите о процессе, доступе или передайте PR";

  if (latestRequest) {
    stage = "Запрос создан в демонстрационном Git-сервисе";
    nextStep = "Дождитесь обратной связи наставника";
  } else if (buddy.isReconcilingRequest) {
    stage = "Ожидаем подтверждения отправки";
    nextStep = "Уточните результат исходного запроса";
  } else if (busy === "review") {
    stage = "PR в работе";
    nextStep = "Проверяем актуальную версию PR";
  } else if (currentReview?.isStale) {
    stage = "PR в работе";
    nextStep = "PR обновился. Запустите проверку снова";
  } else if (currentReview) {
    stage = "Самопроверка завершена";
    nextStep = currentReview.checks.status === "FAILED"
      ? "Разберите падение CI и повторите проверку"
      : "Разберите замечания или отправьте PR наставнику";
  }

  async function confirmReview() {
    const sent = await buddy.requestReview();
    if (sent) setConfirmationOpen(false);
  }

  return (
    <>
      <a href="#chat-message" className={styles.skipLink}>
        Перейти к сообщению
      </a>
      <header className={styles.header}>
        <div className={styles.headerInner}>
          <a href="/" className={styles.brand} aria-label="AI-Buddy, главная">
            <img src="/buddy-mark.svg" width="24" height="24" alt="" />
            <span>AI-Buddy</span>
          </a>
          <span className={styles.team}>Backend</span>
          {buddy.assistant && (
            <span className={styles.team} title={buddy.assistant.model}>
              {buddy.assistant.mode === "live" ? "Live LLM" : "Demo Mode"}
            </span>
          )}
        </div>
      </header>

      <main className={styles.page}>
        <section className={styles.intro} aria-labelledby="page-title">
          <h1 id="page-title">Помощь стажёру</h1>
          <p className={styles.progress} aria-live="polite">
            <span
              className={`${styles.statusDot} ${latestRequest ? styles.complete : ""}`}
              aria-hidden="true"
            />
            <span className={styles.stage}>{stage}</span>
            <span className={styles.separator} aria-hidden="true">
              ·
            </span>
            <span className={styles.nextStep}>{nextStep}</span>
          </p>
        </section>

        <div className={styles.workspace}>
          <ChatPanel
            messages={buddy.messages}
            loading={buddy.loading}
            busy={busy}
            error={confirmationOpen ? null : buddy.error}
            onSend={buddy.sendMessage}
            onRetry={buddy.retry}
          />
          <ReviewPanel
            review={buddy.selectedReview}
            currentReview={currentReview}
            reviews={buddy.reviews}
            busy={busy}
            requested={Boolean(latestRequest)}
            reconciling={buddy.isReconcilingRequest}
            onSelectReview={buddy.selectReview}
            onRecheck={buddy.recheck}
            onRunChecks={() => buddy.recheck(true)}
            onRequest={() => {
              buddy.dismissError();
              setConfirmationOpen(true);
            }}
          />
        </div>
      </main>

      {buddy.requestTarget && (
        <ReviewRequestDialog
          open={confirmationOpen}
          review={buddy.requestTarget}
          reconciling={buddy.isReconcilingRequest}
          busy={busy === "request"}
          error={buddy.error}
          onClose={() => {
            setConfirmationOpen(false);
            buddy.dismissError();
          }}
          onConfirm={confirmReview}
        />
      )}
    </>
  );
}

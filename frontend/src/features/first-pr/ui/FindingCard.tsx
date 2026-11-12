import { SafeLink } from "../../../components/SafeLink";
import type { Finding } from "../types";
import styles from "./ReviewPanel.module.css";

export function FindingCard({ finding }: { finding: Finding }) {
  const headingId = `finding-${finding.id}`;

  return (
    <article className={styles.finding} aria-labelledby={headingId}>
      <div className={styles.findingMeta}>
        <span
          className={`${styles.kind} ${finding.kind === "problem" ? styles.problem : styles.assumption}`}
        >
          {finding.kind === "problem" ? "Проблема" : "Предположение"}
        </span>
        {finding.location && (
          <SafeLink href={finding.location.url} className={styles.codeLocation}>
            {finding.location.path}:{finding.location.line}
          </SafeLink>
        )}
      </div>
      <h3 id={headingId}>{finding.title}</h3>
      <p>{finding.description}</p>
      {finding.rule && (
        <SafeLink href={finding.rule.url} className={styles.rule}>
          {finding.rule.label} · {finding.rule.id}
        </SafeLink>
      )}
    </article>
  );
}

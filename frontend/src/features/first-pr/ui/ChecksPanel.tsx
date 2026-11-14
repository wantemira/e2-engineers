import type { Checks } from "../types";
import styles from "./ReviewPanel.module.css";

export function ChecksPanel({ checks }: { checks: Checks }) {
  if (!checks.runId) return null;
  return (
    <section className={styles.ciDetails} aria-label="Результаты CI">
      <p>Источник — mock CI. Код PR не выполняется.</p>
      <p>ID запуска · <code>{checks.runId}</code></p>
      <p>SHA · <code>{checks.headSha}</code></p>
      {checks.tests?.length ? (
        <ul>{checks.tests.map((test) => (
          <li key={test.name}>
            <strong>{test.name}</strong> · {test.status}
            {test.error && <pre>{test.error}</pre>}
          </li>
        ))}</ul>
      ) : <p>Отдельные результаты тестов не предоставлены.</p>}
      {checks.logs && <details><summary>Логи mock CI</summary><pre>{checks.logs}</pre></details>}
    </section>
  );
}

import Markdown from "react-markdown";
import remarkGfm from "remark-gfm";
import { SafeLink } from "./SafeLink";
import styles from "./MarkdownMessage.module.css";

export function MarkdownMessage({ text }: { text: string }) {
  return (
    <div className={styles.message}>
      <Markdown
        remarkPlugins={[remarkGfm]}
        skipHtml
        components={{
          a: ({ href, children }) => <SafeLink href={href}>{children}</SafeLink>,
          img: ({ alt }) => <span>{alt ? `[Изображение: ${alt}]` : "[Изображение]"}</span>,
          h1: ({ children }) => <h3>{children}</h3>,
          h2: ({ children }) => <h3>{children}</h3>,
          pre: ({ children }) => (
            <pre tabIndex={0} aria-label="Пример кода">{children}</pre>
          ),
          table: ({ children }) => (
            <div className={styles.tableScroll} tabIndex={0} role="region" aria-label="Таблица в ответе">
              <table>{children}</table>
            </div>
          ),
        }}
      >
        {text}
      </Markdown>
    </div>
  );
}

import {
  useEffect,
  useRef,
  useState,
  type FormEvent,
  type KeyboardEvent,
} from "react";
import { Button } from "../../../components/Button";
import { SafeLink } from "../../../components/SafeLink";
import type { BuddyError, BusyState, ChatMessage } from "../types";
import styles from "./ChatPanel.module.css";

type ChatPanelProps = {
  messages: ChatMessage[];
  busy: BusyState | null;
  error: BuddyError | null;
  onSend: (text: string) => Promise<boolean>;
  onRetry: () => Promise<void>;
};

const busyLabels: Record<BusyState, string> = {
  message: "AI-Buddy готовит ответ…",
  review: "Проверяем актуальную версию PR…",
  request: "Отправляем запрос наставнику…",
};

export function ChatPanel({
  messages,
  busy,
  error,
  onSend,
  onRetry,
}: ChatPanelProps) {
  const [draft, setDraft] = useState("");
  const listRef = useRef<HTMLDivElement>(null);
  const inputRef = useRef<HTMLTextAreaElement>(null);
  const shouldFollow = useRef(true);

  useEffect(() => {
    const list = listRef.current;
    if (list && shouldFollow.current) list.scrollTop = list.scrollHeight;
  }, [messages, busy, error]);

  useEffect(() => {
    const input = inputRef.current;
    if (!input) return;
    input.style.height = "auto";
    input.style.height = `${Math.min(input.scrollHeight, 120)}px`;
  }, [draft]);

  async function submit(event?: FormEvent) {
    event?.preventDefault();
    const text = draft.trim();
    if (!text || busy) return;
    shouldFollow.current = true;
    const accepted = await onSend(text);
    if (accepted)
      setDraft((current) => (current.trim() === text ? "" : current));
    inputRef.current?.focus();
  }

  function handleKeyDown(event: KeyboardEvent<HTMLTextAreaElement>) {
    if (
      event.key === "Enter" &&
      !event.shiftKey &&
      !event.nativeEvent.isComposing
    ) {
      event.preventDefault();
      void submit();
    }
  }

  return (
    <section className={styles.panel} aria-labelledby="chat-heading">
      <h2 id="chat-heading" className={styles.heading}>
        Чат
      </h2>
      <div
        ref={listRef}
        className={styles.messages}
        role="log"
        aria-label="Диалог с AI-Buddy"
        aria-live="polite"
        aria-relevant="additions text"
        tabIndex={0}
        onScroll={(event) => {
          const list = event.currentTarget;
          shouldFollow.current =
            list.scrollHeight - list.scrollTop - list.clientHeight < 64;
        }}
      >
        {messages.length === 0 && (
          <div className={styles.assistant}>
            <span className={styles.author}>AI-Buddy</span>
            <p>
              Пришлите ссылку или номер PR — проверю изменения по правилам
              команды. Здесь также можно спросить про рабочие инструменты и
              процессы.
            </p>
          </div>
        )}
        {messages.map((message) => (
          <ChatMessageItem key={message.id} message={message} />
        ))}
        {busy && (
          <p className={styles.pending} role="status">
            <span className={styles.pulse} aria-hidden="true" />
            {busyLabels[busy]}
          </p>
        )}
        {error && (
          <div className={styles.error} role="alert">
            <p>{error.message}</p>
            {error.retryable && (
              <Button
                variant="text"
                disabled={Boolean(busy)}
                onClick={() => void onRetry()}
              >
                Повторить
              </Button>
            )}
          </div>
        )}
      </div>

      <form
        className={styles.composer}
        onSubmit={submit}
        aria-label="Новое сообщение"
      >
        <label htmlFor="chat-message" className="visually-hidden">
          Сообщение AI-Buddy
        </label>
        <textarea
          ref={inputRef}
          id="chat-message"
          rows={1}
          maxLength={8000}
          value={draft}
          placeholder="Напишите сообщение"
          onChange={(event) => setDraft(event.target.value)}
          onKeyDown={handleKeyDown}
          aria-describedby="composer-hint"
        />
        <span id="composer-hint" className="visually-hidden">
          Enter — отправить. Shift и Enter — новая строка.
        </span>
        <Button
          type="submit"
          disabled={!draft.trim() || Boolean(busy)}
          aria-label="Отправить сообщение"
        >
          Отправить
        </Button>
      </form>
    </section>
  );
}

function ChatMessageItem({ message }: { message: ChatMessage }) {
  if (message.role === "user")
    return (
      <div className={styles.user}>
        <p>{message.text}</p>
      </div>
    );

  return (
    <div className={styles.assistant}>
      {message.text && (
        <>
          <span className={styles.author}>AI-Buddy</span>
          <p>{message.text}</p>
        </>
      )}
      {message.links && (
        <ul className={styles.sources}>
          {message.links.map((link) => (
            <li key={`${link.url}-${link.label}`}>
              <SafeLink href={link.url}>{link.label}</SafeLink>
            </li>
          ))}
        </ul>
      )}
      {message.mentorDraft && <MentorDraft text={message.mentorDraft} />}
      {message.receipt && (
        <div className={styles.receipt}>
          <p className={styles.receiptTitle}>
            <span aria-hidden="true">✓</span> Запрос на ревью отправлен
          </p>
          <p>Наставник — {message.receipt.mentor.name}</p>
          <p className={styles.receiptId}>ID запроса · {message.receipt.id}</p>
        </div>
      )}
    </div>
  );
}

function MentorDraft({ text }: { text: string }) {
  const draftRef = useRef<HTMLTextAreaElement>(null);
  const [feedback, setFeedback] = useState("");

  async function copy() {
    try {
      await navigator.clipboard.writeText(text);
      setFeedback("Черновик скопирован");
    } catch {
      draftRef.current?.focus();
      draftRef.current?.select();
      setFeedback("Текст выделен. Скопируйте его вручную.");
    }
  }

  return (
    <div className={styles.mentorDraft}>
      <label>
        Черновик для наставника
        <textarea ref={draftRef} rows={4} value={text} readOnly />
      </label>
      <Button variant="secondary" onClick={() => void copy()}>
        Скопировать черновик
      </Button>
      <span className={styles.copyFeedback} role="status">
        {feedback}
      </span>
    </div>
  );
}

export interface SourceLink {
  label: string;
  url: string;
}

export interface Mentor {
  id: string;
  name: string;
}

export interface ReviewRequest {
  id: string;
  status: "REQUESTED";
  mentor: Mentor;
  prNumber: number;
  headSha: string;
}

export interface ChatMessage {
  id: string;
  role: "user" | "assistant";
  text: string;
  links?: SourceLink[];
  mentorDraft?: string;
  receipt?: ReviewRequest;
}

export interface Finding {
  id: string;
  kind: "problem" | "assumption";
  title: string;
  description: string;
  location?: {
    path: string;
    line: number;
    url?: string;
  };
  rule?: {
    id: string;
    label: string;
    url: string;
  };
}

export interface ReviewRun {
  id: string;
  headSha: string;
  mentor: Mentor;
  pr: {
    number: number;
    title: string;
    repository: string;
    url: string;
  };
  findings: Finding[];
  checks: {
    status: "PASSED" | "FAILED" | "RUNNING" | "UNKNOWN";
    label: string;
  };
  createdAt: string;
  isStale?: boolean;
}

export type BusyState = "message" | "review" | "request";

export interface BuddyError {
  message: string;
  retryable: boolean;
}

export interface OperationResult {
  messages: ChatMessage[];
  review?: ReviewRun;
  reviewRequest?: ReviewRequest;
}

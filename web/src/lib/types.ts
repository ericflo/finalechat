export interface Settings {
  notify_all_messages: boolean;
  remote_mode: boolean;
}

export interface User {
  id: string;
  email: string;
  display_name: string;
  settings: Settings;
  created_at: string;
}

export interface Counts {
  pending_questions: number;
  unread_threads: number;
  /** Active, unmuted threads with a pending question or unread agent messages. */
  attention: number;
}

export type ActivityKind = "thinking" | "working" | "typing" | "waiting" | "tool";

/** What the agent says it is doing right now; lapses at expires_at. */
export interface Activity {
  text: string;
  kind: ActivityKind;
  at: string;
  since: string;
  expires_at: string;
}

export interface Thread {
  id: string;
  external_id: string | null;
  title: string;
  agent: string;
  meta: Record<string, unknown>;
  created_at: string;
  updated_at: string;
  last_activity_at: string;
  last_read_at: string;
  archived_at: string | null;
  muted: boolean;
  preview: string;
  preview_sender: string;
  /** One-to-two sentence summary of the thread (max 2000 chars). `summary` is an alias with identical value. */
  description: string;
  /** Alias of `description`; identical value. Prefer `description`. */
  summary: string;
  unread_count: number;
  pending_questions: number;
  activity: Activity | null;
}

export type Sender = "agent" | "user" | "system";

export interface Attachment {
  id: string;
  thread_id: string;
  message_id: string | null;
  kind: "image" | "file";
  content_type: string;
  filename: string;
  size: number;
  width?: number;
  height?: number;
  thumb_width?: number;
  thumb_height?: number;
  created_at: string;
  url: string;
  thumb_url?: string;
}

export interface Message {
  id: string;
  thread_id: string;
  sender: Sender;
  body: string;
  format: "markdown" | "text";
  importance: "normal" | "important";
  /**
   * Free-form metadata stored and returned verbatim (at most 16 KiB).
   *
   * The web client declares its eagent capability handshake (v1) here on
   * every user message and answer as `meta["eagent.client"]`, a
   * {@link EagentClientCapsule}. See `web/src/lib/clientCaps.ts`.
   */
  meta: Record<string, unknown>;
  /** "session" when posted from the app, "token" when an agent posted it. */
  origin: "session" | "token" | "";
  /** Set on catch-up pages for a message removed since: drop it locally. */
  deleted?: boolean;
  created_at: string;
  attachments: Attachment[];
}

export interface QuestionOption {
  label: string;
  description?: string;
}

/**
 * Client capability handshake (v1), sent as `meta["eagent.client"]` on user
 * messages and answers so eagent's reader (`capsFromMeta`) can see what
 * context this client supplies. Only these documented keys are ever sent:
 * no userAgent string, no canvas, no battery.
 */
export interface EagentClientCapsule {
  /** IANA timezone name, e.g. "America/Los_Angeles". */
  timezone?: string;
  /** BCP 47 locale from the browser, e.g. "en-US". */
  locale?: string;
  /** Coarse device class: "phone" | "tablet" | "desktop". */
  device?: string;
  /** Client name and version, e.g. "finalechat-web/0.1.0". */
  app?: string;
  /** CSS screen size, e.g. "1512x982". */
  screen?: string;
  /** Which of "tz", "locale", "screen" this capsule actually carries. */
  supplies: string[];
}

export interface Answer {
  selected: string[];
  text?: string;
}

export type QuestionStatus = "pending" | "answered" | "cancelled" | "expired" | "dismissed";

export interface Question {
  id: string;
  thread_id: string;
  prompt: string;
  options: QuestionOption[];
  allow_freeform: boolean;
  multi_select: boolean;
  status: QuestionStatus;
  answer: Answer | null;
  meta: Record<string, unknown>;
  created_at: string;
  answered_at: string | null;
  expires_at: string | null;
}

export interface APIToken {
  id: string;
  name: string;
  prefix: string;
  created_at: string;
  last_used_at: string | null;
}

export interface PushSubscriptionInfo {
  id: string;
  endpoint: string;
  user_agent: string;
  created_at: string;
  last_success_at: string | null;
  failure_count: number;
}

export type SignupMode = "first" | "open" | "invite" | "closed";

export interface AuthStatus {
  signup: SignupMode;
  authenticated: boolean;
  push_enabled: boolean;
  attachments_enabled: boolean;
  version: string;
  user?: User;
}

export interface Me {
  user: User;
  counts: Counts;
  push_enabled: boolean;
  attachments_enabled: boolean;
  base_url: string;
  version: string;
  auth: "session" | "token";
  storage?: { attachment_bytes: number; attachment_quota_bytes: number };
}

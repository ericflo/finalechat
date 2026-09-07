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
  unread_count: number;
  pending_questions: number;
}

export type Sender = "agent" | "user" | "system";

export interface Message {
  id: string;
  thread_id: string;
  sender: Sender;
  body: string;
  format: "markdown" | "text";
  importance: "normal" | "important";
  meta: Record<string, unknown>;
  created_at: string;
}

export interface QuestionOption {
  label: string;
  description?: string;
}

export interface Answer {
  selected: string[];
  text?: string;
}

export type QuestionStatus = "pending" | "answered" | "cancelled" | "expired";

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

export interface AuthStatus {
  signup: "open" | "invite" | "closed";
  authenticated: boolean;
  push_enabled: boolean;
  version: string;
  user?: User;
}

export interface Me {
  user: User;
  counts: Counts;
  push_enabled: boolean;
  base_url: string;
  version: string;
  auth: "session" | "token";
}

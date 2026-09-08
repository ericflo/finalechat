import type {
  APIToken,
  Attachment,
  AuthStatus,
  Counts,
  Me,
  Message,
  PushSubscriptionInfo,
  Question,
  Settings,
  Thread,
  User,
} from "./types";

export class APIError extends Error {
  status: number;
  code: string;
  constructor(status: number, code: string, message: string) {
    super(message);
    this.status = status;
    this.code = code;
  }
}

export async function request<T>(method: string, path: string, body?: unknown, init?: RequestInit): Promise<T> {
  const headers: Record<string, string> = { Accept: "application/json" };
  if (body !== undefined) headers["Content-Type"] = "application/json";
  let res: Response;
  try {
    res = await fetch(path, {
      method,
      headers,
      body: body === undefined ? undefined : JSON.stringify(body),
      credentials: "same-origin",
      ...init,
    });
  } catch {
    // fetch rejects only for transport failures; say so in plain words.
    throw new APIError(0, "network", "You're offline.");
  }
  if (res.status === 204) return undefined as T;
  let data: unknown = null;
  const text = await res.text();
  if (text) {
    try {
      data = JSON.parse(text);
    } catch {
      data = null;
    }
  }
  if (!res.ok) {
    const err = (data as { error?: { code?: string; message?: string } } | null)?.error;
    throw new APIError(res.status, err?.code ?? "http_error", err?.message ?? `Request failed (${res.status})`);
  }
  return data as T;
}

const base = "/api/v1";

export const api = {
  authStatus: () => request<AuthStatus>("GET", `${base}/auth/status`),
  register: (input: { email: string; password: string; display_name?: string; invite_code?: string }) =>
    request<{ user: User }>("POST", `${base}/auth/register`, input),
  login: (input: { email: string; password: string }) => request<{ user: User }>("POST", `${base}/auth/login`, input),
  logout: () => request<{ ok: true }>("POST", `${base}/auth/logout`, {}),

  me: () => request<Me>("GET", `${base}/me`),
  updateMe: (input: { display_name?: string; current_password?: string; new_password?: string }) =>
    request<{ user: User }>("PATCH", `${base}/me`, input),
  deleteMe: (password: string) => request<{ ok: true }>("DELETE", `${base}/me`, { password }),
  updateSettings: (input: Partial<Settings>) => request<{ settings: Settings }>("PATCH", `${base}/settings`, input),
  counts: () => request<{ counts: Counts }>("GET", `${base}/counts`),

  listTokens: () => request<{ tokens: APIToken[] }>("GET", `${base}/tokens`),
  createToken: (name: string) => request<{ token: APIToken; secret: string }>("POST", `${base}/tokens`, { name }),
  revokeToken: (id: string) => request<{ ok: true }>("DELETE", `${base}/tokens/${id}`),

  listThreads: (params: { archived?: boolean; cursor?: string; q?: string; limit?: number } = {}) => {
    const qs = new URLSearchParams();
    if (params.archived) qs.set("archived", "1");
    if (params.cursor) qs.set("cursor", params.cursor);
    if (params.q) qs.set("q", params.q);
    if (params.limit) qs.set("limit", String(params.limit));
    const suffix = qs.toString() ? `?${qs}` : "";
    return request<{ threads: Thread[]; next_cursor?: string }>("GET", `${base}/threads${suffix}`);
  },
  getThread: (id: string) => request<{ thread: Thread }>("GET", `${base}/threads/${id}`),
  getMessage: (id: string) => request<{ message: Message }>("GET", `${base}/messages/${encodeURIComponent(id)}`),
  updateThread: (id: string, patch: { title?: string; agent?: string; archived?: boolean; muted?: boolean }) =>
    request<{ thread: Thread }>("PATCH", `${base}/threads/${id}`, patch),
  deleteThread: (id: string) => request<{ ok: true }>("DELETE", `${base}/threads/${id}`),
  markRead: (id: string) => request<{ thread: Thread }>("POST", `${base}/threads/${id}/read`, {}),

  listMessages: (threadId: string, params: { before?: string; after?: string; limit?: number } = {}) => {
    const qs = new URLSearchParams();
    if (params.before) qs.set("before", params.before);
    if (params.after) qs.set("after", params.after);
    qs.set("limit", String(params.limit ?? 100));
    return request<{ messages: Message[]; has_more: boolean }>("GET", `${base}/threads/${threadId}/messages?${qs}`);
  },
  sendMessage: (threadId: string, body: string, attachments: string[] = [], clientKey?: string) =>
    request<{ message: Message; thread: Thread; created?: boolean }>("POST", `${base}/threads/${threadId}/messages`, {
      body,
      format: "markdown",
      sender: "user",
      attachments,
      client_key: clientKey,
    }),
  /** Uploads one file as a pending attachment; progress is reported in 0..1. */
  uploadAttachment: (threadId: string, file: File, onProgress?: (fraction: number) => void) =>
    new Promise<Attachment>((resolve, reject) => {
      const xhr = new XMLHttpRequest();
      xhr.open("POST", `${base}/threads/${threadId}/attachments`);
      xhr.setRequestHeader("Accept", "application/json");
      xhr.withCredentials = true;
      xhr.upload.onprogress = (ev) => {
        if (ev.lengthComputable && onProgress) onProgress(ev.loaded / ev.total);
      };
      xhr.onerror = () => reject(new APIError(0, "network", "Upload failed. Check your connection."));
      xhr.onload = () => {
        let data: { attachments?: Attachment[]; error?: { code?: string; message?: string } } | null = null;
        try {
          data = JSON.parse(xhr.responseText);
        } catch {
          data = null;
        }
        if (xhr.status >= 200 && xhr.status < 300 && data?.attachments?.[0]) resolve(data.attachments[0]);
        else reject(new APIError(xhr.status, data?.error?.code ?? "http_error", data?.error?.message ?? `Upload failed (${xhr.status})`));
      };
      const form = new FormData();
      form.append("file", file, file.name || "upload");
      xhr.send(form);
    }),

  listThreadQuestions: (threadId: string) => request<{ questions: Question[] }>("GET", `${base}/threads/${threadId}/questions`),
  listPendingQuestions: () => request<{ questions: Question[] }>("GET", `${base}/questions?status=pending&attention=true`),
  answerQuestion: (id: string, answer: { selected: string[]; text?: string }) =>
    request<{ question: Question; message: Message; thread: Thread }>("POST", `${base}/questions/${id}/answer`, answer),
  dismissQuestion: (id: string) => request<{ question: Question }>("POST", `${base}/questions/${id}/dismiss`, {}),

  vapid: () => request<{ enabled: boolean; public_key: string }>("GET", `${base}/push/vapid`),
  subscribePush: (sub: PushSubscriptionJSON) => request<{ subscription: PushSubscriptionInfo }>("POST", `${base}/push/subscriptions`, sub),
  unsubscribePush: (endpoint: string) => request<{ ok: true }>("DELETE", `${base}/push/subscriptions`, { endpoint }),
  listPushSubscriptions: () => request<{ subscriptions: PushSubscriptionInfo[] }>("GET", `${base}/push/subscriptions`),
  testPush: () => request<{ ok: true; devices: number }>("POST", `${base}/push/test`, {}),
};

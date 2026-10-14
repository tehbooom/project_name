export type AuthMode = "static" | "bearer";
export type Category = "nouns" | "adjectives";
export type WordStatus = "proposed" | "approved";

export interface Credential {
  mode: AuthMode;
  token: string;
}

export interface Word {
  id: string;
  value: string;
  category: Category;
  status: WordStatus;
  safe: boolean;
}

export interface WordPage {
  words: Word[];
  next_page_token?: string;
}

export interface NameConstraints {
  maxLength?: number;
  maxNounLength?: number;
  maxAdjectiveLength?: number;
  safe?: boolean;
}

export interface PublicConfig {
  unsafe_mode_enabled: boolean;
}

interface APIErrorPayload {
  error?: {
    code?: string;
    message?: string;
  };
}

export class APIError extends Error {
  constructor(
    message: string,
    readonly status: number,
    readonly code = "",
  ) {
    super(message);
    this.name = "APIError";
  }
}

function authHeaders(credential?: Credential): HeadersInit {
  if (!credential) return {};
  return credential.mode === "static"
    ? { "X-Admin-Token": credential.token }
    : { Authorization: `Bearer ${credential.token}` };
}

async function request<T>(
  path: string,
  options: RequestInit = {},
  credential?: Credential,
): Promise<T> {
  const headers = new Headers(options.headers);
  Object.entries(authHeaders(credential)).forEach(([key, value]) => headers.set(key, value));
  if (options.body) headers.set("Content-Type", "application/json");

  const response = await fetch(path, {
    ...options,
    headers,
    credentials: "same-origin",
  });
  if (response.status === 204) return undefined as T;

  const contentType = response.headers.get("Content-Type") ?? "";
  const payload = contentType.includes("application/json")
    ? (await response.json()) as T & APIErrorPayload
    : undefined;
  if (!response.ok) {
    throw new APIError(
      payload?.error?.message ?? `Request failed with status ${response.status}`,
      response.status,
      payload?.error?.code,
    );
  }
  return payload as T;
}

export const api = {
  async generate(constraints: NameConstraints = {}): Promise<string> {
    const query = new URLSearchParams();
    if (constraints.maxLength) query.set("max_length", String(constraints.maxLength));
    if (constraints.maxNounLength) {
      query.set("max_noun_length", String(constraints.maxNounLength));
    }
    if (constraints.maxAdjectiveLength) {
      query.set("max_adjective_length", String(constraints.maxAdjectiveLength));
    }
    if (constraints.safe === false) query.set("safe", "false");
    const suffix = query.size ? `?${query}` : "";
    const result = await request<{ name: string }>(`/v1/names/random${suffix}`);
    return result.name;
  },

  health(): Promise<Response> {
    return fetch("/health/ready", { cache: "no-store" });
  },

  config(): Promise<PublicConfig> {
    return request("/v1/config");
  },

  list(
    credential: Credential,
    category: Category,
    status: WordStatus | "",
    pageToken = "",
  ): Promise<WordPage> {
    const query = new URLSearchParams({ category, limit: "20" });
    if (status) query.set("status", status);
    if (pageToken) query.set("page_token", pageToken);
    return request(`/v1/words?${query}`, {}, credential);
  },

  propose(credential: Credential, category: Category, value: string): Promise<Word> {
    return request(
      "/v1/words",
      { method: "POST", body: JSON.stringify({ category, value }) },
      credential,
    );
  },

  approve(credential: Credential, id: string): Promise<Word> {
    return request(`/v1/words/${encodeURIComponent(id)}/approve`, { method: "POST" }, credential);
  },

  setSafety(credential: Credential, id: string, safe: boolean): Promise<Word> {
    return request(
      `/v1/words/${encodeURIComponent(id)}/safety`,
      { method: "PATCH", body: JSON.stringify({ safe }) },
      credential,
    );
  },

  remove(credential: Credential, id: string): Promise<void> {
    return request(`/v1/words/${encodeURIComponent(id)}`, { method: "DELETE" }, credential);
  },
};

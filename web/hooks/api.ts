import { useState, useCallback, useEffect } from "react";
import { User, Chat, Message, PaginatedResponse } from "../types";
import { shouldUpdateState, usePaginatedFetch } from "./utils";
import { fromIsoString } from "../utils";

// Base URL for the API
const API_BASE_URL = "/api";

// Helper function to handle fetch responses
async function handleResponse<T>(response: Response): Promise<T> {
  if (!response.ok) {
    const error = await response.json();
    throw new Error(error.message || error.error || "An error occurred");
  }
  return response.json();
}

// Helper function to get/set the stored JWT token
export function useToken() {
  const [token, setToken] = useState<string | null>();
  useEffect(() => {
    const tok = localStorage.getItem("token");
    if (tok) {
      setToken(tok);
    }
  }, [setToken]);
  const handleSetToken = useCallback((token: string | null) => {
    if (token) {
      localStorage.setItem("token", token);
    } else {
      localStorage.removeItem("token");
    }
    setToken(token);
  }, []);
  return [token, handleSetToken] as const;
}

// Helper function to set the Authorization header with the JWT token
const setAuthHeader = (token: string | null): HeadersInit => {
  return token ? { Authorization: token } : {};
};

export function useLogin(setToken: (token: string | null) => void) {
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const login = useCallback(
    async (email: string, password: string) => {
      setLoading(true);
      setError(null);
      try {
        const response = await fetch(`${API_BASE_URL}/login`, {
          method: "POST",
          headers: {
            "Content-Type": "application/json",
          },
          body: JSON.stringify({ email, password }),
        });
        const data = await handleResponse<{ token: string }>(response);
        setToken(data.token);
      } catch (err: any) {
        setError(err.message);
      } finally {
        setLoading(false);
      }
    },
    [setToken]
  );

  return { login, loading, error };
}

// New hook to handle user registration
export function useRegister(setToken: (token: string | null) => void) {
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const register = useCallback(
    async (email: string, password: string, username: string) => {
      setLoading(true);
      setError(null);
      try {
        const response = await fetch(`${API_BASE_URL}/register`, {
          method: "POST",
          headers: {
            "Content-Type": "application/json",
          },
          body: JSON.stringify({ email, password, username }),
        });
        const data = await handleResponse<User & { token: string }>(response);
        setToken(data.token);
      } catch (err: any) {
        setError(err.message);
      } finally {
        setLoading(false);
      }
    },
    [setToken]
  );

  return { register, loading, error };
}

export function useAuthUser(token: string | null) {
  const [user, setUser] = useState<User | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);

  const getAuthUser = useCallback(async () => {
    if (!token) {
      setUser(null);
      setLoading(false);
      setError(null);
      return;
    }
    setLoading(true);
    setError(null);
    try {
      const response = await fetch(`${API_BASE_URL}/auth/user`, {
        headers: setAuthHeader(token),
      });
      const data: User = await handleResponse(response);
      if (shouldUpdateState([user], [data])) {
        await setUser(data);
      }
    } catch (err: any) {
      setError(err.message);
    } finally {
      setLoading(false);
    }
  }, [token, user]);

  useEffect(() => {
    getAuthUser();
  }, [token, getAuthUser]);

  return { getAuthUser, user, loading, error };
}

// Hook to get chats
export function useGetChats(token: string | null) {
  // Create a factory function that returns the fetch function
  const fetchChatsFactory = useCallback(
    ({ reverse }: { reverse?: boolean }) => {
      return async (page: number, perPage: number) => {
        const queryParams = new URLSearchParams({
          page: page.toString(),
          per_page: perPage.toString(),
          order: reverse ? "asc" : "desc",
        });
        const response = await fetch(`${API_BASE_URL}/chats?${queryParams}`, {
          headers: setAuthHeader(token),
        });
        return handleResponse<PaginatedResponse<Chat>>(response);
      };
    },
    [token]
  );

  const sortFunc = useCallback((a: Chat, b: Chat): number => {
    return (
      //fromIsoString(b.created_timestamp).getTime() -
      //fromIsoString(a.created_timestamp).getTime()
      b.id - a.id
    );
  }, []);

  // Use the factory function with usePaginatedFetch
  const {
    data: chats,
    loading,
    error,
    fetchPaginatedData: getChats,
  } = usePaginatedFetch<Chat>(token, fetchChatsFactory, sortFunc);

  // Usage example: getChats({ maxPages: 5, perPage: 20 })
  return { getChats, chats, loading, error };
}

// Hook to create a chat
export function useCreateChat(token: string | null) {
  const [chat, setChat] = useState<Chat | null>(null);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const createChat = useCallback(
    async (model: string) => {
      setLoading(true);
      setError(null);
      try {
        const response = await fetch(`${API_BASE_URL}/chats`, {
          method: "POST",
          headers: {
            "Content-Type": "application/json",
            ...setAuthHeader(token),
          },
          body: JSON.stringify({ model }),
        });
        const data: Chat = await handleResponse(response);
        if (shouldUpdateState([chat], [data])) {
          setChat(data);
        }
        return data.id;
      } catch (err: any) {
        setError(err.message);
      } finally {
        setLoading(false);
      }
    },
    [token, chat]
  );

  return { createChat, chat, loading, error };
}

// Hook to get a single chat
export function useGetChat(token: string | null) {
  const [chat, setChat] = useState<Chat | null>(null);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const getChat = useCallback(
    async (chatId: number) => {
      if (!token) {
        setChat(null);
        setLoading(false);
        setError(null);
        return;
      }
      setLoading(true);
      setError(null);
      try {
        const response = await fetch(`${API_BASE_URL}/chats/${chatId}`, {
          headers: setAuthHeader(token),
        });
        const data: Chat = await handleResponse(response);
        if (shouldUpdateState([chat], [data])) {
          await setChat(data);
        }
      } catch (err: any) {
        setError(err.message);
      } finally {
        setLoading(false);
      }
    },
    [token, chat]
  );

  return { getChat, chat, loading, error };
}

// Hook to delete a chat
export function useDeleteChat(token: string | null) {
  const [deleted, setDeleted] = useState(false);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const deleteChat = useCallback(
    async (chatId: number) => {
      if (!token) {
        setDeleted(false);
        setLoading(false);
        setError(null);
        return;
      }
      setLoading(true);
      setError(null);
      try {
        const response = await fetch(`${API_BASE_URL}/chats/${chatId}`, {
          method: "DELETE",
          headers: setAuthHeader(token),
        });
        await handleResponse(response);
        setDeleted(true);
      } catch (err: any) {
        setError(err.message);
      } finally {
        setLoading(false);
      }
    },
    [token]
  );

  return { deleteChat, deleted, loading, error };
}

// Hook to get messages for a chat
export function useGetMessages(options: { token: string; sort?: boolean }) {
  const fetchMessagesFactory = useCallback(
    (config: { chatId: number; reverse: boolean }) => {
      return async (page: number, perPage: number) => {
        const { chatId } = config;
        const queryParams = new URLSearchParams({
          page: page.toString(),
          per_page: perPage.toString(),
          order: config.reverse ? "asc" : "desc",
        });
        const response = await fetch(
          `${API_BASE_URL}/chats/${chatId}/messages?${queryParams}`,
          {
            headers: setAuthHeader(options.token),
          }
        );
        return handleResponse<PaginatedResponse<Message>>(response);
      };
    },
    [options.token]
  );
  const sortFunc = useCallback((a: Message, b: Message): number => {
    return (
      //fromIsoString(a.timestamp).getTime() -
      //fromIsoString(b.timestamp).getTime()
      a.id - b.id
    );
  }, []);

  const {
    data: messages,
    loading,
    error,
    fetchPaginatedData: getMessages,
  } = usePaginatedFetch<Message>(
    options.token,
    fetchMessagesFactory,
    options?.sort ? sortFunc : null
  );

  // Usage example: getMessages({ maxPages: 5, perPage: 20, chatId: 123 })
  return { getMessages, messages, loading, error };
}

// Hook to create a message
export function useCreateMessage(token: string | null) {
  const [message, setMessage] = useState<Message | null>(null);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const createMessage = useCallback(
    async (chatId: number, text: string, senderType: string) => {
      if (!token) {
        setMessage(null);
        setLoading(false);
        setError(null);
        return;
      }
      setLoading(true);
      setError(null);
      try {
        const response = await fetch(
          `${API_BASE_URL}/chats/${chatId}/messages`,
          {
            method: "POST",
            headers: {
              "Content-Type": "application/json",
              ...setAuthHeader(token),
            },
            body: JSON.stringify({ text, sender_type: senderType }),
          }
        );
        const data: Message = await handleResponse(response);
        if (shouldUpdateState([message], [data])) {
          await setMessage(data);
        }
        return data.id;
      } catch (err: any) {
        setError(err.message);
      } finally {
        setLoading(false);
      }
    },
    [token, message]
  );

  return { createMessage, message, loading, error };
}

// Hook to get a single message
export function useGetMessage(token: string | null) {
  const [message, setMessage] = useState<Message | null>(null);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const getMessage = useCallback(
    async (messageId: number) => {
      if (!token) {
        setMessage(null);
        setLoading(false);
        setError(null);
        return;
      }
      setLoading(true);
      setError(null);
      try {
        const response = await fetch(`${API_BASE_URL}/messages/${messageId}`, {
          headers: setAuthHeader(token),
        });
        const data: Message = await handleResponse(response);
        if (shouldUpdateState([message], [data])) {
          await setMessage(data);
        }
      } catch (err: any) {
        setError(err.message);
      } finally {
        setLoading(false);
      }
    },
    [token, message]
  );

  return { getMessage, message, loading, error };
}

// Hook to update a message
export function useUpdateMessage(token: string | null) {
  const [updatedMessage, setUpdatedMessage] = useState<Message | null>(null);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const updateMessage = useCallback(
    async (messageId: number, text: string) => {
      if (!token) {
        setUpdatedMessage(null);
        setLoading(false);
        setError(null);
        return;
      }
      setLoading(true);
      setError(null);
      try {
        const response = await fetch(`${API_BASE_URL}/messages/${messageId}`, {
          method: "PUT",
          headers: {
            "Content-Type": "application/json",
            ...setAuthHeader(token),
          },
          body: JSON.stringify({ text }),
        });
        const data: Message = await handleResponse(response);
        if (shouldUpdateState([updatedMessage], [data])) {
          await setUpdatedMessage(data);
        }
      } catch (err: any) {
        setError(err.message);
      } finally {
        setLoading(false);
      }
    },
    [token, updatedMessage]
  );

  return { updateMessage, updatedMessage, loading, error };
}

// Hook to delete a message
export function useDeleteMessage(token: string | null) {
  const [deleted, setDeleted] = useState(false);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const deleteMessage = useCallback(
    async (messageId: number) => {
      if (!token) {
        setDeleted(false);
        setLoading(false);
        setError(null);
        return;
      }
      setLoading(true);
      setError(null);
      try {
        const response = await fetch(`${API_BASE_URL}/messages/${messageId}`, {
          method: "DELETE",
          headers: setAuthHeader(token),
        });
        await handleResponse(response);
        setDeleted(true);
      } catch (err: any) {
        setError(err.message);
      } finally {
        setLoading(false);
      }
    },
    [token]
  );

  return { deleteMessage, deleted, loading, error };
}

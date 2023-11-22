import { useState, useCallback, useMemo, use } from "react";
import { Chat, Message, PaginatedResponse } from "../types";
import { shouldUpdateState, usePaginatedFetch } from "./utils";
import { fromIsoString } from "../utils";

// Base URL for the API
const API_BASE_URL = "/api";

// Helper function to handle fetch responses
async function handleResponse<T>(response: Response): Promise<T> {
  if (!response.ok) {
    const error = await response.json();
    throw new Error(error.message || "An error occurred");
  }
  return response.json();
}

// Hook to get chats
export function useGetChats() {
  // Create a factory function that returns the fetch function
  const fetchChatsFactory = useCallback(
    ({ reverse }: { reverse?: boolean }) => {
      return async (page: number, perPage: number) => {
        const queryParams = new URLSearchParams({
          page: page.toString(),
          per_page: perPage.toString(),
          order: reverse ? "asc" : "desc",
        });
        const response = await fetch(`${API_BASE_URL}/chats?${queryParams}`);
        return handleResponse<PaginatedResponse<Chat>>(response);
      };
    },
    []
  );

  const sortFunc = useCallback((a: Chat, b: Chat): number => {
    return (
      fromIsoString(b.created_timestamp).getTime() -
      fromIsoString(a.created_timestamp).getTime()
    );
  }, []);

  // Use the factory function with usePaginatedFetch
  const {
    data: chats,
    loading,
    error,
    fetchPaginatedData: getChats,
  } = usePaginatedFetch<Chat>(fetchChatsFactory, sortFunc);

  // Usage example: getChats({ maxPages: 5, perPage: 20 })
  return { getChats, chats, loading, error };
}

// Hook to create a chat
export function useCreateChat() {
  const [chat, setChat] = useState<Chat | null>(null);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const createChat = useCallback(
    async (model: string) => {
      setLoading(true);
      setError(null);
      try {
        const queryParams = new URLSearchParams({
          model: model,
        });
        const response = await fetch(`${API_BASE_URL}/chats?${queryParams}`, {
          method: "POST",
          headers: {
            "Content-Type": "application/json",
          },
        });
        const data: Chat = await handleResponse(response);
        if (shouldUpdateState(chat, data)) {
          await setChat(data);
        }
        return data.id;
      } catch (err: any) {
        setError(err.message);
      } finally {
        setLoading(false);
      }
    },
    [chat]
  );

  return { createChat, chat, loading, error };
}

// Hook to get a single chat
export function useGetChat() {
  const [chat, setChat] = useState<Chat | null>(null);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const getChat = useCallback(
    async (chatId: number) => {
      setLoading(true);
      setError(null);
      try {
        const response = await fetch(`${API_BASE_URL}/chats/${chatId}`);
        const data: Chat = await handleResponse(response);
        if (shouldUpdateState(chat, data)) {
          await setChat(data);
        }
      } catch (err: any) {
        setError(err.message);
      } finally {
        setLoading(false);
      }
    },
    [chat]
  );

  return { getChat, chat, loading, error };
}

// Hook to delete a chat
export function useDeleteChat() {
  const [deleted, setDeleted] = useState(false);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const deleteChat = useCallback(async (chatId: number) => {
    setLoading(true);
    setError(null);
    try {
      const response = await fetch(`${API_BASE_URL}/chats/${chatId}`, {
        method: "DELETE",
      });
      await handleResponse(response);
      setDeleted(true);
    } catch (err: any) {
      setError(err.message);
    } finally {
      setLoading(false);
    }
  }, []);

  return { deleteChat, deleted, loading, error };
}

// Hook to get messages for a chat
export function useGetMessages(options: { sort?: boolean }) {
  const [optionsSort, _] = useState(options?.sort);

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
          `${API_BASE_URL}/chats/${chatId}/messages?${queryParams}`
        );
        return handleResponse<PaginatedResponse<Message>>(response);
      };
    },
    []
  );
  const sortFunc = useCallback((a: Message, b: Message): number => {
    return (
      fromIsoString(a.timestamp).getTime() -
      fromIsoString(b.timestamp).getTime()
    );
  }, []);

  const {
    data: messages,
    loading,
    error,
    fetchPaginatedData: getMessages,
  } = usePaginatedFetch<Message>(
    fetchMessagesFactory,
    options?.sort ? sortFunc : null
  );

  // Usage example: getMessages({ maxPages: 5, perPage: 20, chatId: 123 })
  return { getMessages, messages, loading, error };
}

// Hook to create a message
export function useCreateMessage() {
  const [message, setMessage] = useState<Message | null>(null);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const createMessage = useCallback(
    async (chatId: number, text: string, senderType: string) => {
      setLoading(true);
      setError(null);
      try {
        const response = await fetch(
          `${API_BASE_URL}/chats/${chatId}/messages`,
          {
            method: "POST",
            headers: {
              "Content-Type": "application/json",
            },
            body: JSON.stringify({ text, sender_type: senderType }),
          }
        );
        const data: Message = await handleResponse(response);
        if (shouldUpdateState(message, data)) {
          await setMessage(data);
        }
        return data.id;
      } catch (err: any) {
        setError(err.message);
      } finally {
        setLoading(false);
      }
    },
    [message]
  );

  return { createMessage, message, loading, error };
}

// Hook to get a single message
export function useGetMessage() {
  const [message, setMessage] = useState<Message | null>(null);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const getMessage = useCallback(
    async (messageId: number) => {
      setLoading(true);
      setError(null);
      try {
        const response = await fetch(`${API_BASE_URL}/messages/${messageId}`);
        const data: Message = await handleResponse(response);
        if (shouldUpdateState(message, data)) {
          await setMessage(data);
        }
      } catch (err: any) {
        setError(err.message);
      } finally {
        setLoading(false);
      }
    },
    [message]
  );

  return { getMessage, message, loading, error };
}

// Hook to update a message
export function useUpdateMessage() {
  const [updatedMessage, setUpdatedMessage] = useState<Message | null>(null);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const updateMessage = useCallback(
    async (messageId: number, text: string) => {
      setLoading(true);
      setError(null);
      try {
        const response = await fetch(`${API_BASE_URL}/messages/${messageId}`, {
          method: "PUT",
          headers: {
            "Content-Type": "application/json",
          },
          body: JSON.stringify({ text }),
        });
        const data: Message = await handleResponse(response);
        if (shouldUpdateState(updatedMessage, data)) {
          await setUpdatedMessage(data);
        }
      } catch (err: any) {
        setError(err.message);
      } finally {
        setLoading(false);
      }
    },
    [updatedMessage]
  );

  return { updateMessage, updatedMessage, loading, error };
}

// Hook to delete a message
export function useDeleteMessage() {
  const [deleted, setDeleted] = useState(false);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const deleteMessage = useCallback(async (messageId: number) => {
    setLoading(true);
    setError(null);
    try {
      const response = await fetch(`${API_BASE_URL}/messages/${messageId}`, {
        method: "DELETE",
      });
      await handleResponse(response);
      setDeleted(true);
    } catch (err: any) {
      setError(err.message);
    } finally {
      setLoading(false);
    }
  }, []);

  return { deleteMessage, deleted, loading, error };
}

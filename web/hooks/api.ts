import { useState, useCallback } from "react";
import { Chat, Message, PaginatedResponse } from "../types";

// Base URL for the API
const API_BASE_URL = "http://127.0.0.1:5000";

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
  const [chats, setChats] = useState<PaginatedResponse<Chat> | null>(null);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const getChats = useCallback(async (page?: number, perPage?: number) => {
    setLoading(true);
    setError(null);
    try {
      const queryParams = new URLSearchParams();
      if (page) queryParams.append("page", page.toString());
      if (perPage) queryParams.append("per_page", perPage.toString());
      const response = await fetch(`${API_BASE_URL}/chats?${queryParams}`);
      const data: PaginatedResponse<Chat> = await handleResponse(response);
      setChats(data);
    } catch (err: any) {
      setError(err.message);
    } finally {
      setLoading(false);
    }
  }, []);

  return { getChats, chats, loading, error };
}

// Hook to create a chat
export function useCreateChat() {
  const [chat, setChat] = useState<Chat | null>(null);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const createChat = useCallback(async () => {
    setLoading(true);
    setError(null);
    try {
      const response = await fetch(`${API_BASE_URL}/chats`, {
        method: "POST",
        headers: {
          "Content-Type": "application/json",
        },
      });
      const data: Chat = await handleResponse(response);
      setChat(data);
      return data.id;
    } catch (err: any) {
      setError(err.message);
    } finally {
      setLoading(false);
    }
  }, []);

  return { createChat, chat, loading, error };
}

// Hook to get a single chat
export function useGetChat() {
  const [chat, setChat] = useState<Chat | null>(null);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const getChat = useCallback(async (chatId: number) => {
    setLoading(true);
    setError(null);
    try {
      const response = await fetch(`${API_BASE_URL}/chats/${chatId}`);
      const data: Chat = await handleResponse(response);
      setChat(data);
    } catch (err: any) {
      setError(err.message);
    } finally {
      setLoading(false);
    }
  }, []);

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
export function useGetMessages() {
  const [messages, setMessages] = useState<PaginatedResponse<Message> | null>(
    null
  );
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const getMessages = useCallback(
    async (chatId: number, page?: number, perPage?: number) => {
      setLoading(true);
      setError(null);
      try {
        const queryParams = new URLSearchParams();
        if (page) queryParams.append("page", page.toString());
        if (perPage) queryParams.append("per_page", perPage.toString());
        const response = await fetch(
          `${API_BASE_URL}/chats/${chatId}/messages?${queryParams}`
        );
        const data: PaginatedResponse<Message> = await handleResponse(response);
        setMessages(data);
      } catch (err: any) {
        setError(err.message);
      } finally {
        setLoading(false);
      }
    },
    []
  );

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
        setMessage(data);
        return data.id;
      } catch (err: any) {
        setError(err.message);
      } finally {
        setLoading(false);
      }
    },
    []
  );

  return { createMessage, message, loading, error };
}

// Hook to get a single message
export function useGetMessage() {
  const [message, setMessage] = useState<Message | null>(null);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const getMessage = useCallback(async (messageId: number) => {
    setLoading(true);
    setError(null);
    try {
      const response = await fetch(`${API_BASE_URL}/messages/${messageId}`);
      const data: Message = await handleResponse(response);
      setMessage(data);
    } catch (err: any) {
      setError(err.message);
    } finally {
      setLoading(false);
    }
  }, []);

  return { getMessage, message, loading, error };
}

// Hook to update a message
export function useUpdateMessage() {
  const [updatedMessage, setUpdatedMessage] = useState<Message | null>(null);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const updateMessage = useCallback(async (messageId: number, text: string) => {
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
      setUpdatedMessage(data);
    } catch (err: any) {
      setError(err.message);
    } finally {
      setLoading(false);
    }
  }, []);

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

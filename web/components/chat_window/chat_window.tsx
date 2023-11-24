"use client";
// Code Context: React TypeScript with bootstrap 5.3.2 and bootstrap-icons injected

import React, { useCallback, useEffect, useState } from "react";
import MessageList from "./message_list";
import MessageInput from "./message_input";
import {
  useGetMessages,
  useCreateMessage,
  useCreateChat,
  useGetChat,
} from "../../hooks/api";
import { DEFAULT_MODEL_NAME } from "../../constants";

const DEFAULT_POLL_INTERVAL = 1000;

export interface ChatWindowProps {
  token: string;
  setToken: (token: string) => void;
  chatId: number;
  pollInterval?: number;
  onChatCreated: (chatId: number) => void;
}

const ChatWindow = ({
  token,
  setToken,
  chatId,
  pollInterval,
  onChatCreated,
}: ChatWindowProps) => {
  const {
    getMessages,
    messages,
    loading: messagesLoading,
    error: messagesError,
  } = useGetMessages({ token, sort: true });
  const { getChat, chat, loading: chatLoading } = useGetChat(token);
  const [modelName, setModelName] = useState<string>(DEFAULT_MODEL_NAME);
  const { createMessage, error: messageError } = useCreateMessage(token);
  const { createChat, error: chatError } = useCreateChat(token);
  const [isPolling, _setIsPolling] = useState(true);

  useEffect(() => {
    if (token && chatId > 0) {
      getChat(chatId);
      getMessages({ chatId });
    }
  }, [token, chatId]);

  // Set isPolling to false when the window is unfocused
  useEffect(() => {
    const handleVisibilityChange = () => {
      _setIsPolling(!document.hidden);
    };
    document.addEventListener("visibilitychange", handleVisibilityChange);
    return () => {
      document.removeEventListener("visibilitychange", handleVisibilityChange);
    };
  }, []);

  useEffect(() => {
    let intervalId;
    if (isPolling && chatId > 0) {
      intervalId = setInterval(
        () => getMessages({ chatId }),
        pollInterval || DEFAULT_POLL_INTERVAL
      );
    }
    return () => clearInterval(intervalId);
  }, [chatId, isPolling, getMessages]);

  const handleSendMessage = useCallback(
    (text: string) => {
      (async () => {
        if (chatId < 0) {
          const newChatId = await createChat(modelName);
          await createMessage(newChatId, text, "user");
          onChatCreated(newChatId);
          return;
        } else {
          await createMessage(chatId, text, "user");
          getMessages({ chatId });
        }
      })();
    },
    [chatId, modelName, createChat, createMessage, getMessages, onChatCreated]
  );

  return (
    <div className="d-flex flex-column h-100">
      {chatError && <div className="alert alert-danger">{chatError}</div>}
      {messagesError && (
        <div className="alert alert-danger">{messagesError}</div>
      )}
      {messageError && <div className="alert alert-danger">{messageError}</div>}
      <MessageList
        chat={chatLoading ? null : chat}
        messages={chatId > 0 ? messages : []}
        modelName={modelName}
        onModelChange={setModelName}
      />
      <MessageInput chatId={chatId} onSendMessage={handleSendMessage} />
    </div>
  );
};

export default ChatWindow;
